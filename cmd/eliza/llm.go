package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OpenAI-compatible API types.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Reasoning  string     `json:"reasoning_content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID   string       `json:"id"`
	Type string       `json:"type"`
	Func ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatRequest struct {
	Model      string    `json:"model"`
	Messages   []Message `json:"messages"`
	Tools      []ToolDef `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
	Stream     bool      `json:"stream"`
	StreamOpts any       `json:"stream_options,omitempty"`
}

type usageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage          usageInfo `json:"usage"`
	UsageEstimated bool      `json:"-"`
	Incomplete     bool      `json:"-"`
	StreamError    string    `json:"-"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int          `json:"index"`
				ID       string       `json:"id"`
				Type     string       `json:"type"`
				Function ToolCallFunc `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageInfo `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type LLMConfig struct {
	RequestTimeout time.Duration
	ConnectTimeout time.Duration
	MaxRetries     int
	BackoffMax     time.Duration
}

func DefaultLLMConfig() LLMConfig {
	return LLMConfig{
		RequestTimeout: 120 * time.Second,
		ConnectTimeout: 10 * time.Second,
		MaxRetries:     2,
		BackoffMax:     4 * time.Second,
	}
}

type StreamCallbacks struct {
	OnContent   func(string)
	OnReasoning func(string)
	OnRetry     func(attempt int, delay time.Duration, reason string)
}

type LLMClient struct {
	BaseURL string
	APIKey  string
	Model   string
	Config  LLMConfig

	HTTPClient *http.Client

	mu                    sync.Mutex
	TotalPromptTokens     int
	TotalCompletionTokens int
	LastPromptTokens      int
	LastTotalTokens       int
	LastUsageEstimated    bool
	AuxPromptTokens       int
	AuxCompletionTokens   int
}

func NewLLMClient(baseURL, apiKey, model string) *LLMClient {
	return NewLLMClientWithConfig(baseURL, apiKey, model, DefaultLLMConfig())
}

func NewLLMClientWithConfig(baseURL, apiKey, model string, cfg LLMConfig) *LLMClient {
	defaults := DefaultLLMConfig()
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaults.RequestTimeout
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = defaults.ConnectTimeout
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = defaults.BackoffMax
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   cfg.ConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ResponseHeaderTimeout: cfg.ConnectTimeout,
	}
	return &LLMClient{
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:     apiKey,
		Model:      model,
		Config:     cfg,
		HTTPClient: &http.Client{Transport: transport},
	}
}

// Chat is retained as a convenience wrapper, but it always uses SSE streaming.
func (c *LLMClient) Chat(messages []Message, tools []ToolDef) (*chatResponse, error) {
	return c.ChatContext(context.Background(), messages, tools, StreamCallbacks{})
}

func (c *LLMClient) ChatContext(ctx context.Context, messages []Message, tools []ToolDef, callbacks StreamCallbacks) (*chatResponse, error) {
	return c.chat(ctx, messages, tools, false, callbacks)
}

// ChatAuxiliary is used by compaction, planning, and summaries. It uses the
// same streaming implementation but keeps its usage separate from the main chat.
func (c *LLMClient) ChatAuxiliary(messages []Message, tools []ToolDef) (*chatResponse, error) {
	return c.ChatAuxiliaryContext(context.Background(), messages, tools)
}

func (c *LLMClient) ChatAuxiliaryContext(ctx context.Context, messages []Message, tools []ToolDef) (*chatResponse, error) {
	return c.chat(ctx, messages, tools, true, StreamCallbacks{})
}

func (c *LLMClient) chat(parent context.Context, messages []Message, tools []ToolDef, auxiliary bool, callbacks StreamCallbacks) (*chatResponse, error) {
	request := chatRequest{
		Model: c.Model, Messages: messages, Tools: tools,
		ToolChoice: "auto", Stream: true,
		StreamOpts: map[string]bool{"include_usage": true},
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ctx := parent
	var cancel context.CancelFunc
	if c.Config.RequestTimeout > 0 {
		ctx, cancel = context.WithTimeout(parent, c.Config.RequestTimeout)
		defer cancel()
	}

	var lastErr error
	for attempt := 0; attempt <= c.Config.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, classifyContextError(err)
		}
		response, retry, reason, err := c.doStream(ctx, body, callbacks)
		if err == nil {
			c.recordUsage(response, messages, auxiliary)
			return response, nil
		}
		lastErr = err
		if !retry || attempt >= c.Config.MaxRetries {
			return response, err
		}
		delay := c.retryDelay(attempt)
		if callbacks.OnRetry != nil {
			callbacks.OnRetry(attempt+1, delay, reason)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return response, classifyContextError(ctx.Err())
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (c *LLMClient) doStream(ctx context.Context, body []byte, callbacks StreamCallbacks) (*chatResponse, bool, string, error) {
	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, "request", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, "cancelled", classifyContextError(ctx.Err())
		}
		return nil, true, "connection", fmt.Errorf("LLM connection failed: %w", err)
	}
	defer resp.Body.Close()

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		err := fmt.Errorf("LLM API HTTP %d: %s", resp.StatusCode, sanitizeAPIError(preview))
		return nil, retryableStatus(resp.StatusCode), "http " + strconv.Itoa(resp.StatusCode), err
	}
	if !strings.Contains(contentType, "text/event-stream") {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, false, "incompatible", fmt.Errorf(
			"LLM endpoint does not support required SSE streaming (content-type=%q, body=%s)",
			resp.Header.Get("Content-Type"), sanitizeAPIError(preview),
		)
	}

	assembled := &chatResponse{}
	var message Message
	message.Role = "assistant"
	toolCalls := map[int]*ToolCall{}
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	gotDone := false
	gotSemanticData := false
	finishReason := ""

	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
				// SSE metadata and keep-alives are intentionally ignored.
			} else if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data == "[DONE]" {
					gotDone = true
					break
				}
				if data != "" {
					var chunk streamChunk
					if err := json.Unmarshal([]byte(data), &chunk); err != nil {
						assembled.Incomplete = gotSemanticData
						assembled.StreamError = "invalid_sse_json"
						finalizeStreamResponse(assembled, message, toolCalls, finishReason)
						return assembled, !gotSemanticData, "invalid SSE", fmt.Errorf("invalid SSE data: %w", err)
					}
					if chunk.Error != nil {
						assembled.Incomplete = gotSemanticData
						assembled.StreamError = chunk.Error.Type
						finalizeStreamResponse(assembled, message, toolCalls, finishReason)
						return assembled, !gotSemanticData, "SSE error", fmt.Errorf("LLM stream error: %s (%s)", chunk.Error.Message, chunk.Error.Type)
					}
					if chunk.Usage != nil {
						assembled.Usage = *chunk.Usage
					}
					for _, choice := range chunk.Choices {
						delta := choice.Delta
						if delta.Content != "" {
							gotSemanticData = true
							message.Content += delta.Content
							if callbacks.OnContent != nil {
								callbacks.OnContent(delta.Content)
							}
						}
						reasoning := delta.ReasoningContent
						if reasoning == "" {
							reasoning = delta.Reasoning
						}
						if reasoning != "" {
							gotSemanticData = true
							message.Reasoning += reasoning
							if callbacks.OnReasoning != nil {
								callbacks.OnReasoning(reasoning)
							}
						}
						for _, deltaCall := range delta.ToolCalls {
							gotSemanticData = true
							call := toolCalls[deltaCall.Index]
							if call == nil {
								call = &ToolCall{Type: "function"}
								toolCalls[deltaCall.Index] = call
							}
							if deltaCall.ID != "" {
								call.ID += deltaCall.ID
							}
							if deltaCall.Type != "" {
								call.Type = deltaCall.Type
							}
							call.Func.Name += deltaCall.Function.Name
							call.Func.Arguments += deltaCall.Function.Arguments
						}
						if choice.FinishReason != "" {
							finishReason = choice.FinishReason
						}
					}
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			if ctx.Err() != nil {
				assembled.Incomplete = gotSemanticData
				assembled.StreamError = "cancelled"
				finalizeStreamResponse(assembled, message, toolCalls, finishReason)
				return assembled, false, "cancelled", classifyContextError(ctx.Err())
			}
			assembled.Incomplete = gotSemanticData
			assembled.StreamError = "read_error"
			finalizeStreamResponse(assembled, message, toolCalls, finishReason)
			return assembled, !gotSemanticData, "stream read", fmt.Errorf("read SSE stream: %w", readErr)
		}
	}

	finalizeStreamResponse(assembled, message, toolCalls, finishReason)
	if !gotDone {
		assembled.Incomplete = gotSemanticData
		assembled.StreamError = "missing_done"
		return assembled, !gotSemanticData, "truncated stream", fmt.Errorf("SSE stream ended before [DONE]")
	}
	if !gotSemanticData && len(assembled.Choices) == 0 {
		return assembled, false, "empty stream", fmt.Errorf("SSE stream completed without assistant data")
	}
	return assembled, false, "", nil
}

func finalizeStreamResponse(response *chatResponse, message Message, calls map[int]*ToolCall, finishReason string) {
	if len(calls) > 0 {
		indexes := make([]int, 0, len(calls))
		for index := range calls {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			if call := calls[index]; call != nil {
				message.ToolCalls = append(message.ToolCalls, *call)
			}
		}
	}
	if strings.TrimSpace(message.Content) == "" && strings.TrimSpace(message.Reasoning) == "" && len(message.ToolCalls) == 0 {
		return
	}
	response.Choices = nil
	response.Choices = append(response.Choices, struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	}{Message: message, FinishReason: finishReason})
}

func (c *LLMClient) recordUsage(response *chatResponse, messages []Message, auxiliary bool) {
	if response == nil {
		return
	}
	usage := response.Usage
	if usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 && usage.TotalTokens <= 0 {
		usage.PromptTokens = estimateContextTokens(messages)
		if len(response.Choices) > 0 {
			usage.CompletionTokens = estimateContextTokens([]Message{response.Choices[0].Message})
		}
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		response.Usage = usage
		response.UsageEstimated = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.TotalPromptTokens += usage.PromptTokens
	c.TotalCompletionTokens += usage.CompletionTokens
	if auxiliary {
		c.AuxPromptTokens += usage.PromptTokens
		c.AuxCompletionTokens += usage.CompletionTokens
	} else {
		c.LastPromptTokens = usage.PromptTokens
		c.LastTotalTokens = usage.TotalTokens
		c.LastUsageEstimated = response.UsageEstimated
	}
}

func (c *LLMClient) retryDelay(attempt int) time.Duration {
	base := 250 * time.Millisecond
	delay := base * time.Duration(1<<min(attempt, 6))
	if delay > c.Config.BackoffMax {
		delay = c.Config.BackoffMax
	}
	jitter := time.Duration(rand.Int63n(int64(delay/3 + 1)))
	return delay + jitter
}

func retryableStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
}

func classifyContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("LLM request cancelled: %w", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("LLM request timeout: %w", err)
	}
	return err
}

func sanitizeAPIError(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 800 {
		text = text[:800] + "..."
	}
	if text == "" {
		return "empty response body"
	}
	return redactText(text)
}
