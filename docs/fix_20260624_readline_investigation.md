# fix_20260624_readline_investigation.md

> **日期**: 2026-06-24
> **类型**: 调研
> **分支**: `readline-investigation`

---

## 背景

ELIZA Agent v0.8.0 的 TUI 输入框存在以下问题：
1. 中文输入/删除偶尔异常（backspace 在某些终端发送 ^H 而非 DEL）
2. `redrawLine()` 使用 `\x1b[0K`（清除到行尾），在某些情况下不如 `\x1b[2K`（清除整行）稳健
3. 需要更完善的输入体验

## 已完成修复（在 main 分支）

| 文件 | 修复 |
|------|------|
| `input.go:96` | Backspace 同时匹配 `byte 127`(DEL) 和 `byte 8`(^H) |
| `input.go:258` | `\x1b[0K` → `\x1b[2K`（清除整行） |

## 调研：chzyer/readline

**库**: `github.com/chzyer/readline` v1.5.1
- Stars: ~2K，MIT 协议
- 用户: CockroachDB, Netflix, Docker
- Pure Go，无 CGO

### 结论：不适用

**根因**: readline 是**单行编辑器**。多行粘贴时 `\r` 字符被 readline 解释为 Enter（行提交），无法像我们自写的 `input.go` 那样将 `\n` 插入 buffer 供用户编辑后统一提交。

| 需求 | readline | 自写 input.go |
|------|:--:|:--:|
| CJK 输入/删除 | ✅ | ✅ |
| 粘贴多行不自动提交 | ❌ | ✅ (50ms 时序检测) |
| 空格键 | ✅ | ✅ |
| 斜杠命令 | ✅ (透传) | ✅ |
| 历史记录 | ✅ | ❌ |

### Bubble Tea 调研（bubbletea-wip-archived）

之前已封存，主要问题：
- 流式管道复杂导致 SSE chunk 乱码
- textarea value-receiver bug
- 空格的 tea.KeySpace 类型未识别
- 整体投入产出比低

## 当前状态

- `main`: 原生 `input.go`（含 ^H + \x1b[2K 修复），零依赖，输入框功能完整
- `pure-stdlib`: 同样修复，零依赖参考实现
- `readline-investigation`: 本调研的封存分支
- `bubbletea-wip-archived`: 已删除

## 待解决问题

1. **粘贴多行**：当前 `input.go` 的粘贴检测基于字节到达间隔（50ms 阈值）。在某些终端/网络环境下，粘贴的字节流可能不够密集，导致阈值误判——`\r` 被当作 Enter 而非插入 `\n`。需要一种不依赖时序的方案。

2. **历史记录**：当前无 ↑↓ 历史记录功能。readline 有此功能但粘贴不支持。

3. **候选方向**：
   - `peterh/liner`（单行编辑器，接口极简）
   - `ergochat/readline`（chzyer/readline 的分支改进版）
   - 继续优化自写 `input.go`（加大时序窗口、bracketed paste mode 检测）
   - 考虑多行编辑器库（如 `bubbles/textarea` 从 Bubble Tea 生态中单独提取）
