# fix_20260624_readline_investigation.md

> **日期**: 2026-06-24 → 更新 2026-06-25
> **类型**: 输入框架调研
> **分支**: `readline-investigation`（封存）

---

## 背景

ELIZA Agent v0.8.0 的 TUI 输入框痛点：
1. Backspace 在某些终端发送 ^H(8) 而非 DEL(127) → **已修复**（`input.go:96`）
2. `redrawLine()` 的 `\x1b[0K` 不如 `\x1b[2K` 稳健 → **已修复**（`input.go:258`）
3. 粘贴多行文本的时序检测在部分终端不准确
4. 缺少 ↑↓ 历史记录

## 调研过程

### chzyer/readline（已否决）

| 问题 | 详情 |
|------|------|
| 粘贴 | **单行编辑器** — `\r` 必定触发行提交，无法像自写 `input.go` 将 `\n` 插入 buffer 等用户编辑 |
| 依赖 | `golang.org/x/sys`（间接已有） |
| 优势 | 历史记录、tab 补全 |

### Bubble Tea（已否决）

- SSE chunk 经 channel + goroutine + p.Send 乱码
- textarea value-receiver bug
- 投入产出比太低

---

## 最终推荐：`golang.org/x/term` Terminal.ReadLine()

| 维度 | `golang.org/x/term` |
|------|:--|
| **来源** | Go 官方扩展标准库，Go 团队维护 |
| **版本** | v0.44.0 |
| **依赖** | `golang.org/x/sys`（官方，已有） |
| **Bracketed Paste** | ✅ `SetBracketedPasteMode(true)` — 粘贴内容被 `\x1b[200~` / `\x1b[201~` 包裹，`\r`/`\n` 保留在 buffer 中，**按 Enter 才提交** |
| **CJK** | ✅ 内部 `[]rune` buffer + `utf8.DecodeRune` |
| **历史记录** | ✅ `History` 接口，↑↓ 翻历史 |
| **动态 prompt** | ✅ `SetPrompt()` |
| **Tab 补全** | ✅ `AutoCompleteCallback` |
| **跨平台** | ✅ Linux / macOS / Windows |
| **单行/多行** | ✅ 可编辑含 `\n` 的行 |

### 核心 API

```go
import "golang.org/x/term"

t := term.NewTerminal(os.Stdin, "USER> ")
t.SetBracketedPasteMode(true)  // 开启粘贴保护

line, err := t.ReadLine()       // 阻塞读取，粘贴内容保留 \n
t.SetPrompt("USER [autopilot]> ") // 动态 prompt
```

### 集成影响

```
修改 agent.go RunInteractive():
  替换 readTerminalLine() → term.NewTerminal(os.Stdin, prompt).ReadLine()
  保留 prompt 动态切换
  保留 FILE: 超长文本逻辑

可删除文件：
  input.go          (303行)
  terminal_unix.go  (59行)
  terminal_windows.go

go.mod 新增：
  golang.org/x/term v0.44.0
  golang.org/x/sys  v0.46.0 (transitive, 可能已有)
```

### 与自写 input.go 对比

| 能力 | 自写 input.go | x/term ReadLine |
|------|:--:|:--:|
| CJK 输入/删除 | ✅ | ✅ |
| Backspace (DEL/^H) | ✅ | ✅ |
| 粘贴多行不提交 | ⚠️ 时序检测 | ✅ bracketed paste |
| 空格键 | ✅ | ✅ |
| 斜杠命令透传 | ✅ | ✅ |
| ↑↓ 历史记录 | ❌ | ✅ |
| Tab 补全 | ❌ | ✅ |
| 代码量 | 303行自维护 | 0行（标准库维护） |

### Hermes Agent 参考

Hermes（Nous Research 开源 agent）使用 Python `prompt_toolkit` — 同等级别的终端输入库。`golang.org/x/term` 是 Go 生态中最接近 `prompt_toolkit` 的标准方案。

---

## 待执行（新对话）

1. 在 main 分支集成 `golang.org/x/term`
2. 替换 `RunInteractive()` 中的 `readTerminalLine()`
3. 保留粘贴多行 → temp file (FILE:) 逻辑
4. 测试：CJK 输入、空格、斜杠命令、粘贴多行、↑↓ 历史
