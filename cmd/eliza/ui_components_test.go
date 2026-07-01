package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUIComponentsRenderWithinNarrowWidths(t *testing.T) {
	ctx := UIComponentContext{Width: 36, Plain: false, Unicode: true}
	components := [][]string{
		(MessageComponent{Kind: MessageUser, Text: "这是一条很长的用户消息，用来检查窄终端排版不会乱"}).Lines(ctx),
		(MessageComponent{Kind: MessageAgent, Text: "我正在思考下一步，同时保持每一行都不会触发终端自动换行。"}).Lines(ctx),
		(StatusComponent{Level: "RUNNING", Message: "思考中，正在等待模型返回并保持输入框稳定"}).Lines(ctx),
		(ToolCallComponent{Name: "run_command", Status: "COMPLETED", DurationMS: 1234, Exit: "0", Truncated: true}).Lines(ctx),
		ApprovalComponent{Prompt: "Dangerous command: rm -rf /tmp/example-with-a-very-long-name", Selected: 0}.Lines(32),
	}

	for _, lines := range components {
		for _, line := range lines {
			if displayWidth(stripANSI(line)) > 36 {
				t.Fatalf("component line overflowed width=36 actual=%d line=%q", displayWidth(stripANSI(line)), line)
			}
		}
	}
}

func TestInputBarComponentKeepsPromptAndRunningLabelsStable(t *testing.T) {
	ctx := UIComponentContext{Width: 72, Plain: false, Unicode: true}
	promptHeader, promptPrefix := (InputBarComponent{Kind: InputBarPrompt, Mode: ModeReadonly, Role: "default", Hint: "/help  /status"}).Labels(ctx)
	runningHeader, runningPrefix := (InputBarComponent{Kind: InputBarRunning, Mode: ModeAutopilot, Role: "ops", Hint: "Guide, Enter; /cancel or Ctrl-C"}).Labels(ctx)

	for _, want := range []string{"╭─ INPUT", "USER [readonly/default]", "╰─ "} {
		if !strings.Contains(promptHeader+promptPrefix, want) {
			t.Fatalf("prompt label missing %q: header=%q prefix=%q", want, promptHeader, promptPrefix)
		}
	}
	for _, want := range []string{"╭─ GUIDE", "RUNNING [autopilot/ops]", "/cancel", "╰─ "} {
		if !strings.Contains(runningHeader+runningPrefix, want) {
			t.Fatalf("running label missing %q: header=%q prefix=%q", want, runningHeader, runningPrefix)
		}
	}
}

func TestAgentUIEventsRenderThroughLegacySink(t *testing.T) {
	var output bytes.Buffer
	ui := &AgentUI{renderer: &Renderer{out: &output, err: &output, color: false, unicode: true, width: 56}}

	ui.Emit(UserMessageUIEvent{Text: "你好，ELIZA"})
	ui.Emit(StatusUIEvent{Level: "RUNNING", Message: "思考中"})
	ui.Emit(AssistantStartedUIEvent{})
	ui.Emit(AssistantDeltaUIEvent{Text: "收到，我会只检查 TUI。"})
	ui.Emit(AssistantDoneUIEvent{})
	ui.Emit(ToolCallUIEvent{Name: "go_test", Status: "COMPLETED", DurationMS: 42})

	text := output.String()
	for _, want := range []string{"│ ● 你好", "RUNNING  思考中", "│ ● 收到", "TOOL     name=go_test"} {
		if !strings.Contains(text, want) {
			t.Fatalf("legacy sink output missing %q: %q", want, text)
		}
	}
}

func TestRendererInputDeleteRedrawClearsLongerPreviousText(t *testing.T) {
	var output bytes.Buffer
	renderer := &Renderer{out: &output, err: &output, color: false, unicode: true, width: 48}

	renderer.Prompt(ModeReadonly, "default")
	renderer.updateInput([]rune("abcdef中文"), len([]rune("abcdef中文")))
	renderer.updateInput([]rune("abc"), len([]rune("abc")))

	text := output.String()
	if strings.Count(text, "\x1b[0J") < 2 {
		t.Fatalf("expected input redraw to clear previous longer text, got %q", text)
	}
	if !strings.Contains(text, "╰─ abc") {
		t.Fatalf("redraw did not show shortened input: %q", text)
	}
}

func stripANSI(text string) string {
	var builder strings.Builder
	for index := 0; index < len(text); index++ {
		if text[index] != 0x1b {
			builder.WriteByte(text[index])
			continue
		}
		index++
		for index < len(text) && (text[index] < 0x40 || text[index] > 0x7e) {
			index++
		}
	}
	return builder.String()
}
