package main

import (
	"fmt"
	"io"
	"strings"
)

// ─── Context Bar ───────────────────────────────────────────────────
//
// 每次 LLM 响应后显示一行 context 用量条，类似 Hermes 的底部状态栏。
// 格式: [████████░░] 78%  99.8K / 128.0K tokens | 轮次:3 消息:12
//
// 数据来源: API 返回的 usage.prompt_tokens（上次调用的实际上下文用量）。
// 未调用过 API 时用字符数估算（中文约 1.5 字/token，英文约 4 字/token）。

const barWidth = 10 // 进度条宽度（字符数）

// formatTokens 将 token 数格式化为人类可读字符串
func formatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// estimateContextTokens 估算当前消息列表的 token 数（粗糙：字符数/3.5）
func estimateContextTokens(messages []Message) int {
	total := 0
	for _, msg := range messages {
		// 角色名
		total += len(msg.Role)
		// 内容
		total += len(msg.Content)
		// tool_calls 的 function name + arguments
		for _, tc := range msg.ToolCalls {
			total += len(tc.Func.Name) + len(tc.Func.Arguments)
		}
	}
	// 中英混合约 3.5 字符/token
	return int(float64(total) / 3.5)
}

// showContextBar 显示 context 用量条。在每次 LLM 回复后调用。
func (a *Agent) showContextBar() {
	window := a.config.Model.ContextWindow
	if window <= 0 {
		window = 131072 // 默认 128K
	}

	// 优先用 API 返回的真实值
	used := a.llm.LastPromptTokens
	if used <= 0 {
		// 未调用过 API，用估算值
		used = estimateContextTokens(a.messages)
	}

	pct := float64(used) / float64(window) * 100
	if pct > 100 {
		pct = 100
	}

	// 构建进度条
	filled := int(pct / 100 * barWidth)
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	bar := strings.Repeat("#", filled) + strings.Repeat("-", empty)
	estimated := ""
	if a.llm.LastUsageEstimated || a.llm.LastPromptTokens <= 0 {
		estimated = " estimated"
	}
	a.ui.writeWithInputPaused(a.ui.out, func(w io.Writer) {
		fmt.Fprintf(w, "\nCONTEXT  [%s] %4.0f%%  %s / %s tokens%s | mode:%s role:%s requests:%d steps:%d messages:%d\n",
			bar,
			pct,
			formatTokens(used),
			formatTokens(window),
			estimated,
			a.registry.Mode(),
			a.roleName,
			a.sessionRequests,
			a.totalSteps,
			len(a.messages),
		)
	})
}
