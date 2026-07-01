# fix: 新增 edit_file 和 glob 工具

**日期**: 2026-07-01
**参考**: learn-claude-code s02_tool_use (shareAI-lab)
**影响文件**: `cmd/eliza/tools.go`, `cmd/eliza/main.go`, `cmd/eliza/tool_registry_test.go`

---

## 动机

对标 Claude Code、Codex CLI、Hermes Agent 的工具集，ELIZA 缺少两个基础编程原语：

| 操作 | 当前做法 | 问题 |
|------|----------|------|
| 局部编辑文件 | `read_file` 全量读 + LLM 脑中改 + `write_file` 全量写 | 大文件 token 浪费；LLM 可能篡改无关段落 |
| 查找文件 | `run_command find ...` | find 语法复杂，不同 LLM 写出不同结果；shell 转义坑多 |

## 新增工具

### edit_file

外科手术式文件编辑：LLM 只需传 `(path, old_text, new_text)`，工具做精确匹配替换。

- 精确匹配：old_text 必须在文件中存在，否则返回错误
- 单次替换：仅替换第一个匹配，文件其余部分完全不变
- 多处匹配警告：如果匹配到多处，仅替换第一处并提示
- FilePolicy 约束：受 workspace roots / blocked paths 限制
- readonly 模式阻止（写操作），需审批

```go
// 调用示例：修改 src.go 中某一行
edit_file {
  "path": "src.go",
  "old_text": "println(\"hello\")",
  "new_text": "fmt.Println(\"hello world\")"
}
```

### glob

文件模式匹配：传一个 glob pattern，返回工作区内匹配的文件列表。

- 支持标准 glob 通配符：`*`, `?`, `[...]`, `**`
- FilePolicy 过滤：仅返回 workspace roots 内且不在 blocked paths 中的文件
- 只读操作，readonly 模式开放，无需审批
- 无匹配返回友好提示

```go
// 调用示例：查找所有 Go 测试文件
glob {
  "pattern": "cmd/**/*_test.go"
}
```

## 实现细节

| 属性 | edit_file | glob |
|------|-----------|------|
| 分类 | file | file |
| 只读 | false | true |
| 审批策略 | ToolApprovalAlways | ToolApprovalNever |
| 适用 Profile | code_review, ops | 全部 4 个 |
| 核心实现 | `strings.Replace(text, old, new, 1)` | `filepath.Glob` + workspace root 过滤 |

两个工具均通过 ToolRegistry 注册，走统一的 ValidateCall → AuthorizeCall → ApprovalRequest → ExecuteContext 管线。

## 测试

新增 3 个测试（tool_registry_test.go）：

| 测试 | 覆盖 |
|------|------|
| `TestEditFileReplacesExactMatchAndRejectsMissing` | 精确替换 + 文件内容验证 + 不匹配报错 |
| `TestGlobFindsFilesAndFiltersByPolicy` | `*.go` / `*_test.go` 匹配 + 非目标文件过滤 + 空结果提示 |
| `TestEditFileAndGlobAuthorizeByPolicy` | readonly 阻止 edit_file / 开放 glob + 审批触发 + blocked path 过滤 |

## 验证结果

```bash
go test ./cmd/eliza/   # 28 tests PASS (含 3 新增)
go vet ./cmd/eliza/     # PASS
eliza --version         # ELIZA Agent v0.9.0
```

ELIZA 工具集从 13 个增加到 15 个。
