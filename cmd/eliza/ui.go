package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/term"
)

const (
	ansiReset        = "\x1b[0m"
	ansiDeepRed      = "\x1b[38;5;88m"
	ansiSoftRed      = "\x1b[38;5;217m"
	ansiPink         = "\x1b[38;5;224m"
	ansiHair         = "\x1b[38;5;211m"
	ansiHairDim      = "\x1b[38;5;197m"
	ansiSkin         = "\x1b[38;5;223m"
	ansiEye          = "\x1b[38;5;235m"
	ansiUniform      = "\x1b[38;5;160m"
	ansiLaptop       = "\x1b[38;5;230m"
	ansiLaptopShadow = "\x1b[38;5;180m"
	ansiWhite        = "\x1b[38;5;15m"
	ansiDim          = "\x1b[2m"
)

type UIConfig struct {
	Plain   bool
	NoColor bool
}

const (
	bannerTitle  = "ELIZA-AGENT (DRC Bank ver.)"
	bannerCredit = "Powered By MUY & ELIZA"
)

type Renderer struct {
	out     io.Writer
	err     io.Writer
	plain   bool
	color   bool
	unicode bool
	width   int

	streamBuf strings.Builder
	streaming bool

	mu                   sync.Mutex
	input                rendererInputOverlay
	runningInputTerminal bool
}

type rendererInputOverlay struct {
	active     bool
	header     string
	prefix     string
	buf        []rune
	pos        int
	lines      int
	cursorLine int
}

func NewRenderer(cfg UIConfig) *Renderer {
	stdoutTTY := isTerminalFile(os.Stdout)
	term := strings.ToLower(os.Getenv("TERM"))
	plain := cfg.Plain || !stdoutTTY || term == "dumb"
	color := !plain && !cfg.NoColor && os.Getenv("NO_COLOR") == "" && runtime.GOOS != "windows"
	lang := strings.ToUpper(os.Getenv("LC_ALL") + os.Getenv("LC_CTYPE") + os.Getenv("LANG"))
	unicodeOK := !plain && (strings.Contains(lang, "UTF-8") || strings.Contains(lang, "UTF8"))
	return &Renderer{
		out: os.Stdout, err: os.Stderr,
		plain: plain, color: color, unicode: unicodeOK,
		width: terminalWidth(),
	}
}

func isTerminalFile(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalWidth() int {
	if width := detectedTerminalWidth(); width >= 20 {
		return width
	}
	return 80
}

func detectedTerminalWidth() int {
	for _, file := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		if file == nil {
			continue
		}
		fd := int(file.Fd())
		if !term.IsTerminal(fd) {
			continue
		}
		width, _, err := term.GetSize(fd)
		if err == nil && width >= 20 {
			return width
		}
	}
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && value >= 20 {
		return value
	}
	return 0
}

func (r *Renderer) style(text, color string) string {
	if !r.color {
		return text
	}
	return color + text + ansiReset
}

type crlfWriter struct {
	dst  io.Writer
	prev byte
}

func (w *crlfWriter) Write(p []byte) (int, error) {
	var converted []byte
	for _, b := range p {
		if b == '\n' && w.prev != '\r' {
			converted = append(converted, '\r')
		}
		converted = append(converted, b)
		w.prev = b
	}
	if len(converted) == 0 {
		return 0, nil
	}
	_, err := w.dst.Write(converted)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func rendererOutputWriter(w io.Writer) io.Writer {
	if terminalRawActive() {
		return &crlfWriter{dst: w}
	}
	return w
}

func (r *Renderer) writeWithInputPaused(w io.Writer, write func(io.Writer)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	wasActive := r.input.active
	if wasActive {
		r.clearInputLocked()
	}
	write(rendererOutputWriter(w))
	if wasActive {
		r.renderInputLocked()
	}
}

func (r *Renderer) Output(write func(io.Writer)) {
	r.writeWithInputPaused(r.out, write)
}

func (r *Renderer) OutputErr(write func(io.Writer)) {
	r.writeWithInputPaused(r.err, write)
}

func (r *Renderer) Print(text string) {
	r.Output(func(w io.Writer) {
		fmt.Fprint(w, text)
	})
}

func (r *Renderer) Printf(format string, args ...any) {
	r.Output(func(w io.Writer) {
		fmt.Fprintf(w, format, args...)
	})
}

func (r *Renderer) PrintErr(text string) {
	r.OutputErr(func(w io.Writer) {
		fmt.Fprint(w, text)
	})
}

func (r *Renderer) PrintErrf(format string, args ...any) {
	r.OutputErr(func(w io.Writer) {
		fmt.Fprintf(w, format, args...)
	})
}

func (r *Renderer) startInput(header, prefix string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.input.active {
		r.clearInputLocked()
	}
	r.input = rendererInputOverlay{active: true, header: header, prefix: prefix}
	r.renderInputLocked()
}

func (r *Renderer) updateInput(buf []rune, pos int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.input.active {
		return
	}
	r.clearInputLocked()
	r.input.buf = append(r.input.buf[:0], buf...)
	r.input.pos = pos
	r.renderInputLocked()
}

func (r *Renderer) finishInput() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.input.active {
		return
	}
	r.clearInputLocked()
	r.input = rendererInputOverlay{}
}

