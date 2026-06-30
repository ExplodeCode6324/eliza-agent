# fix: chromedp context.WithTimeout cancel 导致 browserCtx 被取消

**日期**: 2026-06-30
**提交**: `b20db5b`
**影响文件**: `cmd/eliza/browser.go`

---

## 现象

浏览器工具注册成功（`PASS 浏览器工具已启用`），但 `browser_open` 执行失败：

```
TOOL     name=browser_open status=FAILED duration=119ms
err=context canceled
```

`about:blank` 初始化导航成功，Chrome 进程正常运行。但后续 chromedp.Run
调用 `Navigate(target)` 时，`browserCtx.Err()` 返回 `context canceled`。

## 根因

`ensureLocked()` 和 `run()` 方法均使用 `context.WithTimeout(b.browserCtx, ...)`
派生子 context，配合 `defer cancel()`：

```go
// ensureLocked (原代码)
startCtx, cancel := context.WithTimeout(b.browserCtx, b.timeout)
defer cancel()
chromedp.Run(startCtx, chromedp.Navigate("about:blank"))
// cancel() 在函数返回时触发 → 关闭 chromedp 内部 tab
// → 浏览器连接状态异常 → 后续操作失败

// run (原代码)
ctx, cancel := context.WithTimeout(b.browserCtx, b.timeout)
defer cancel()
chromedp.Run(ctx, actions...)
// 同样会在操作完成后 cancel → browserCtx.Err() = context canceled
```

chromedp v0.14.2 中，取消派生 context 不仅关闭当前 tab，
还会触发浏览器连接的内部清理，导致 `browserCtx` 被连带取消。

最小测试证明：同一 Chrome 二进制 + 同一 chromedp 版本，
只要不 cancel 派生 context，一切正常。

## 修复

改用 `b.browserCtx` 直接传给 `chromedp.Run()`，不派生子 context：

```go
// ensureLocked (修复后)
if err := chromedp.Run(b.browserCtx, chromedp.Navigate("about:blank")); err != nil {
    b.resetLocked()
    return fmt.Errorf("启动无头浏览器失败: %w", err)
}

// run (修复后)
err := chromedp.Run(b.browserCtx, actions...)
```

`browserCtx` 仅在 `Reset()` 或进程退出时取消，正常操作不再触发连接关闭。

## 权衡

移除 `context.WithTimeout` 意味着单个 chromedp 操作不再有 Go 层的超时保护。
但 Chrome 本身有网络超时（默认 30s），且 chromedp 操作通常是秒级完成的
页面加载，不会无限阻塞。如需严格的超时控制，可用 goroutine + channel 替代
context 超时。

## 验证

```bash
ELIZA_MODE=autopilot ./eliza -q "browser_open https://www.example.com 告诉我标题"
```

结果：

| 步骤 | 状态 | 耗时 |
|------|------|------|
| `browser_open` → example.com | COMPLETED | 1439ms |
| `browser_snapshot` | COMPLETED | 5ms |
| `browser_reset` | COMPLETED | 50ms |

三步全部正常，页面标题 "Example Domain" 正确返回。
