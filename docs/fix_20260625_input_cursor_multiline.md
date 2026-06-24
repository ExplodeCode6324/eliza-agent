# fix_20260625_input_cursor_multiline.md

> **日期**: 2026-06-25  
> **类型**: 热修复  
> **影响**: TUI 输入体验

---

## 触发条件

1. 用户在输入框内打错字，无法用方向键移动光标修改
2. 用户粘贴多行文本，每行被当作独立消息发送，Agent 陷入死循环

## 根因

`readTerminalLine()` 使用 `bufio.Reader.ReadString('\n')`，完全依赖终端 cooked mode：
- Cooked mode 下内核行编辑不暴露方向键
- 粘贴的换行符被内核解释为 Enter，逐行提交

## 修复方案

新增 `input.go`，引入 raw terminal mode + 自定义行编辑器。

### 核心设计

- 缓冲区使用 `[]rune`（非 `[]byte`），确保中日韩字符正确处理
- 多字节 UTF-8 序列（如中文 3 字节）完整读取后解码为单个 rune
- 光标移动、删除、重绘均以 rune 为单位
- Backspace/Delete 删除一个完整 rune（而非一个字节）

### 新增文件

| 文件 | 职责 |
|------|------|
| `input.go` | 光标编辑 + 粘贴检测 + 多行文件模式 |
| `terminal_unix.go` | Unix raw mode 切换（ioctl tcsetattr） |
| `terminal_windows.go` | Windows 降级 stub |

### 光标编辑

- 进入 raw mode，逐字节读取 stdin
- 处理：方向键（移动）、Backspace/Delete、Home/End、Ctrl+A/E/U
- 每次按键后重绘整行（`\r` + 清屏 + 内容 + 光标定位）

### 多行/超长处理

- 检测到输入含 `\n` 或长度 >500 → 写入 `/tmp/eliza-input-*.txt`
- 返回 `FILE:/path` 前缀
- Agent 层自动读取文件内容并清理临时文件
- Pipe 模式（非 TTY）自动降级到 bufio fallback，不受影响

### 修改文件

| 文件 | 改动 |
|------|------|
| `agent.go` | `prepareMessages()` 增加 `FILE:` 前缀检测；新增 `os` import |
| `tools.go` | `readTerminalLine()` 委托给 `readLineInput()` |

## 验证

- `go build` ✅
- `go vet` ✅
- Pipe 模式 fallback ✅
- TTY 交互模式：需在真实终端中测试方向键和粘贴
