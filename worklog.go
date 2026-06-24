package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const worklogSchemaVersion = "1.0"

type WorklogEvent struct {
	SchemaVersion   string         `json:"schema_version"`
	Timestamp       string         `json:"timestamp"`
	Sequence        uint64         `json:"sequence"`
	SessionID       string         `json:"session_id"`
	RequestID       string         `json:"request_id,omitempty"`
	EventID         string         `json:"event_id"`
	EventType       string         `json:"event_type"`
	Status          string         `json:"status"`
	ToolCallID      string         `json:"tool_call_id,omitempty"`
	Sensitivity     string         `json:"sensitivity"`
	RedactionStatus string         `json:"redaction_status"`
	Payload         map[string]any `json:"payload,omitempty"`
}

// WorklogBuilder is the single write gateway for conversation, LLM, tool,
// plan, memory, skill, policy, and UI events.
type WorklogBuilder struct {
	mu          sync.Mutex
	startTime   time.Time
	cfg         *Config
	enabled     bool
	sessionID   string
	dir         string
	eventsPath  string
	sessionPath string
	artifacts   string
	sequence    uint64
	eventsFile  *os.File
	sessionFile *os.File
	queries     []string
	closed      bool
}

func NewWorklogBuilder(cfg *Config) *WorklogBuilder {
	w := &WorklogBuilder{startTime: time.Now(), cfg: cfg, sessionID: randomID()}
	if cfg == nil || !cfg.Worklog.Enabled {
		return w
	}
	w.enabled = true
	root := cfg.Worklog.Dir
	if root == "" {
		root = filepath.Join(appBaseDir(), "worklogs")
	}
	dateDir := filepath.Join(root, time.Now().Format("2006-01-02"))
	w.dir = filepath.Join(dateDir, "session_"+w.sessionID)
	w.eventsPath = filepath.Join(w.dir, "events.jsonl")
	w.sessionPath = filepath.Join(w.dir, "session.md")
	w.artifacts = filepath.Join(w.dir, "artifacts")
	if err := os.MkdirAll(w.artifacts, 0755); err != nil {
		w.enabled = false
		return w
	}
	var err error
	w.eventsFile, err = os.OpenFile(w.eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		w.enabled = false
		return w
	}
	w.sessionFile, err = os.OpenFile(w.sessionPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		_ = w.eventsFile.Close()
		w.enabled = false
		return w
	}
	if info, statErr := w.sessionFile.Stat(); statErr == nil && info.Size() == 0 {
		fmt.Fprintf(w.sessionFile, "# ELIZA 会话记录\n\n- session_id: `%s`\n- started_at: %s\n- version: %s\n\n", w.sessionID, w.startTime.Format(time.RFC3339), version)
		_ = w.sessionFile.Sync()
	}
	_ = w.recordLocked("session.started", "started", "", "", map[string]any{
		"version":       version,
		"profile":       os.Getenv("ELIZA_PROFILE"),
		"os":            cfg.System.OS,
		"arch":          cfg.System.Architecture,
		"model":         cfg.Model.Name,
		"config_source": "binary-adjacent .env and config",
	})
	return w
}

func (w *WorklogBuilder) SessionID() string   { return w.sessionID }
func (w *WorklogBuilder) SessionPath() string { return w.sessionPath }
func (w *WorklogBuilder) Directory() string   { return w.dir }

func (w *WorklogBuilder) AddQuery(q string) {
	w.mu.Lock()
	w.queries = append(w.queries, q)
	w.mu.Unlock()
}

func (w *WorklogBuilder) RecordEvent(eventType, status, requestID, toolCallID string, payload map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.recordLocked(eventType, status, requestID, toolCallID, payload)
}

func (w *WorklogBuilder) recordLocked(eventType, status, requestID, toolCallID string, payload map[string]any) error {
	if !w.enabled || w.closed || w.eventsFile == nil {
		return nil
	}
	w.sequence++
	safePayload, redacted := sanitizePayload(payload)
	event := WorklogEvent{
		SchemaVersion:   worklogSchemaVersion,
		Timestamp:       time.Now().UTC().Format(time.RFC3339Nano),
		Sequence:        w.sequence,
		SessionID:       w.sessionID,
		RequestID:       requestID,
		EventID:         "evt_" + randomID(),
		EventType:       eventType,
		Status:          status,
		ToolCallID:      toolCallID,
		Sensitivity:     "internal",
		RedactionStatus: map[bool]string{true: "redacted", false: "not_needed"}[redacted],
		Payload:         safePayload,
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode worklog event: %w", err)
	}
	maxEvent := 16 * 1024
	if w.cfg != nil && w.cfg.Worklog.MaxEventBytes > 0 {
		maxEvent = w.cfg.Worklog.MaxEventBytes
	}
	if maxEvent < 1024 {
		maxEvent = 1024
	}
	if len(encoded) > maxEvent {
		event.Payload = map[string]any{"summary": summarizeText(fmt.Sprintf("%v", safePayload), maxEvent/2), "event_payload_truncated": true}
		event.RedactionStatus = "redacted"
		encoded, err = json.Marshal(event)
		if err != nil {
			return err
		}
	}
	if _, err := w.eventsFile.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if err := w.eventsFile.Sync(); err != nil {
		return err
	}
	w.projectLocked(event)
	return nil
}

