# fix: TUI 重构为事件驱动架构

**日期**: 2026-07-01
**提交**: `e909280` refactor: decouple tui components
**影响文件**: `cmd/eliza/ui.go`, `cmd/eliza/ui_events.go` (新), `cmd/eliza/ui_components.go` (新), `cmd/eliza/agent.go`, `go.mod`

---

## 动机

原 TUI 实现中，Agent 逻辑直接调用 `Renderer` 的方法（`Status()`, `Tool()`, `UserMessage()` 等），耦合紧密。新增功能时需要同时在 Agent 和 Renderer 两端修改，且难以对 UI 层做独立测试。

## 重构方案

### 事件驱动层 (`ui_events.go`)

定义三组接口，将 Agent 逻辑与渲染器解耦：

**UIEvent（Agent → UI）：** 9 种事件类型
- `StatusUIEvent` — 状态消息
- `UserMessageUIEvent` / `AssistantStartedUIEvent` / `AssistantDeltaUIEvent` / `AssistantDoneUIEvent` — 对话流
- `ToolCallUIEvent` — 工具调用结果
- `RawTextUIEvent` — 原始文本输出（stdout/stderr）
- `TitleUIEvent` / `ApprovalRequestedUIEvent` — 标题和审批

**UICommand（UI → Agent）：** 3 种命令类型
- `UserSubmittedUICommand` — 用户提交消息
- `ApprovalSelectedUICommand` — 审批选择
- `CancelRequestedUICommand` — 取消请求

**UISink 接口：** 预留多输出端支持（如未来同时输出到终端和日志）。

**AgentUI 封装：** 包装 `Renderer`，`Emit()` 方法根据事件类型分发到对应渲染方法。Agent 层只需 `ui.Emit(StatusUIEvent{...})` 即可。

### 声明式组件 (`ui_components.go`)

将 UI 元素抽象为组件，每个组件实现 `Lines(ctx)` 方法返回预包装的行列表：

| 组件 | 用途 |
|------|------|
| `MessageComponent` | 用户/Agent 消息块（带边框和颜色） |
| `StatusComponent` | 状态行 |
| `ToolCallComponent` | 工具调用结果 |
| `InputBarComponent` | INPUT/GUIDE 输入栏标签 |
| `ApprovalComponent` | 审批对话框 |

**UIComponentContext** 统一注入宽度、配色、Unicode 模式，组件内部处理窄终端自动换行。

### 测试增强

- `ui_components_test.go` — 4 个组件测试：窄宽度溢出、输入栏标签、事件渲染、删除重绘
- `tui_pty_unix_test.go` — 真实 PTY 终端测试：光标移动 + 退格删除 + CJK 输入 + CRLF 保证

新增依赖：`github.com/creack/pty`（仅测试，生产无影响）

## 文件变更

| 文件 | 变更 |
|------|------|
| `ui_events.go` | 新增 (248 行) |
| `ui_components.go` | 新增 (230 行) |
| `ui_components_test.go` | 新增 (94 行) |
| `tui_pty_unix_test.go` | 新增 (80 行) |
| `ui.go` | 精简 -85 行 |
| `agent.go` | 适配 AgentUI |
| `go.mod` / `go.sum` | 新增 creack/pty |

## 验证

```bash
go test ./cmd/eliza/   # 32 tests PASS（含 4 新增 TUI + 1 PTY）
go vet ./cmd/eliza/     # PASS
CGO_ENABLED=0 go build  # PASS
```
