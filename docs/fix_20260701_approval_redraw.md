# fix: stabilize approval prompt redraw

**日期**: 2026-07-01
**提交**: `2bd6f60`
**前置功能**: `d3496bb` feat: add selectable approval prompt
**影响文件**: `cmd/eliza/input.go`, `cmd/eliza/ui.go`

---

## 现象

可选择式审批框（↑/↓ 选择 + Enter 确认）在用户反复上下切换选项时，
出现重绘错位、残留字符、边框断裂等视觉异常。

## 根因

两处问题叠加：

**1. 光标位置不确定 (`input.go`)**

重绘时使用 `\x1b[%dA\x1b[0J` 上移并清屏，但未保证光标在行首。
流式输出后光标可能不在第 0 列，导致 ANSI 逃逸序列上移行数计算偏差。

```go
// 修复前
fmt.Fprintf(os.Stderr, "\x1b[%dA\x1b[0J", renderedLines)

// 修复后：先 \r 回到行首
fmt.Fprintf(os.Stderr, "\r\x1b[%dA\x1b[0J", renderedLines)
```

**2. 逐行写入导致部分重绘 (`ui.go`)**

审批框渲染使用多次 `fmt.Fprintln` 逐行输出，每次 write 调用之间
存在时序窗口。如果终端在两次 write 之间刷新，用户会看到半成品：

```go
// 修复前：逐行输出，可能看到边框闪烁/断裂
fmt.Fprintln(r.err, "╭────╮")
for _, line := range lines {
    r.approvalStyledLine(line, inner)  // 内部 fmt.Fprintln
}
fmt.Fprintln(r.err, "╰────╯")
```

## 修复

两处改动均为原子性保证：

**`ui.go`**: 用 `strings.Builder` 收集全部输出行，一次 `fmt.Fprint` 写入：

```go
var builder strings.Builder
builder.WriteString(......)
builder.WriteString("\r\n")
for _, line := range lines {
    builder.WriteString(r.approvalStyledLine(line, inner))
    builder.WriteString("\r\n")
}
builder.WriteString(......)
fmt.Fprint(r.err, builder.String())  // 一次性写入
```

`approvalPlainLine` 和 `approvalStyledLine` 改为返回 string，不直接输出。

**`input.go`**: 重绘前加 `\r` 确保光标在行首。

## 验证

审批框在 plain 和 unicode 两种模式下，反复 ↑/↓ 切换 10+ 次后
边框完整、选项对齐、无残留字符。