func (r *Renderer) suspendInput() func() {
	r.mu.Lock()
	if !r.input.active {
		r.mu.Unlock()
		return func() {}
	}
	saved := r.input
	r.clearInputLocked()
	r.input.active = false
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.input.active {
			r.clearInputLocked()
		}
		r.input = saved
		r.input.active = true
		r.renderInputLocked()
	}
}

func (r *Renderer) clearInputLocked() {
	if r.input.lines <= 0 {
		return
	}
	fmt.Fprint(r.err, "\r")
	if r.input.cursorLine > 0 {
		fmt.Fprintf(r.err, "\x1b[%dA", r.input.cursorLine)
	}
	fmt.Fprint(r.err, "\x1b[0J")
	r.input.lines = 0
	r.input.cursorLine = 0
}

func (r *Renderer) renderInputLocked() {
	if !r.input.active {
		return
	}
	r.refreshWidthLocked()
	lines, cursorLine, cursorCol := r.inputLines()
	for index, line := range lines {
		if index > 0 {
			fmt.Fprint(r.err, "\r\n")
		}
		fmt.Fprint(r.err, r.style(line, ansiSoftRed))
	}
	r.input.lines = len(lines)
	r.input.cursorLine = cursorLine
	fmt.Fprint(r.err, "\r")
	linesUp := len(lines) - 1 - cursorLine
	if linesUp > 0 {
		fmt.Fprintf(r.err, "\x1b[%dA", linesUp)
	}
	if cursorCol > 0 {
		fmt.Fprintf(r.err, "\x1b[%dC", cursorCol)
	}
}

func (r *Renderer) inputLines() ([]string, int, int) {
	width := r.width
	if width < 20 {
		width = 80
	}
	headerLines := wrapDisplay(r.input.header, width)
	lines := append([]string(nil), headerLines...)
	inputLines, cursorLine, cursorCol := renderInputBufferLines(r.input.prefix, r.input.buf, r.input.pos, width)
	lines = append(lines, inputLines...)
	return lines, len(headerLines) + cursorLine, cursorCol
}

func (r *Renderer) refreshWidthLocked() {
	if width := detectedTerminalWidth(); width >= 20 {
		r.width = width
	}
	if r.width < 20 {
		r.width = 80
	}
}

// ─── Left-only box (no right border — avoids alignment issues) ─────

func (r *Renderer) drawBoxTop(w io.Writer) {
	if r.plain || !r.unicode {
		return
	}
	fmt.Fprintln(w, r.style("╭──", ansiDeepRed))
}

func (r *Renderer) drawBoxLine(w io.Writer, content string) {
	if r.plain || !r.unicode {
		fmt.Fprintln(w, content)
		return
	}
	fmt.Fprintln(w, r.style("│ ", ansiDeepRed)+content)
}

func (r *Renderer) drawBoxBottom(w io.Writer) {
	if r.plain || !r.unicode {
		return
	}
	fmt.Fprintln(w, r.style("╰──", ansiDeepRed))
}

func (r *Renderer) Title(text string) {
	r.writeWithInputPaused(r.out, func(w io.Writer) {
		fmt.Fprintln(w, r.style(text, ansiDeepRed))
	})
}

