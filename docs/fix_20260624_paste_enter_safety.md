# fix_20260624_paste_enter_safety.md

> **日期**: 2026-06-24  
> **类型**: 热修复（安全）  
> **影响**: TUI 输入安全 — 粘贴多行文本不再自动分条发送

---

## 触发条件

用户在 TUI 输入框中粘贴多行文本时，内容被逐行拆分为多条独立消息自动发送，Agent 立即逐条读取并执行。这在粘贴包含指令或代码的多行内容时存在安全风险。

## 根因

`input.go` 的 `readLineRaw()` 函数在 raw terminal mode 下逐字节读取 stdin。当用户粘贴多行文本时，终端模拟器将每行的换行符发送为 `\r`（CR, byte 13）。

`readLineRaw()` 中 `case b == 13` 分支直接视为 "用户敲了 Enter"，立即提交当前缓冲区内容并返回。粘贴流中的每个 `\r` 触发一次提交，导致多行内容被拆成多条独立消息：

```
粘贴 "line1\nline2\nline3"
终端流:  l i n e 1 \r l i n e 2 \r l i n e 3 \r
                                   ↑
                            byte=13 → 立即提交 "line1"
                                   → "line2" 提交
                                   → "line3" 提交
```

## 修复方案

在 `readLineRaw()` 中增加**基于字节到达间隔的粘贴检测**：

- 记录每个字节到达的时间戳
- 当 `\r`（byte 13）到达时，计算与上一字节的时间间隔 `gap`
- 若 `gap < 50ms`：判定为粘贴流中的换行 → 将 `\n` 插入缓冲区，**不提交**
- 若 `gap >= 50ms`：判定为人工敲击 Enter → 正常提交流程

### 50ms 阈值原理

- 粘贴时字节以密集突发方式到达（相邻字节间隔 < 1ms）
- 人类最快击键间隔约 100-150ms
- 50ms 阈值留有 >2x 安全余量，不会误判人工 Enter

### 改动文件

| 文件 | 改动 |
|------|------|
| `input.go` | 新增 `"time"` import；`readLineRaw()` 增加 `lastByteTime` 时间戳和粘贴判定逻辑（+17 行） |

无其他文件改动。

### 数据流（修复后）

```
粘贴 "line1\nline2\nline3"
终端流:  l i n e 1 \r l i n e 2 \r l i n e 3 \r
                    ↑               ↑               ↑
               gap<50ms       gap<50ms       gap<50ms
               → 插入 \n      → 插入 \n      → 插入 \n
缓冲区: "line1\nline2\nline3\n"（完整保留）
用户手动敲 Enter → gap >= 50ms → 提交
含 \n → 写 temp file → 返回 "FILE:/path"
Agent 读取文件完整内容一次性处理
```

## 验证

| 项目 | 结果 |
|------|------|
| `go vet` | ✅ 无警告 |
| `go build` | ✅ 通过，产物 6.8M |
| pipe 模式 fallback | ✅ 不受影响（走 `readTerminalLineFallback`） |
| 人工 Enter | ✅ gap >= 50ms，正常提交 |
| 粘贴多行（TTY） | ⏳ 需在真实终端中粘贴多行文本验证 |
