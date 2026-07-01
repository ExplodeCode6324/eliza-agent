package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"unicode"
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
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && value >= 20 {
		return value
	}
	return 80
}

func (r *Renderer) style(text, color string) string {
	if !r.color {
		return text
	}
	return color + text + ansiReset
}

// ─── Left-only box (no right border — avoids alignment issues) ─────

func (r *Renderer) drawBoxTop() {
	if r.plain || !r.unicode {
		return
	}
	fmt.Fprintln(r.out, r.style("╭──", ansiDeepRed))
}

func (r *Renderer) drawBoxLine(content string) {
	if r.plain || !r.unicode {
		fmt.Fprintln(r.out, content)
		return
	}
	fmt.Fprintln(r.out, r.style("│ ", ansiDeepRed)+content)
}

func (r *Renderer) drawBoxBottom() {
	if r.plain || !r.unicode {
		return
	}
	fmt.Fprintln(r.out, r.style("╰──", ansiDeepRed))
}

func (r *Renderer) Title(text string) {
	fmt.Fprintln(r.out, r.style(text, ansiDeepRed))
}

func (r *Renderer) Status(level, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%-8s %s", strings.ToUpper(level), message)
	if strings.EqualFold(level, "FAIL") || strings.EqualFold(level, "WARN") || strings.EqualFold(level, "BLOCKED") {
		fmt.Fprintln(r.err, r.style(line, ansiDeepRed))
		return
	}
	fmt.Fprintln(r.out, r.style(line, ansiWhite))
}

func (r *Renderer) Prompt(mode, role string) {
	fmt.Fprintf(r.out, "\n%s ", r.style(fmt.Sprintf("USER [%s/%s]>", mode, role), ansiWhite))
}

// ─── Assistant box (streaming → box at end) ────────────────────────

func (r *Renderer) BeginAssistant() {
	r.streaming = true
	r.streamBuf.Reset()
}

func (r *Renderer) AssistantChunk(chunk string) {
	r.streamBuf.WriteString(chunk)
}

func (r *Renderer) EndAssistant() {
	r.streaming = false
	content := strings.TrimSpace(r.streamBuf.String())
	if content == "" {
		return
	}
	r.drawBoxTop()
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		r.drawBoxLine(r.style("●", ansiDeepRed) + " " + r.style(line, ansiPink))
	}
	r.drawBoxBottom()
	r.streamBuf.Reset()
}

// ─── User message box ──────────────────────────────────────────────

func (r *Renderer) UserMessage(message string) {
	if r.plain || !r.unicode {
		fmt.Fprintf(r.out, "USER: %s\n", message)
		return
	}
	r.drawBoxTop()
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimRight(line, "\r")
		r.drawBoxLine(r.style("●", ansiWhite) + " " + line)
	}
	r.drawBoxBottom()
	fmt.Fprintln(r.out)
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
	fmt.Fprintln(r.err, r.style(line, ansiDeepRed))
}

func (r *Renderer) Confirm(prompt string) bool {
	fmt.Fprint(r.err, r.style(prompt, ansiDeepRed))
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
	if r.plain || !r.unicode {
		fmt.Fprintln(r.err, "+"+strings.Repeat("-", borderWidth)+"+")
		for _, line := range lines {
			r.approvalPlainLine(line, inner)
		}
		fmt.Fprintln(r.err, "+"+strings.Repeat("-", borderWidth)+"+")
		return len(lines) + 2
	}
	fmt.Fprintln(r.err, r.style("╭"+strings.Repeat("─", borderWidth)+"╮", ansiSoftRed))
	for _, line := range lines {
		r.approvalStyledLine(line, inner)
	}
	fmt.Fprintln(r.err, r.style("╰"+strings.Repeat("─", borderWidth)+"╯", ansiSoftRed))
	return len(lines) + 2
}

func (r *Renderer) approvalBoxLines(prompt string, selected int, width int) []string {
	var lines []string
	lines = append(lines, "审批请求")
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
	lines = append(lines, "↑/↓ 选择，Enter 确认")
	return lines
}

func (r *Renderer) approvalPlainLine(raw string, width int) {
	raw = truncateDisplay(raw, width)
	padding := width - displayWidth(raw)
	if padding < 0 {
		padding = 0
	}
	fmt.Fprintln(r.err, "| "+raw+strings.Repeat(" ", padding)+" |")
}

func (r *Renderer) approvalStyledLine(raw string, width int) {
	raw = truncateDisplay(raw, width)
	padding := width - displayWidth(raw)
	if padding < 0 {
		padding = 0
	}
	color := ansiSoftRed
	if strings.HasPrefix(raw, "> ") || raw == "审批请求" {
		color = ansiWhite
	}
	fmt.Fprint(r.err, r.style("│ ", ansiSoftRed))
	fmt.Fprint(r.err, r.style(raw, color)+strings.Repeat(" ", padding))
	fmt.Fprintln(r.err, r.style(" │", ansiSoftRed))
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
		{"skills", strconv.Itoa(skills)}, {"memory", memoryDir()},
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