func (r *Renderer) Status(level, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%-8s %s", strings.ToUpper(level), message)
	if strings.EqualFold(level, "FAIL") || strings.EqualFold(level, "WARN") || strings.EqualFold(level, "BLOCKED") {
		r.writeWithInputPaused(r.err, func(w io.Writer) {
			fmt.Fprintln(w, r.style(line, ansiDeepRed))
		})
		return
	}
	r.writeWithInputPaused(r.out, func(w io.Writer) {
		fmt.Fprintln(w, r.style(line, ansiWhite))
	})
}

func (r *Renderer) Prompt(mode, role string) {
	header, prefix := r.inputLabels("INPUT", mode, role, "/help  /status")
	r.startInput(header, prefix)
}

func (r *Renderer) RunningInputBar(mode, role string) {
	header, prefix := r.inputLabels("GUIDE", mode, role, "Guide, Enter; /cancel or Ctrl-C")
	r.startInput(header, prefix)
}

func (r *Renderer) inputLabels(kind, mode, role, hint string) (string, string) {
	label := fmt.Sprintf("%s  USER [%s/%s]", kind, mode, role)
	if kind == "GUIDE" {
		label = fmt.Sprintf("%s  RUNNING [%s/%s]", kind, mode, role)
	}
	if hint != "" {
		label += "  " + hint
	}
	if r.plain || !r.unicode {
		return label, "> "
	}
	return "╭─ " + label, "╰─ "
}

func (r *Renderer) ReadPromptLine(mode, role string) (string, error) {
	terminalMu.Lock()
	if err := enterRawTerminal(); err != nil {
		terminalMu.Unlock()
		header, prefix := r.inputLabels("INPUT", mode, role, "/help  /status")
		r.writeWithInputPaused(r.out, func(w io.Writer) {
			fmt.Fprintf(w, "\n%s\n%s", header, prefix)
		})
		return readTerminalLineFallback()
	}
	enableBracketedPaste()
	terminalMu.Unlock()
	defer func() {
		terminalMu.Lock()
		disableBracketedPaste()
		exitRawTerminal()
		terminalMu.Unlock()
	}()

	r.Prompt(mode, role)
	defer r.finishInput()
	return readLineRawWith(func(buf []rune, pos int) {
		r.updateInput(buf, pos)
	}, submitInputBufferQuiet)
}

func (r *Renderer) StartRunningInput(mode, role string) {
	terminalMu.Lock()
	if err := enterRawTerminal(); err == nil {
		enableBracketedPaste()
		r.runningInputTerminal = true
	}
	terminalMu.Unlock()
	r.RunningInputBar(mode, role)
}

func (r *Renderer) StopRunningInput() {
	r.finishInput()
	terminalMu.Lock()
	if r.runningInputTerminal {
		disableBracketedPaste()
		exitRawTerminal()
		r.runningInputTerminal = false
	}
	terminalMu.Unlock()
}

func (r *Renderer) PollRunningInput() ([]string, bool, error) {
	terminalMu.Lock()
	chunk, err := readPendingTerminalBytes()
	terminalMu.Unlock()
	if err != nil || len(chunk) == 0 {
		return nil, false, err
	}
	r.mu.Lock()
	buf := append([]rune(nil), r.input.buf...)
	pos := r.input.pos
	r.mu.Unlock()
	buf, pos, lines, interrupted, err := feedPendingInputBytes(buf, pos, chunk, func(next []rune, nextPos int) {
		r.updateInput(next, nextPos)
	})
	r.mu.Lock()
	if r.input.active {
		r.input.buf = append(r.input.buf[:0], buf...)
		r.input.pos = pos
	}
	r.mu.Unlock()
	return lines, interrupted, err
}

func (r *Renderer) SuspendInputOverlay() func() {
	return r.suspendInput()
}

// ─── Assistant box (streaming → box at end) ────────────────────────

func (r *Renderer) BeginAssistant() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streaming = true
	r.streamBuf.Reset()
}

func (r *Renderer) AssistantChunk(chunk string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streamBuf.WriteString(chunk)
}

