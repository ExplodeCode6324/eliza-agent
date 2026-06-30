# fix_20260701_input_paste_cjk_redraw.md

> **日期**: 2026-07-01  
> **类型**: 热修复（输入稳定性 + UX）  
> **影响**: TUI 输入框 — 长文本粘贴折叠为临时文件；中文输入法提交后即时显示

---

## 触发条件

1. 用户在底部输入框粘贴多行文本时，不同终端表现不一致：
   - 有的终端把粘贴换行识别为 Enter，导致多行内容被逐行发送
   - 有的终端不会逐行发送，但会在输入区重复重绘大量内容，把上方消息刷走
2. 用户使用中文输入法输入时，候选词最后一次快速提交的汉字不会立即显示。例如输入“这是一条测试消息”时，“消息”可能已经进入缓冲区，但终端上要等下一次按键或空格才显示。

## 根因

原输入实现仍然依赖手写 raw terminal 解析和时间间隔启发式：

- 多行粘贴主要靠 `gap < 50ms` 判断换行是否来自粘贴流，这会受终端、SSH、tmux、输入法和慢速粘贴影响
- 粘贴内容直接进入可见 buffer，`redrawLine()` 会对多行 buffer 做 cursor-up + clear-to-end 重绘，容易在部分终端产生重复打印和刷屏
- 为了降低粘贴刷屏风险，曾加入 `5ms` 内突发输入不重绘的逻辑；中文输入法经常一次性提交多个 UTF-8 字符，因此后一个汉字会进入 buffer 但跳过 redraw
- `terminal_unix.go` 直接使用 `syscall.TCGETS/TCSETS`，在 macOS 上不可用，导致 `go test ./...` 构建失败

## 修复方案

### A: 使用 bracketed paste 协议识别粘贴

进入 raw mode 后向终端写入：

```
ESC[?2004h
```

退出 raw mode 时关闭：

```
ESC[?2004l
```

支持 bracketed paste 的终端会把粘贴内容包裹为：

```
ESC[200~ pasted content ESC[201~
```

`readLineRaw()` 现在会识别 `ESC[200~`，整块读取到 `ESC[201~`，不再把粘贴流里的换行误判为手动 Enter。

### B: 多行/超长粘贴折叠为临时文件引用

粘贴内容满足以下任一条件时写入 `/tmp/eliza-input-*.txt`：

- 包含 `\n`
- 长度超过 500 字节

输入框里只插入短占位符：

```
[Pasted text #1: 12 lines -> /tmp/eliza-input-xxxx.txt]
```

用户提交时再展开占位符，读取真实内容；如果最终内容仍然是多行或超长，则沿用 `FILE:/tmp/eliza-input-*.txt` 进入 Agent 层，由 `prepareMessages()` 读取并清理。

### C: 非 bracketed paste 终端保留 fallback

如果终端不发 `ESC[200~` / `ESC[201~`，仍保留 `50ms` 换行检测作为兜底：

- 粘贴流里的 `\r` / `\n` 只进入 buffer，不触发发送
- 后续检测到用户输入间隔恢复到 `>= 50ms` 时，把已积累的多行 buffer 折叠为临时文件引用，再重绘输入框

这样不支持 bracketed paste 的终端也不会逐行发送或刷屏。

### D: 中文输入即时重绘

移除了普通字符路径上的 `5ms` 突发不重绘判断：

- ASCII 和 UTF-8 字符插入后立即 `redrawLine()`
- 只有已经检测到“粘贴换行”的 fallback 粘贴流才延迟重绘
- 中文输入法一次性提交多个汉字时，每个已解码 rune 都会立即显示

### E: 统一 raw terminal 实现

删除平台分叉的 raw mode 文件，改用 `golang.org/x/term`：

| 旧文件 | 处理 |
|--------|------|
| `terminal_unix.go` | 删除 |
| `terminal_windows.go` | 删除 |
| `terminal.go` | 新增；使用 `term.MakeRaw()` / `term.Restore()` |

## 改动文件

| 文件 | 改动 |
|------|------|
| `input.go` | bracketed paste 解析；粘贴临时文件占位符；提交前展开；fallback 粘贴折叠；中文输入即时重绘 |
| `input_test.go` | 新增粘贴折叠、占位符展开、bracketed paste 结束符解析、中文输入法快速提交重绘测试 |
| `terminal.go` | 新增 `golang.org/x/term` raw terminal 实现 |
| `terminal_unix.go` | 删除 syscall ioctl 实现 |
| `terminal_windows.go` | 删除空 stub |
| `go.mod` / `go.sum` | 新增 `golang.org/x/term` 依赖；Go 版本升级到 1.25.0 |

## 数据流（修复后）

### 支持 bracketed paste 的终端

```
粘贴多行
  ↓
ESC[200~ ... ESC[201~
  ↓
readBracketedPaste() 整块读取
  ↓
含换行 / 超长 → 写入 /tmp/eliza-input-*.txt
  ↓
输入框显示 [Pasted text #N: ... -> path]
  ↓
用户手动 Enter
  ↓
expandPasteReferences() 展开真实内容
  ↓
FILE:/tmp/eliza-input-*.txt → Agent 读取完整内容一次性处理
```

### 中文输入法快速提交

```
输入法提交 “消息”
  ↓
逐个读取 UTF-8 rune
  ↓
insertText()
  ↓
立即 redrawLine()
  ↓
输入框马上显示完整 “消息”
```

## 验证

| 项目 | 结果 |
|------|------|
| `go test ./...` | ✅ 通过 |
| `CGO_ENABLED=0 go build -ldflags='-s -w'` | ✅ 通过 |
| Linux amd64 静态构建 | ✅ 通过 |
| Linux arm64 静态构建 | ✅ 通过 |
| macOS arm64 构建 | ✅ 通过 |
| 长文本 / 多行粘贴真实 TTY 测试 | ✅ 已确认正常 |
| 中文输入法“这是一条测试消息”真实 TTY 测试 | ✅ 已确认正常 |
