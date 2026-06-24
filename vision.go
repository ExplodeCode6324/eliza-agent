package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ─── view_image tool ────────────────────────────────────────────────
//
// Reads an image file (png/jpeg/gif/webp) and sends it to a vision-capable
// LLM endpoint for understanding. Returns the model's text response.
//
// Backend auto-detection:
//   - URL contains "generativelanguage" → Google Gemini format
//   - Otherwise → OpenAI-compatible format (default for internal endpoints)
//
// Configured via .env:
//   ELIZA_VISION_BASE_URL   Vision API endpoint (falls back to ELIZA_BASE_URL)
//   ELIZA_VISION_API_KEY    Vision API key (falls back to ELIZA_API_KEY)
//   ELIZA_VISION_MODEL      Vision model name (falls back to ELIZA_MODEL)

type ViewImageTool struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func NewViewImageTool(baseURL, apiKey, model string) *ViewImageTool {
	if baseURL == "" {
		baseURL = os.Getenv("ELIZA_BASE_URL")
	}
	if apiKey == "" {
		apiKey = os.Getenv("ELIZA_API_KEY")
	}
	if model == "" {
		model = os.Getenv("ELIZA_MODEL")
	}
	return &ViewImageTool{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		model:   strings.TrimSpace(model),
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (t *ViewImageTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "view_image",
			Description: "读取图片文件并调用内网视觉模型进行理解。用于识别截图、终端输出、错误信息等。参数: path (必填,图片路径), prompt (可选,理解指令,如'截图里有什么错误？')",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "图片文件路径（受 FilePolicy 约束，仅允许 png/jpeg/gif/webp）",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "理解指令（可选，默认'请描述这张图片的内容'）",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *ViewImageTool) Execute(args map[string]any) (string, error) {
	return t.ExecuteContext(context.Background(), args)
}

func (t *ViewImageTool) ExecuteContext(ctx context.Context, args map[string]any) (string, error) {
	if strings.TrimSpace(t.apiKey) == "" || strings.TrimSpace(t.baseURL) == "" {
		return "", fmt.Errorf("vision API 未配置: 请设置 ELIZA_VISION_BASE_URL 和 ELIZA_VISION_API_KEY")
	}

	path, _ := args["path"].(string)
	prompt, _ := args["prompt"].(string)
	if path == "" {
		return "", fmt.Errorf("缺少 path 参数")
	}
	if prompt == "" {
		prompt = "请描述这张图片的内容"
	}

	// Read file (no FilePolicy — view_image uses its own MIME check)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取图片失败: %w", err)
	}

	mime := detectImageMIME(data)
	if mime == "" {
		return "", fmt.Errorf("不支持的文件类型: %s (仅允许 png/jpeg/gif/webp)", path)
	}

	// Cap image size at 10MB to prevent abuse
	if len(data) > 10*1024*1024 {
		return "", fmt.Errorf("图片过大 (%d bytes, 限制 10MB)", len(data))
	}

	b64 := base64.StdEncoding.EncodeToString(data)

	if isGemini(t.baseURL) {
		return t.callGemini(ctx, b64, mime, prompt)
	}
	return t.callOpenAI(ctx, b64, mime, prompt)
}

// ─── Backend detection ──────────────────────────────────────────────

func isGemini(baseURL string) bool {
	return strings.Contains(strings.ToLower(baseURL), "generativelanguage")
}

// ─── OpenAI-compatible backend ──────────────────────────────────────

type openaiVisionReq struct {
	Model    string                `json:"model"`
	Messages []openaiVisionMessage `json:"messages"`
}

type openaiVisionMessage struct {
	Role    string                `json:"role"`
	Content []openaiVisionContent `json:"content"`
}

type openaiVisionContent struct {
	Type     string             `json:"type"`
	Text     string             `json:"text,omitempty"`
	ImageURL *openaiImageURL    `json:"image_url,omitempty"`
}

type openaiImageURL struct {
	URL string `json:"url"`
}

type openaiVisionResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (t *ViewImageTool) callOpenAI(ctx context.Context, b64, mime, prompt string) (string, error) {
	body, _ := json.Marshal(openaiVisionReq{
		Model: t.model,
		Messages: []openaiVisionMessage{{
			Role: "user",
			Content: []openaiVisionContent{
				{Type: "text", Text: prompt},
				{Type: "image_url", ImageURL: &openaiImageURL{
					URL: "data:" + mime + ";base64," + b64,
				}},
			},
		}},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("vision API HTTP %d: %s", resp.StatusCode, string(raw[:min(len(raw), 500)]))
	}

	var parsed openaiVisionResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("vision API 响应解析失败: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("vision API 错误: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("vision API 返回空 choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// ─── Gemini backend ─────────────────────────────────────────────────

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string          `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inline_data,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (t *ViewImageTool) callGemini(ctx context.Context, b64, mime, prompt string) (string, error) {
	body, _ := json.Marshal(geminiRequest{
		Contents: []geminiContent{{
			Parts: []geminiPart{
				{Text: prompt},
				{InlineData: &geminiInlineData{
					MimeType: mime,
					Data:     b64,
				}},
			},
		}},
	})

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", t.baseURL, t.model, t.apiKey)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("vision API HTTP %d: %s", resp.StatusCode, string(raw[:min(len(raw), 500)]))
	}

	var parsed geminiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("vision API 响应解析失败: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("vision API 错误: %s", parsed.Error.Message)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("vision API 返回空 candidates")
	}

	var result strings.Builder
	for _, part := range parsed.Candidates[0].Content.Parts {
		result.WriteString(part.Text)
	}
	return result.String(), nil
}

// ─── MIME detection ─────────────────────────────────────────────────

func detectImageMIME(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	// PNG
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	// JPEG
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	// GIF
	if len(data) >= 6 && data[0] == 'G' && data[1] == 'I' && data[2] == 'F' {
		return "image/gif"
	}
	// WebP
	if len(data) >= 12 && data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P' {
		return "image/webp"
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