func (r *Renderer) EndAssistant() {
	r.mu.Lock()
	r.streaming = false
	content := strings.TrimSpace(r.streamBuf.String())
	r.streamBuf.Reset()
	r.mu.Unlock()
	if content == "" {
		return
	}
	r.writeWithInputPaused(r.out, func(w io.Writer) {
		r.drawBoxTop(w)
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimRight(line, "\r")
			r.drawBoxLine(w, r.style("●", ansiDeepRed)+" "+r.style(line, ansiPink))
		}
		r.drawBoxBottom(w)
	})
}

// ─── User message box ──────────────────────────────────────────────

func (r *Renderer) UserMessage(message string) {
	r.writeWithInputPaused(r.out, func(w io.Writer) {
		if r.plain || !r.unicode {
			fmt.Fprintf(w, "USER: %s\n", message)
			return
		}
		r.drawBoxTop(w)
		for _, line := range strings.Split(message, "\n") {
			line = strings.TrimRight(line, "\r")
			r.drawBoxLine(w, r.style("●", ansiWhite)+" "+line)
		}
		r.drawBoxBottom(w)
		fmt.Fprintln(w)
	})
}

// ─── Tool output (no box) ──────────────────────────────────────────

func (r *Renderer) Tool(name, status string, durationMS int64, exit string, truncated bool) {
	line := fmt.Sprintf("TOOL     name=%s status=%s duration=%dms", name, status, durationMS)
	if exit != "" {
		line += " exit=" + exit
	}
	if truncated {
		line += " truncated=true"
	}
	r.writeWithInputPaused(r.err, func(w io.Writer) {
		fmt.Fprintln(w, r.style(line, ansiDeepRed))
	})
}

func (r *Renderer) Confirm(prompt string) bool {
	r.writeWithInputPaused(r.err, func(w io.Writer) {
		fmt.Fprint(w, r.style(prompt, ansiDeepRed))
	})
	input, err := readTerminalLine()
	if err != nil && input == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(input), "y")
}

func (r *Renderer) ApprovalBox(prompt string, selected int) int {
	width := r.width
	if width <= 0 {
		width = 80
	}
	if width > 96 {
		width = 96
	}
	if width < 36 {
		width = 36
	}
	inner := width - 4
	lines := r.approvalBoxLines(prompt, selected, inner)
	borderWidth := inner + 2
	var builder strings.Builder
	if r.plain || !r.unicode {
		builder.WriteString("+" + strings.Repeat("-", borderWidth) + "+\r\n")
		for _, line := range lines {
			builder.WriteString(r.approvalPlainLine(line, inner))
			builder.WriteString("\r\n")
		}
		builder.WriteString("+" + strings.Repeat("-", borderWidth) + "+\r\n")
		fmt.Fprint(r.err, builder.String())
		return len(lines) + 2
	}
	builder.WriteString(r.style("╭"+strings.Repeat("─", borderWidth)+"╮", ansiSoftRed))
	builder.WriteString("\r\n")
	for _, line := range lines {
		builder.WriteString(r.approvalStyledLine(line, inner))
		builder.WriteString("\r\n")
	}
	builder.WriteString(r.style("╰"+strings.Repeat("─", borderWidth)+"╯", ansiSoftRed))
	builder.WriteString("\r\n")
	fmt.Fprint(r.err, builder.String())
	return len(lines) + 2
}

func (r *Renderer) approvalBoxLines(prompt string, selected int, width int) []string {
	var lines []string
	lines = append(lines, "Approval request")
	lines = append(lines, strings.Repeat("─", width))
	for _, raw := range strings.Split(prompt, "\n") {
		raw = strings.TrimRight(raw, "\r")
		for _, wrapped := range wrapDisplay(raw, width) {
			lines = append(lines, wrapped)
		}
	}
	lines = append(lines, strings.Repeat("─", width))
	for index, option := range approvalOptions {
		marker := "  "
		if index == selected {
			marker = "> "
		}
		lines = append(lines, marker+option)
	}
	lines = append(lines, strings.Repeat("─", width))
	lines = append(lines, "↑/↓ select, Enter confirm")
	return lines
}

func (r *Renderer) approvalPlainLine(raw string, width int) string {
	raw = truncateDisplay(raw, width)
	padding := width - displayWidth(raw)
	if padding < 0 {
		padding = 0
	}
	return "| " + raw + strings.Repeat(" ", padding) + " |"
}

