package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestEditFileReplacesExactMatchAndRejectsMissing(t *testing.T) {
	policy := newTestFilePolicy(t)
	registry := NewToolRegistry(nil)
	registry.Register(&EditFileTool{policy: policy})

	// 创建测试文件
	src := filepath.Join(policy.baseDir, "src.go")
	original := "package main\n\nfunc main() {\n	println(\"hello\")\n}\n"
	if err := os.WriteFile(src, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// 精确替换
	output, err := registry.ExecuteContext(context.Background(), "req_1", ToolCall{Func: ToolCallFunc{Name: "edit_file"}}, map[string]any{
		"path":     "src.go",
		"old_text": "println(\"hello\")",
		"new_text": "fmt.Println(\"hello world\")",
	})
	if err != nil {
		t.Fatalf("edit_file should succeed: %v", err)
	}
	if !strings.Contains(output, "已编辑") {
		t.Fatalf("unexpected output: %s", output)
	}

	// 验证内容
	content, _ := os.ReadFile(src)
	if !strings.Contains(string(content), "fmt.Println") {
		t.Fatal("file was not edited correctly")
	}
	if strings.Contains(string(content), "println(") {
		t.Fatal("old text should be gone after edit")
	}

	// 不存在的文本应报错
	_, err = registry.ExecuteContext(context.Background(), "req_2", ToolCall{Func: ToolCallFunc{Name: "edit_file"}}, map[string]any{
		"path":     "src.go",
		"old_text": "this text does not exist",
		"new_text": "anything",
	})
	if err == nil || !strings.Contains(err.Error(), "未在文件中找到") {
		t.Fatalf("missing old_text should return error, got: %v", err)
	}
}

func TestGlobFindsFilesAndFiltersByPolicy(t *testing.T) {
	policy := newTestFilePolicy(t)
	registry := NewToolRegistry(nil)
	registry.Register(&GlobTool{policy: policy})

	// 创建一些 .go 文件
	for _, name := range []string{"a.go", "b.go", "c_test.go", "readme.md"} {
		if err := os.WriteFile(filepath.Join(policy.baseDir, name), []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// glob 匹配 *.go
	output, err := registry.ExecuteContext(context.Background(), "req_1", ToolCall{Func: ToolCallFunc{Name: "glob"}}, map[string]any{
		"pattern": filepath.Join(policy.baseDir, "*.go"),
	})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if !strings.Contains(output, "a.go") || !strings.Contains(output, "b.go") || !strings.Contains(output, "c_test.go") {
		t.Fatalf("glob *.go should find all .go files, got: %s", output)
	}
	if strings.Contains(output, "readme.md") {
		t.Fatal("glob *.go should not match .md files")
	}

	// glob 匹配 *_test.go
	output2, err := registry.ExecuteContext(context.Background(), "req_2", ToolCall{Func: ToolCallFunc{Name: "glob"}}, map[string]any{
		"pattern": filepath.Join(policy.baseDir, "*_test.go"),
	})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if !strings.Contains(output2, "c_test.go") {
		t.Fatalf("glob *_test.go should find test file, got: %s", output2)
	}
	if strings.Contains(output2, "a.go") || strings.Contains(output2, "b.go") {
		t.Fatal("glob *_test.go should not find regular .go files")
	}

	// glob 无匹配时的响应
	output3, err := registry.ExecuteContext(context.Background(), "req_3", ToolCall{Func: ToolCallFunc{Name: "glob"}}, map[string]any{
		"pattern": filepath.Join(policy.baseDir, "*.xyz"),
	})
	if err != nil {
		t.Fatalf("empty glob should not error: %v", err)
	}
	if !strings.Contains(output3, "未找到匹配的文件") {
		t.Fatalf("empty glob should return info message, got: %s", output3)
	}
}

func TestEditFileAndGlobAuthorizeByPolicy(t *testing.T) {
	policy := newTestFilePolicy(t)
	cmdPolicy, err := NewCommandPolicy(ModeReadonly, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	edit := &EditFileTool{policy: policy}
	g := &GlobTool{policy: policy}

	// edit_file 应被 readonly 阻止
	readonlyRegistry := NewToolRegistry(cmdPolicy)
	readonlyRegistry.RegisterMany(edit, g)
	if readonlyRegistry.ToolAllowed("edit_file") {
		t.Fatal("edit_file should be blocked in readonly mode")
	}
	if !readonlyRegistry.ToolAllowed("glob") {
		t.Fatal("glob should be allowed in readonly mode")
	}

	// edit_file 应触发审批
	autopilotRegistry := NewToolRegistry(cmdPolicy)
	autopilotRegistry.RegisterMany(edit, g)
	if err := autopilotRegistry.SetMode(ModeAutopilot); err != nil {
		t.Fatal(err)
	}
	if !autopilotRegistry.RequiresApproval("edit_file", map[string]any{"path": "x", "old_text": "a", "new_text": "b"}) {
		t.Fatal("edit_file should require approval")
	}
	if autopilotRegistry.RequiresApproval("glob", map[string]any{"pattern": "*.go"}) {
		t.Fatal("glob should not require approval")
	}

	// glob 拒绝 blocked path 内的文件
	blockedPath := filepath.Join(policy.baseDir, "blocked", "secret.txt")
	os.MkdirAll(filepath.Dir(blockedPath), 0755)
	os.WriteFile(blockedPath, []byte("secret"), 0644)
	output, err := readonlyRegistry.ExecuteContext(context.Background(), "req_1", ToolCall{Func: ToolCallFunc{Name: "glob"}}, map[string]any{
		"pattern": filepath.Join(policy.baseDir, "blocked", "*.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "未找到") {
		t.Fatalf("glob should filter blocked paths, got: %s", output)
	}
}