func (w *WorklogBuilder) projectLocked(event WorklogEvent) {
	if w.sessionFile == nil {
		return
	}
	var text string
	switch event.EventType {
	case "conversation.user":
		if large, _ := event.Payload["large"].(bool); large {
			return
		}
		text = fmt.Sprintf("## USER\n\n%s\n\n", valueString(event.Payload["content"]))
	case "conversation.assistant":
		if large, _ := event.Payload["large"].(bool); large {
			return
		}
		marker := ""
		if event.Status == "incomplete" {
			marker = " (incomplete)"
		}
		text = fmt.Sprintf("## ELIZA%s\n\n%s\n\n", marker, valueString(event.Payload["content"]))
	case "tool.completed", "tool.failed", "tool.denied", "tool.cancelled":
		text = fmt.Sprintf("### TOOL %s [%s]\n\n- tool_call_id: `%s`\n- duration_ms: %v\n- result: %s\n\n",
			valueString(event.Payload["name"]), strings.ToUpper(event.Status), event.ToolCallID,
			event.Payload["duration_ms"], valueString(event.Payload["summary"]))
	case "plan.created", "plan.updated", "plan.completed", "plan.cancelled":
		text = fmt.Sprintf("### PLAN [%s]\n\n%s\n\n", strings.ToUpper(event.Status), valueString(event.Payload["summary"]))
	case "session.ended":
		text = fmt.Sprintf("---\nSession ended at %s (%s).\n", event.Timestamp, valueString(event.Payload["reason"]))
	default:
		return
	}
	_, _ = io.WriteString(w.sessionFile, text)
	_ = w.sessionFile.Sync()
}