func (r *Renderer) approvalStyledLine(raw string, width int) string {
	raw = truncateDisplay(raw, width)
	padding := width - displayWidth(raw)
	if padding < 0 {
		padding = 0
	}
	color := ansiSoftRed
	if strings.HasPrefix(raw, "> ") || raw == "Approval request" {
		color = ansiWhite
	}
	return r.style("│ ", ansiSoftRed) + r.style(raw, color) + strings.Repeat(" ", padding) + r.style(" │", ansiSoftRed)
}

// ─── Banner ────────────────────────────────────────────────────────

func (r *Renderer) Banner(cfg *Config, registry *ToolRegistry, worklogPath string, skills int) {
	profile := valueOrUnknown(os.Getenv("ELIZA_PROFILE"))
	role := registry.Role()
	values := [][2]string{
		{"version", version}, {"profile", profile}, {"model", cfg.Model.Name},
		{"base_url", displayEndpoint(cfg.Model.BaseURL)}, {"api_key", maskSecret(cfg.Model.APIKey)},
		{"mode", registry.Mode()}, {"role", role},
		{"os/arch", cfg.System.OS + "/" + cfg.System.Architecture},
		{"skills", strconv.Itoa(skills)}, {"browser_tools", browserToolsStatus(registry)},
		{"memory", memoryDir()},
		{"worklog", worklogPath}, {"workspace", strings.Join(cfg.File.WorkspaceRoots, ";")},
	}

	if r.plain {
		fmt.Fprintln(r.out, bannerTitle)
		fmt.Fprintln(r.out, bannerCredit)
		for _, pair := range values {
			fmt.Fprintf(r.out, "%-12s %s\n", pair[0]+":", pair[1])
		}
	} else if r.unicode {
		if r.width >= 110 {
			r.drawWideStartupPanel(values)
		} else {
			if r.width >= elizaBrailleWidth {
				r.drawBrailleHero()
				fmt.Fprintln(r.out)
			}
			r.drawStackedStartupPanel(values)
		}
	} else {
		fmt.Fprintln(r.out, bannerTitle)
		fmt.Fprintln(r.out, bannerCredit)
		for _, pair := range values {
			fmt.Fprintf(r.out, "%-12s %s\n", pair[0]+":", pair[1])
		}
	}

	helpLine := "Input /help for commands  |  /status for status"
	for _, line := range wrapDisplay(helpLine, r.width) {
		fmt.Fprintln(r.out, r.style(line, ansiDim))
	}
}

func browserToolsStatus(registry *ToolRegistry) string {
	if registry == nil {
		return "disabled"
	}
	if _, ok := registry.Get("browser_open"); ok {
		return "enabled"
	}
	return "disabled"
}

func (r *Renderer) drawBrailleHero() {
	padding := (r.width - elizaBrailleWidth) / 2
	if padding < 0 {
		padding = 0
	}
	for index, glyphs := range elizaBrailleGlyphs {
		fmt.Fprintln(r.out, strings.Repeat(" ", padding)+r.renderBrailleRow(glyphs, elizaBrailleColors[index]))
	}
}

func (r *Renderer) drawWideStartupPanel(values [][2]string) {
	panelWidth := r.width
	if panelWidth > 132 {
		panelWidth = 132
	}
	innerWidth := panelWidth - 2
	girlWidth := elizaBrailleWidth
	leftWidth := innerWidth - girlWidth - 1
	leftLines := startupPanelLines(values, leftWidth)
	rowCount := len(leftLines)
	if len(elizaBrailleGlyphs) > rowCount {
		rowCount = len(elizaBrailleGlyphs)
	}

	r.drawPanelBorder('╭', '╮', panelWidth)
	for row := 0; row < rowCount; row++ {
		leftRaw := ""
		if row < len(leftLines) {
			leftRaw = leftLines[row]
		}
		leftRaw = truncateDisplay(leftRaw, leftWidth)
		leftPadding := leftWidth - displayWidth(leftRaw)
		if leftPadding < 0 {
			leftPadding = 0
		}
		girl := strings.Repeat(" ", girlWidth)
		if row < len(elizaBrailleGlyphs) {
			girl = r.renderBrailleRow(elizaBrailleGlyphs[row], elizaBrailleColors[row])
		}
		fmt.Fprint(r.out, r.style("│", ansiDeepRed))
		fmt.Fprint(r.out, r.stylePanelLine(leftRaw)+strings.Repeat(" ", leftPadding))
		fmt.Fprint(r.out, r.style("│", ansiDeepRed))
		fmt.Fprint(r.out, girl)
		fmt.Fprintln(r.out, r.style("│", ansiDeepRed))
	}
	r.drawPanelBorder('╰', '╯', panelWidth)
}

