package main

import (
	"fmt"
	"io"
)

type UIEvent interface {
	uiEvent()
}

type UICommand interface {
	uiCommand()
}

type UISink interface {
	Emit(UIEvent)
}

type UIStream string

const (
	UIStreamStdout UIStream = "stdout"
	UIStreamStderr UIStream = "stderr"
)

type StatusUIEvent struct {
	Level   string
	Message string
}

func (StatusUIEvent) uiEvent() {}

type UserMessageUIEvent struct {
	RequestID string
	Text      string
}

func (UserMessageUIEvent) uiEvent() {}

type AssistantStartedUIEvent struct {
	RequestID string
}

func (AssistantStartedUIEvent) uiEvent() {}

type AssistantDeltaUIEvent struct {
	RequestID string
	Text      string
}

func (AssistantDeltaUIEvent) uiEvent() {}

type AssistantDoneUIEvent struct {
	RequestID string
}

func (AssistantDoneUIEvent) uiEvent() {}

type ToolCallUIEvent struct {
	RequestID  string
	ToolCallID string
	Name       string
	Status     string
	DurationMS int64
	Exit       string
	Truncated  bool
}

func (ToolCallUIEvent) uiEvent() {}

type RawTextUIEvent struct {
	Stream UIStream
	Text   string
}

func (RawTextUIEvent) uiEvent() {}

type TitleUIEvent struct {
	Text string
}

func (TitleUIEvent) uiEvent() {}

type ApprovalRequestedUIEvent struct {
	RequestID  string
	ToolCallID string
	Prompt     string
	Risk       string
	Options    []string
}

func (ApprovalRequestedUIEvent) uiEvent() {}

type UserSubmittedUICommand struct {
	Text string
}

func (UserSubmittedUICommand) uiCommand() {}

type ApprovalSelectedUICommand struct {
	ToolCallID string
	Decision   string
	Guidance   string
}

func (ApprovalSelectedUICommand) uiCommand() {}

type CancelRequestedUICommand struct {
	RequestID string
}

func (CancelRequestedUICommand) uiCommand() {}

type AgentUI struct {
	renderer *Renderer
}

func NewAgentUI(cfg UIConfig) *AgentUI {
	return &AgentUI{renderer: NewRenderer(cfg)}
}

func (u *AgentUI) Emit(event UIEvent) {
	if u == nil || u.renderer == nil || event == nil {
		return
	}
	switch e := event.(type) {
	case StatusUIEvent:
		u.renderer.Status(e.Level, "%s", e.Message)
	case UserMessageUIEvent:
		u.renderer.UserMessage(e.Text)
	case AssistantStartedUIEvent:
		u.renderer.BeginAssistant()
	case AssistantDeltaUIEvent:
		u.renderer.AssistantChunk(e.Text)
	case AssistantDoneUIEvent:
		u.renderer.EndAssistant()
	case ToolCallUIEvent:
		u.renderer.Tool(e.Name, e.Status, e.DurationMS, e.Exit, e.Truncated)
	case RawTextUIEvent:
		if e.Stream == UIStreamStderr {
			u.renderer.PrintErr(e.Text)
		} else {
			u.renderer.Print(e.Text)
		}
	case TitleUIEvent:
		u.renderer.Title(e.Text)
	case ApprovalRequestedUIEvent:
		// Interactive approval is handled by RequestApproval so the caller can
		// receive a command/result. The event remains available for future sinks.
	default:
	}
}

func (u *AgentUI) Banner(cfg *Config, registry *ToolRegistry, worklogPath string, skills int) {
	u.renderer.Banner(cfg, registry, worklogPath, skills)
}

func (u *AgentUI) Status(level, format string, args ...any) {
	u.Emit(StatusUIEvent{Level: level, Message: fmt.Sprintf(format, args...)})
}

func (u *AgentUI) UserMessage(message string) {
	u.Emit(UserMessageUIEvent{Text: message})
}

func (u *AgentUI) BeginAssistant() {
	u.Emit(AssistantStartedUIEvent{})
}

func (u *AgentUI) AssistantChunk(chunk string) {
	u.Emit(AssistantDeltaUIEvent{Text: chunk})
}

func (u *AgentUI) EndAssistant() {
	u.Emit(AssistantDoneUIEvent{})
}

func (u *AgentUI) Tool(name, status string, durationMS int64, exit string, truncated bool) {
	u.Emit(ToolCallUIEvent{Name: name, Status: status, DurationMS: durationMS, Exit: exit, Truncated: truncated})
}

func (u *AgentUI) Title(text string) {
	u.Emit(TitleUIEvent{Text: text})
}

func (u *AgentUI) Print(text string) {
	u.Emit(RawTextUIEvent{Stream: UIStreamStdout, Text: text})
}

func (u *AgentUI) Printf(format string, args ...any) {
	u.Print(fmt.Sprintf(format, args...))
}

func (u *AgentUI) PrintErr(text string) {
	u.Emit(RawTextUIEvent{Stream: UIStreamStderr, Text: text})
}

func (u *AgentUI) PrintErrf(format string, args ...any) {
	u.PrintErr(fmt.Sprintf(format, args...))
}

func (u *AgentUI) Output(write func(io.Writer)) {
	u.renderer.Output(write)
}

func (u *AgentUI) OutputErr(write func(io.Writer)) {
	u.renderer.OutputErr(write)
}

func (u *AgentUI) ReadPromptLine(mode, role string) (string, error) {
	return u.renderer.ReadPromptLine(mode, role)
}

func (u *AgentUI) StartRunningInput(mode, role string) {
	u.renderer.StartRunningInput(mode, role)
}

func (u *AgentUI) StopRunningInput() {
	u.renderer.StopRunningInput()
}

func (u *AgentUI) PollRunningInput() ([]string, bool, error) {
	return u.renderer.PollRunningInput()
}

func (u *AgentUI) SuspendInputOverlay() func() {
	return u.renderer.SuspendInputOverlay()
}

func (u *AgentUI) Confirm(prompt string) bool {
	return u.renderer.Confirm(prompt)
}

func (u *AgentUI) ApprovalBox(prompt string, selected int) int {
	return u.renderer.ApprovalBox(prompt, selected)
}

func (u *AgentUI) RequestApproval(prompt string) ApprovalResult {
	selected, err := readApprovalChoice(func(selected int) int {
		return u.ApprovalBox(prompt, selected)
	}, len(approvalOptions))
	if err != nil {
		u.Status("WARN", "Approval input unavailable; denied: %s", prompt)
		return approvalDenied()
	}
	return approvalResultFromSelection(u.renderer, selected)
}
