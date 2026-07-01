package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestToolRegistryValidatesSchemaPolicyAndApprovals(t *testing.T) {
	policy, err := NewCommandPolicy(ModeReadonly, []string{`(?i)\brm\b`}, DefaultReadonlyCommands())
	if err != nil {
		t.Fatal(err)
	}
	filePolicy := newTestFilePolicy(t)
	registry := NewToolRegistry(policy)
	registry.RegisterMany(
		&ReadFileTool{policy: filePolicy},
		&WriteFileTool{policy: filePolicy},
		&RunCommandTool{policy: policy},
		&MemoryTool{allowWrite: true},
		&BrowserClickTool{},
	)

	if allowed, reason := registry.ToolAllowedReason("write_file"); allowed || !strings.Contains(reason, "readonly") {
		t.Fatalf("readonly mode should hide write_file, allowed=%v reason=%q", allowed, reason)
	}
	if allowed, reason := registry.ToolAllowedReason("browser_click"); allowed || !strings.Contains(reason, "readonly") {
		t.Fatalf("readonly mode should hide browser_click, allowed=%v reason=%q", allowed, reason)
	}
	if allowed, reason := registry.ToolAllowedReason("read_file"); !allowed || reason != "" {
		t.Fatalf("read_file should be visible in readonly, allowed=%v reason=%q", allowed, reason)
	}
	if err := registry.ValidateCall("read_file", map[string]any{"path": "x", "limit": 1.5}); err == nil {
		t.Fatal("schema validation should reject non-integer limit")
	}
	if err := registry.ValidateCall("memory", map[string]any{"action": "unknown"}); err == nil {
		t.Fatal("schema validation should reject unknown memory action")
	}

	if err := registry.SetMode(ModeAutopilot); err != nil {
		t.Fatal(err)
	}
	writeArgs := map[string]any{"path": "out.txt", "content": "hello"}
	if err := registry.AuthorizeCall("write_file", writeArgs); err != nil {
		t.Fatalf("write_file should authorize inside workspace in autopilot: %v", err)
	}
	request, ok := registry.ApprovalRequest("write_file", writeArgs)
	if !ok || !request.Required || !strings.Contains(request.Prompt, "WRITE_FILE") {
		t.Fatalf("write_file should require unified approval, request=%#v ok=%v", request, ok)
	}
	if registry.RequiresApproval("memory", map[string]any{"action": "recall"}) {
		t.Fatal("memory recall should not require approval")
	}
	if !registry.RequiresApproval("memory", map[string]any{"action": "save", "target": "agent", "content": "note"}) {
		t.Fatal("memory save should require approval")
	}
	if !registry.RequiresApproval("run_command", map[string]any{"command": "rm -rf tmp"}) {
		t.Fatal("dangerous command should require approval in autopilot")
	}
}

func TestToolRegistryAuthorizesFilePolicyBeforeExecute(t *testing.T) {
	policy, err := NewCommandPolicy(ModeAutopilot, nil, DefaultReadonlyCommands())
	if err != nil {
		t.Fatal(err)
	}
	filePolicy := newTestFilePolicy(t)
	registry := NewToolRegistry(policy)
	registry.RegisterMany(
		&WriteFileTool{policy: filePolicy},
		NewViewImageTool("https://example.invalid/v1", "key", "model", filePolicy),
	)

	if err := registry.AuthorizeCall("write_file", map[string]any{"path": "blocked/out.txt", "content": "x"}); err == nil {
		t.Fatal("write_file should reject blocked paths during registry authorization")
	}
	if err := registry.AuthorizeCall("view_image", map[string]any{"path": "../escape.png"}); err == nil {
		t.Fatal("view_image should reject paths outside workspace during registry authorization")
	}
}

func TestToolRegistryExecuteSetsAuditContextAndFormatsErrors(t *testing.T) {
	policy, err := NewCommandPolicy(ModeReadonly, nil, DefaultReadonlyCommands())
	if err != nil {
		t.Fatal(err)
	}
	registry := NewToolRegistry(policy)
	tool := &auditContextTool{}
	registry.Register(tool)

	call := ToolCall{ID: "call_123"}
	call.Func.Name = "audit_context"
	output, err := registry.ExecuteContext(context.Background(), "request_456", call, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if output != "ok" || tool.requestID != "request_456" || tool.toolCallID != "call_123" {
		t.Fatalf("registry did not execute with audit context, output=%q request=%q call=%q", output, tool.requestID, tool.toolCallID)
	}

	formatted := registry.FormatResult(ToolExecutionResult{Name: "audit_context", Err: errors.New("boom")})
	if formatted != "错误: boom" {
		t.Fatalf("unexpected formatted error: %q", formatted)
	}
}

func newTestFilePolicy(t *testing.T) *FilePolicy {
	t.Helper()
	policy, err := NewFilePolicy(FilePolicyConfig{
		BaseDir:        t.TempDir(),
		WorkspaceRoots: []string{"."},
		BlockedPaths:   []string{"blocked"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

type auditContextTool struct {
	requestID  string
	toolCallID string
}

func (t *auditContextTool) Definition() ToolDef {
	return ToolDef{Type: "function", Function: ToolFunction{
		Name:        "audit_context",
		Description: "test tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}}
}

func (t *auditContextTool) Execute(map[string]any) (string, error) {
	return "ok", nil
}

func (t *auditContextTool) SetAuditContext(requestID, toolCallID string) {
	t.requestID = requestID
	t.toolCallID = toolCallID
}