func (r *Renderer) drawStackedStartupPanel(values [][2]string) {
	panelWidth := r.width
	if panelWidth > 96 {
		panelWidth = 96
	}
	if panelWidth < 22 && r.width >= 22 {
		panelWidth = 22
	}
	innerWidth := panelWidth - 2
	leftLines := startupPanelLines(values, innerWidth)
	r.drawPanelBorder('╭', '╮', panelWidth)
	for _, raw := range leftLines {
		r.drawPanelContent(raw, innerWidth)
	}
	r.drawPanelBorder('╰', '╯', panelWidth)
}

func (r *Renderer) drawPanelBorder(left, right rune, width int) {
	fmt.Fprintln(r.out, r.style(string(left)+strings.Repeat("─", width-2)+string(right), ansiDeepRed))
}

func (r *Renderer) drawPanelContent(raw string, width int) {
	raw = truncateDisplay(raw, width)
	padding := width - displayWidth(raw)
	if padding < 0 {
		padding = 0
	}
	fmt.Fprint(r.out, r.style("│", ansiDeepRed))
	fmt.Fprint(r.out, r.stylePanelLine(raw)+strings.Repeat(" ", padding))
	fmt.Fprintln(r.out, r.style("│", ansiDeepRed))
}

func (r *Renderer) stylePanelLine(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == bannerTitle {
		return r.style(raw, ansiWhite)
	}
	if trimmed == bannerCredit {
		return r.style(raw, ansiDim)
	}
	if strings.Trim(trimmed, "─") == "" {
		return r.style(raw, ansiDeepRed)
	}
	colon := strings.Index(raw, ":")
	if colon < 0 {
		return r.style(raw, ansiWhite)
	}
	return r.style(raw[:colon+1], ansiPink) + r.style(raw[colon+1:], ansiWhite)
}

func startupPanelLines(values [][2]string, width int) []string {
	lines := []string{"  " + bannerTitle, "  " + bannerCredit}
	separatorWidth := width - 4
	if separatorWidth < 1 {
		separatorWidth = 1
	}
	lines = append(lines, "  "+strings.Repeat("─", separatorWidth))
	for _, pair := range values {
		prefix := fmt.Sprintf("  %-12s ", pair[0]+":")
		available := width - displayWidth(prefix)
		if available < 8 {
			available = 8
		}
		parts := wrapDisplay(pair[1], available)
		for index, part := range parts {
			if index == 0 {
				lines = append(lines, prefix+part)
			} else {
				lines = append(lines, strings.Repeat(" ", displayWidth(prefix))+part)
			}
		}
	}
	return lines
}

func truncateDisplay(text string, width int) string {
	if displayWidth(text) <= width {
		return text
	}
	if width <= 1 {
		return "…"
	}
	var builder strings.Builder
	used := 0
	for _, char := range text {
		charWidth := displayWidth(string(char))
		if used+charWidth > width-1 {
			break
		}
		builder.WriteRune(char)
		used += charWidth
	}
	return builder.String() + "…"
}

func (r *Renderer) renderBrailleRow(glyphRow, colorRow string) string {
	glyphs := []rune(glyphRow)
	colors := []rune(colorRow)
	var builder strings.Builder
	for index, glyph := range glyphs {
		if glyph == ' ' {
			builder.WriteRune(' ')
			continue
		}
		key := rune('K')
		if index < len(colors) {
			key = colors[index]
		}
		builder.WriteString(r.style(string(glyph), brailleColor(key)))
	}
	return builder.String()
}

