package main

import (
	"fmt"
	"strings"
)

type UIComponentContext struct {
	Width   int
	Plain   bool
	Unicode bool
	Style   func(text, color string) string
}

func (ctx UIComponentContext) normalizedWidth() int {
	return normalizedTerminalWidth(ctx.Width)
}

func (ctx UIComponentContext) safeWidth() int {
	return redrawSafeWidth(ctx.normalizedWidth())
}

func (ctx UIComponentContext) style(text, color string) string {
	if ctx.Style == nil {
		return text
	}
	return ctx.Style(text, color)
}

type MessageKind string

const (
	MessageUser  MessageKind = "user"
	MessageAgent MessageKind = "agent"
)

type MessageComponent struct {
	Kind MessageKind
	Text string
}

func (c MessageComponent) Lines(ctx UIComponentContext) []string {
	text := strings.TrimRight(c.Text, "\r\n")
	if ctx.Plain || !ctx.Unicode {
		return c.plainLines(ctx, text)
	}
	markerColor := ansiDeepRed
	textColor := ansiPink
	if c.Kind == MessageUser {
		markerColor = ansiWhite
		textColor = ansiWhite
	}
	prefixWidth := displayWidth("│ ● ")
	contentWidth := ctx.safeWidth() - prefixWidth
	if contentWidth < 8 {
		contentWidth = 8
	}
	lines := []string{ctx.style("╭──", ansiDeepRed)}
	for _, line := range wrapComponentText(text, contentWidth) {
		lines = append(lines, ctx.style("│ ", ansiDeepRed)+ctx.style("●", markerColor)+" "+ctx.style(line, textColor))
	}
	lines = append(lines, ctx.style("╰──", ansiDeepRed))
	if c.Kind == MessageUser {
		lines = append(lines, "")
	}
	return lines
}

func (c MessageComponent) plainLines(ctx UIComponentContext, text string) []string {
	prefix := "● "
	if c.Kind == MessageUser {
		prefix = "USER: "
	}
	width := ctx.safeWidth() - displayWidth(prefix)
	if width < 8 {
		width = 8
	}
	wrapped := wrapComponentText(text, width)
	lines := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			lines = append(lines, prefix+line)
			continue
		}
		lines = append(lines, strings.Repeat(" ", displayWidth(prefix))+line)
	}
	return lines
}

type StatusComponent struct {
	Level   string
	Message string
}

func (c StatusComponent) Lines(ctx UIComponentContext) []string {
	level := strings.ToUpper(strings.TrimSpace(c.Level))
	if level == "" {
		level = "INFO"
	}
	message := strings.TrimRight(c.Message, "\r\n")
	prefix := fmt.Sprintf("%-8s ", level)
	width := ctx.safeWidth() - displayWidth(prefix)
	if width < 8 {
		width = 8
	}
	wrapped := wrapComponentText(message, width)
	lines := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			lines = append(lines, prefix+line)
			continue
		}
		lines = append(lines, strings.Repeat(" ", displayWidth(prefix))+line)
	}
	return lines
}

type ToolCallComponent struct {
	Name       string
	Status     string
	DurationMS int64
	Exit       string
	Truncated  bool
}

func (c ToolCallComponent) Lines(ctx UIComponentContext) []string {
	line := fmt.Sprintf("TOOL     name=%s status=%s duration=%dms", c.Name, c.Status, c.DurationMS)
	if c.Exit != "" {
		line += " exit=" + c.Exit
	}
	if c.Truncated {
		line += " truncated=true"
	}
	width := ctx.safeWidth()
	wrapped := wrapDisplay(line, width)
	if len(wrapped) == 0 {
		return []string{line}
	}
	return wrapped
}

type InputBarKind string

const (
	InputBarPrompt  InputBarKind = "prompt"
	InputBarRunning InputBarKind = "running"
)

type InputBarComponent struct {
	Kind InputBarKind
	Mode string
	Role string
	Hint string
}

func (c InputBarComponent) Labels(ctx UIComponentContext) (string, string) {
	kind := "INPUT"
	if c.Kind == InputBarRunning {
		kind = "GUIDE"
	}
	label := fmt.Sprintf("%s  USER [%s/%s]", kind, c.Mode, c.Role)
	if c.Kind == InputBarRunning {
		label = fmt.Sprintf("%s  RUNNING [%s/%s]", kind, c.Mode, c.Role)
	}
	if c.Hint != "" {
		label += "  " + c.Hint
	}
	if ctx.Plain || !ctx.Unicode {
		return label, "> "
	}
	return "╭─ " + label, "╰─ "
}

type ApprovalComponent struct {
	Prompt   string
	Selected int
	Options  []string
}

func (c ApprovalComponent) Lines(width int) []string {
	if width < 1 {
		width = 80
	}
	if len(c.Options) == 0 {
		c.Options = approvalOptions
	}
	lines := []string{"Approval request", strings.Repeat("─", width)}
	for _, raw := range strings.Split(c.Prompt, "\n") {
		raw = strings.TrimRight(raw, "\r")
		for _, wrapped := range wrapDisplay(raw, width) {
			lines = append(lines, wrapped)
		}
	}
	lines = append(lines, strings.Repeat("─", width))
	for index, option := range c.Options {
		marker := "  "
		if index == c.Selected {
			marker = "> "
		}
		optionWidth := width - displayWidth(marker)
		if optionWidth < 1 {
			optionWidth = 1
		}
		lines = append(lines, marker+truncateDisplay(option, optionWidth))
	}
	lines = append(lines, strings.Repeat("─", width))
	lines = append(lines, "↑/↓ select, Enter confirm")
	return lines
}

func wrapComponentText(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	parts := strings.Split(text, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimRight(part, "\r")
		wrapped := wrapDisplay(part, width)
		if len(wrapped) == 0 {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapped...)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