func (w *WorklogBuilder) RecordConversation(role, content, requestID, status string) {
	eventType := "conversation." + role
	maxEvent := 16 * 1024
	if w.cfg != nil && w.cfg.Worklog.MaxEventBytes > 0 {
		maxEvent = w.cfg.Worklog.MaxEventBytes
	}
	if len(content) <= maxEvent/2 {
		_ = w.RecordEvent(eventType, status, requestID, "", map[string]any{"content": content})
		return
	}
	maxArtifact := int64(8 * 1024 * 1024)
	if w.cfg != nil && w.cfg.Worklog.MaxArtifactBytes > 0 {
		maxArtifact = w.cfg.Worklog.MaxArtifactBytes
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	artifact, err := w.writeArtifact(role+"_"+requestID, content, maxArtifact)
	payload := map[string]any{"content": summarizeText(content, 600), "large": true}
	if err == nil {
		payload["artifact"] = artifact
	} else {
		payload["artifact_error"] = err.Error()
	}
	_ = w.recordLocked(eventType, status, requestID, "", payload)
	if w.sessionFile != nil {
		marker := ""
		if status == "incomplete" {
			marker = " (incomplete)"
		}
		fmt.Fprintf(w.sessionFile, "## %s%s\n\n%s\n\n", strings.ToUpper(role), marker, redactText(content))
		_ = w.sessionFile.Sync()
	}
}

func (w *WorklogBuilder) RecordTool(requestID string, call ToolCall, args map[string]any, output string, toolErr error, elapsed time.Duration, status string) {
	payload := map[string]any{
		"name":        call.Func.Name,
		"arguments":   summarizeArguments(args),
		"duration_ms": elapsed.Milliseconds(),
		"summary":     summarizeText(output, 600),
		"truncated":   strings.Contains(output, "TRUNCATED"),
	}
	if exit := extractExit(output); exit != "" {
		payload["exit_code"] = exit
	}
	if strings.Contains(output, "[timeout=") {
		payload["timeout"] = true
	}
	if toolErr != nil {
		payload["error"] = toolErr.Error()
	}
	maxEvent := 16 * 1024
	maxArtifact := int64(8 * 1024 * 1024)
	if w.cfg != nil {
		if w.cfg.Worklog.MaxEventBytes > 0 {
			maxEvent = w.cfg.Worklog.MaxEventBytes
		}
		if w.cfg.Worklog.MaxArtifactBytes > 0 {
			maxArtifact = w.cfg.Worklog.MaxArtifactBytes
		}
	}
	if len(output) <= maxEvent {
		payload["output"] = output
	} else {
		w.mu.Lock()
		if artifact, err := w.writeArtifact(call.ID, output, maxArtifact); err == nil {
			payload["artifact"] = artifact
		} else {
			payload["artifact_error"] = err.Error()
		}
		_ = w.recordLocked("tool."+status, status, requestID, call.ID, payload)
		w.mu.Unlock()
		return
	}
	_ = w.RecordEvent("tool."+status, status, requestID, call.ID, payload)
}

func (w *WorklogBuilder) writeArtifact(callID, content string, maxBytes int64) (map[string]any, error) {
	if !w.enabled || w.closed || w.artifacts == "" {
		return nil, fmt.Errorf("worklog disabled")
	}
	name := fmt.Sprintf("%06d_%s.txt", w.sequence+1, safeFilename(callID))
	path := filepath.Join(w.artifacts, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	sourceSize := len(content)
	content = redactText(content)
	reader := strings.NewReader(content)
	written, copyErr := io.Copy(file, io.LimitReader(reader, maxBytes))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if syncErr != nil {
		return nil, syncErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	hash := sha256.Sum256([]byte(content))
	return map[string]any{
		"path":          filepath.ToSlash(filepath.Join("artifacts", name)),
		"size":          written,
		"original_size": sourceSize,
		"truncated":     int64(len(content)) > maxBytes,
		"sha256":        hex.EncodeToString(hash[:]),
	}, nil
}

func (w *WorklogBuilder) Close(reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	_ = w.recordLocked("session.ended", "completed", "", "", map[string]any{"reason": reason})
	w.closed = true
	var first error
	if w.eventsFile != nil {
		if err := w.eventsFile.Close(); err != nil {
			first = err
		}
	}
	if w.sessionFile != nil {
		if err := w.sessionFile.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Build and BuildWithSummary remain for deterministic emergency exports.
func (w *WorklogBuilder) Build(llm *LLMClient, cfg *Config) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var builder strings.Builder
	builder.WriteString("# ELIZA 工作记录\n\n")
	fmt.Fprintf(&builder, "**会话时间**: %s → %s\n\n", w.startTime.Format("2006-01-02 15:04:05"), time.Now().Format("15:04:05"))
	if cfg != nil {
		fmt.Fprintf(&builder, "**模型**: %s\n", cfg.Model.Name)
	}
	if llm != nil {
		fmt.Fprintf(&builder, "**Token 消耗**: prompt=%d completion=%d total=%d\n\n", llm.TotalPromptTokens, llm.TotalCompletionTokens, llm.TotalPromptTokens+llm.TotalCompletionTokens)
	}
	if len(w.queries) > 0 {
		builder.WriteString("## 用户查询\n\n")
		for index, query := range w.queries {
			fmt.Fprintf(&builder, "%d. %s\n", index+1, redactText(query))
		}
	}
	return builder.String()
}

func (w *WorklogBuilder) BuildWithSummary(llm *LLMClient, cfg *Config, summary string) string {
	return w.Build(llm, cfg) + "\n## 会话摘要\n\n" + redactText(strings.TrimSpace(summary)) + "\n"
}

func (w *WorklogBuilder) ExportSummary(llm *LLMClient, cfg *Config, summary, dir string) (string, error) {
	if dir == "" {
		dir = filepath.Join(appBaseDir(), "worklogs")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	filename := filepath.Join(dir, fmt.Sprintf("%s_summary.md", time.Now().Format("2006-01-02_150405")))
	if err := os.WriteFile(filename, []byte(w.BuildWithSummary(llm, cfg, summary)), 0644); err != nil {
		return "", err
	}
	_ = w.RecordEvent("summary.generated", "completed", "", "", map[string]any{"path": filename})
	return filename, nil
}

func sanitizePayload(payload map[string]any) (map[string]any, bool) {
	if payload == nil {
		return nil, false
	}
	result := make(map[string]any, len(payload))
	redacted := false
	for key, value := range payload {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "api_key") || strings.Contains(lower, "authorization") || strings.Contains(lower, "token") && lower != "tokens" {
			result[key] = "***"
			redacted = true
			continue
		}
		switch typed := value.(type) {
		case string:
			safe := redactText(typed)
			if safe != typed {
				redacted = true
			}
			result[key] = safe
		case map[string]any:
			safe, changed := sanitizePayload(typed)
			result[key] = safe
			redacted = redacted || changed
		default:
			result[key] = value
		}
	}
	return result, redacted
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|authorization|password|passwd|secret|token)\s*[:=]\s*[^\s,;]+`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`),
}

func redactText(text string) string {
	for _, pattern := range secretPatterns {
		text = pattern.ReplaceAllStringFunc(text, func(match string) string {
			if index := strings.IndexAny(match, "=:"); index >= 0 {
				return match[:index+1] + "***"
			}
			return "***"
		})
	}
	return text
}

func summarizeArguments(args map[string]any) map[string]any {
	result := make(map[string]any, len(args))
	for key, value := range args {
		if strings.HasPrefix(key, "_eliza_") {
			continue
		}
		text := fmt.Sprintf("%v", value)
		if len(text) > 500 {
			text = text[:500] + "..."
		}
		result[key] = text
	}
	return result
}

func summarizeText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

func safeFilename(value string) string {
	if value == "" {
		return "artifact"
	}
	var builder strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			builder.WriteRune(char)
		}
	}
	if builder.Len() == 0 {
		return "artifact"
	}
	return builder.String()
}

func valueString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

func readLastCompleteEvent(path string) (*WorklogEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	var last WorklogEvent
	for scanner.Scan() {
		var event WorklogEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		last = event
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if last.EventID == "" {
		return nil, fmt.Errorf("no complete event")
	}
	return &last, nil
}