func brailleColor(key rune) string {
	switch key {
	case 'W':
		return ansiWhite
	case 'P':
		return ansiHair
	case 'D':
		return ansiHairDim
	case 'S':
		return ansiSkin
	case 'K':
		return ansiEye
	case 'R':
		return ansiUniform
	case 'C':
		return ansiLaptop
	case 'O':
		return ansiLaptopShadow
	default:
		return ansiDeepRed
	}
}

func displayEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "<invalid>"
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
}

func displayWidth(text string) int {
	width := 0
	for _, char := range text {
		if unicode.Is(unicode.Han, char) || unicode.Is(unicode.Hangul, char) || unicode.Is(unicode.Hiragana, char) || unicode.Is(unicode.Katakana, char) {
			width += 2
		} else if !unicode.Is(unicode.Mn, char) {
			width++
		}
	}
	return width
}

func wrapDisplay(text string, width int) []string {
	if width < 1 {
		return []string{text}
	}
	var lines []string
	var builder strings.Builder
	current := 0
	for _, char := range text {
		charWidth := displayWidth(string(char))
		if current > 0 && current+charWidth > width {
			lines = append(lines, builder.String())
			builder.Reset()
			current = 0
		}
		builder.WriteRune(char)
		current += charWidth
	}
	if builder.Len() > 0 || len(lines) == 0 {
		lines = append(lines, builder.String())
	}
	return lines
}

func renderInputBufferLines(prefix string, buf []rune, pos int, width int) ([]string, int, int) {
	if width < 1 {
		width = 80
	}
	if pos < 0 {
		pos = 0
	}
	if pos > len(buf) {
		pos = len(buf)
	}
	prefixWidth := displayWidth(prefix)
	indent := strings.Repeat(" ", prefixWidth)
	currentPrefix := prefix
	line := currentPrefix
	col := prefixWidth
	lineIndex := 0
	cursorLine, cursorCol := 0, prefixWidth
	cursorSet := false
	var lines []string

	flush := func() {
		lines = append(lines, line)
		lineIndex++
		currentPrefix = indent
		line = currentPrefix
		col = prefixWidth
	}
	markCursor := func() {
		if cursorSet {
			return
		}
		cursorLine = lineIndex
		cursorCol = col
		cursorSet = true
	}

	if len(buf) == 0 {
		return []string{prefix}, 0, prefixWidth
	}
	for index, char := range buf {
		if index == pos {
			markCursor()
		}
		if char == '\n' {
			flush()
			continue
		}
		charText := string(char)
		charWidth := displayWidth(charText)
		if col > prefixWidth && col+charWidth > width {
			flush()
		}
		line += charText
		col += charWidth
	}
	if pos == len(buf) {
		markCursor()
	}
	lines = append(lines, line)
	return lines, cursorLine, cursorCol
}

// Braille cells preserve an 80x80 dot sampling of docs/logo.png in only 40x20
// terminal cells. The parallel color rows use the product's existing palette:
// W white, P/D pink hair, S skin, K outline, R uniform, C/O laptop.
const elizaBrailleWidth = 40

var elizaBrailleGlyphs = []string{
	"              ⢀⣀⣤⣤⡶⠶⠶⠶⠶⠶⠶⢶⣦⣤⣄⣀          ",
	"             ⠺⣿⣯⣁⣀        ⢀⣀⣹⣿⡿         ",
	"              ⠈⠉⠙⠛⢛⣿⣿⣿⣿⣿⣿⣿⡛⠛⠉⠉          ",
	"        ⢀⣠⠔⣛⣛⠃⣀⣤⣶⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣶⣄⣀       ",
	"        ⣼⣫⣾⡟⣵⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣦⡀     ",
	"        ⣿⣿⡟⢹⣿⣿⣿⡿⣿⣿⣿⣿⣿⣿⣿⣿⠏⣿⣿⣿⣿⣿⣿⣿⣿⣿⣷⡀    ",
	"       ⢸⢸⣿⢃⣿⣿⣿⣿⢇⣿⣿⣿⣿⣿⣿⣿⢏⣀⢻⣿⣿⣿⣿⣿⣿⣿⣿⣿⣧    ",
	"        ⣆⡉⣼⣿⣿⣿⣇⣛⡸⣿⣿⣿⣿⣿⡟⣘⣛⡘⣻⣿⣿⣿⣿⣿⣿⣿⣿⣿⡄   ",
	"       ⠚⢻⡇⣿⣿⣿⣿⡙⠛⢷⣃⡻⣿⣿⣼⡿⠟⠛⠿⡜⣿⣹⣿⠇⣿⣿⣿⣿⣿⣇   ",
	"         ⢰⣿⢻⣇⢹⢰⡆⣤⠙⣿⣞⣃⣿⡁⢠ ⠰⡘⡄⢹⣿⢀⣛⠛⣿⣿⣿⣿   ",
	"         ⠸⠃⣆⠻⢀⣼⡇  ⣿⣿⣿⣿⡇   ⣿⡇⢸⡟⣼⣿⡇⣿⣿⣿⡿⣇  ",
	"           ⢿⣷⡸⣿⣧⣤⣥⣿⣿⣿⣿⣧⣤⣤⣽⣿⡏⣏⢰⡿⢛⣡⣿⣿⣿⡇⠙⠦ ",
	"           ⢸⡟⣧⡻⢿⣿⣿⣟⡿⠿⣻⣿⣿⣿⣿⠿⠛  ⣴⣿⡉⢿⡿⠃⠇   ",
	"  ⣀⣀⣀⣀⣀⣀⣀⣀⣀⣈⣁⣘⣃ ⠈⢙⣛⠛⠛⠛⢛⣿⡍⣁⣤⣶⣤⣘⠻⡇⠈⠸⠁     ",
	" ⣿⣻⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣯⢰⣾⣿⣷⡰⣿⡿⣿⣼⣿⣿⣿⡿⣛⢿⡄        ",
	" ⠸⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⡆⢿⣿⣿⢗⣤⡴⣿⣿⢿⣿⣻⣿⣿⡎⠇        ",
	"  ⢹⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣈⢹⣿⡎⡏⣾⣿⡇⢾⡏⣿⣿⣿⣿⡀        ",
	"   ⢿⣿⠿⠿⣿⣿⣿⣿⣿⣿⠿⢿⠿⠿⣇⢶⡝⢇⣵⣿⣯⣖⢶⣶⣾⣿⣭⣻⣷        ",
	"   ⢈⣉⣛⣃⣀⣀⣀⣀⣀⣀⣛⣛⣛⣃⣛⣈⣃⣸⠱⠙⠿⠿⣸⣿⣿⣿⣿⣿⡟        ",
	"   ⠸⣿⣿⣸⣿⣹⣉⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⡿⠶⠿⠃⠉⠉ ⠈⠉⠉⠁        ",
}

var elizaBrailleColors = []string{
	"..............WWWWPSSSSSSOSWSW..........",
	".............SWSOK........KKSCC.........",
	"..............KWSOOOOOOOOOOOWO..........",
	"........PPPPPPOPPPDDPPPPPPPPOPPPO.......",
	"........PPSPPPPPPPPPPPPPPPPPPPPPSPP.....",
	"........DDPPCCSPPSPSSPSPPPPSSPWSCSPP....",
	".......PDDDPPPSKPPPPPPPPOPPPPPPPPPPP....",
	"........DDPPPPPSOPPPPDPSSPPPPPDPPPPPD...",
	".......DDDPPODOSOODDDPOSSOODOPPDPDPPD...",
	".........DDPPKWSWSSOOSCW.SSSPPSDPRDDD...",
	".........DDPPSSO..SSSSS...WOPPOOSRRRRD..",
	"...........PROSPSSSSSSSSSSSSDOSODRRRKDD.",
	"...........PRROOSSSSSSSSPS..RRRDDDK.....",
	"..OWWWWWWWCSPOO.SOOSSSSOSOWSPPDDKDD.....",
	".SCCCSSSSSSSCCCSRROSOOOOWPPODRRR........",
	".OOCCCCCCCCCCCSSSRDOOWOODORKKRKR........",
	"..SOCCCCCCCCCCSSCRRRDPKRKRRRRRRR........",
	"...SSCCSSSSSSCCCCCSDDOOOPOORDDDR........",
	"...KOSOPPPCCCCCCSSOOOOSSSRPRRDRR........",
	"...OCCSSSSCCCCCSSWOSCSSSOKO.RRKK........",
}
