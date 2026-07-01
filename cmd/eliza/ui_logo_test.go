package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestBrailleLogoPreservesReferenceFeaturesAndText(t *testing.T) {
	if len(elizaBrailleGlyphs) != 20 || len(elizaBrailleColors) != len(elizaBrailleGlyphs) {
		t.Fatalf("unexpected braille logo dimensions: glyphs=%d colors=%d", len(elizaBrailleGlyphs), len(elizaBrailleColors))
	}
	joined := strings.Join(elizaBrailleColors, "")
	for _, marker := range []string{"W", "P", "D", "S", "K", "R", "C", "O"} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("braille logo lost palette feature %s", marker)
		}
	}
	for index, row := range elizaBrailleGlyphs {
		if len([]rune(row)) != elizaBrailleWidth {
			t.Fatalf("braille row %d width changed: %q", index, row)
		}
		if len([]rune(elizaBrailleColors[index])) != elizaBrailleWidth {
			t.Fatalf("braille color row %d width changed: %q", index, elizaBrailleColors[index])
		}
	}
	if bannerTitle != "ELIZA-AGENT (DRC Bank ver.)" || bannerCredit != "Powered By MUY & ELIZA" {
		t.Fatal("existing logo text was changed")
	}

	plainRenderer := &Renderer{color: false}
	plain := plainRenderer.renderBrailleRow(elizaBrailleGlyphs[0], elizaBrailleColors[0])
	if strings.Contains(plain, "\x1b[") || !strings.ContainsAny(plain, "⢀⣀⣤⡶") {
		t.Fatalf("no-color braille fallback is invalid: %q", plain)
	}
	colorRenderer := &Renderer{color: true}
	colored := colorRenderer.renderBrailleRow(elizaBrailleGlyphs[0], elizaBrailleColors[0])
	if !strings.Contains(colored, ansiHair) || !strings.Contains(colored, ansiWhite) {
		t.Fatal("colored braille logo lost hair/highlight colors")
	}
}

func TestWideBannerUsesBrailleGirlAndSingleStartupPanel(t *testing.T) {
	var output bytes.Buffer
	renderer := &Renderer{out: &output, err: &output, color: false, unicode: true, width: 132}
	policy, err := NewCommandPolicy(ModeReadonly, nil, DefaultReadonlyCommands())
	if err != nil {
		t.Fatal(err)
	}
	registry := NewToolRegistry(policy)
	cfg := &Config{
		Model:  ModelConfig{Name: "model", BaseURL: "https://example.internal/v1", APIKey: "secret-value"},
		System: SystemInfo{OS: "linux", Architecture: "amd64"},
		File:   FilePolicyConfig{WorkspaceRoots: []string{"/workspace"}},
	}
	renderer.Banner(cfg, registry, "/worklogs/session.md", 3)
	text := output.String()
	first := strings.Split(strings.TrimLeft(text, "\n"), "\n")[0]
	if !strings.HasPrefix(first, "╭") {
		t.Fatalf("wide banner should start with the panel, got %q", first)
	}
	if strings.Count(text, "╭") != 1 || strings.Count(text, "╯") != 1 {
		t.Fatalf("startup information is not enclosed by one panel: %q", text)
	}
	if !strings.ContainsAny(text, "⢀⣀⣤⡶⣿") || !strings.Contains(text, bannerTitle) || !strings.Contains(text, "version:") {
		t.Fatal("panel is missing the girl, title, or startup parameters")
	}
	if !strings.Contains(text, "browser_tools:") || !strings.Contains(text, "disabled") {
		t.Fatalf("startup panel should include browser tool status: %q", text)
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if displayWidth(line) > 132 {
			t.Fatalf("wide banner line overflowed: width=%d line=%q", displayWidth(line), line)
		}
	}
}

func TestBannerShowsEnabledBrowserToolsInsideStartupPanel(t *testing.T) {
	var output bytes.Buffer
	renderer := &Renderer{out: &output, err: &output, color: false, unicode: true, width: 96}
	policy, _ := NewCommandPolicy(ModeReadonly, nil, DefaultReadonlyCommands())
	registry := NewToolRegistry(policy)
	registry.Register(&BrowserOpenTool{})
	cfg := &Config{
		Model:  ModelConfig{Name: "model", BaseURL: "https://example.internal/v1", APIKey: "secret-value"},
		System: SystemInfo{OS: "linux", Architecture: "amd64"},
		File:   FilePolicyConfig{WorkspaceRoots: []string{"/workspace"}},
	}

	renderer.Banner(cfg, registry, "/worklogs/session.md", 3)
	text := output.String()

	if !strings.Contains(text, "browser_tools:") || !strings.Contains(text, "enabled") {
		t.Fatalf("startup panel should show enabled browser tools: %q", text)
	}
}

func TestStandardWidthPutsGirlAboveParametersWithoutOverflow(t *testing.T) {
	var output bytes.Buffer
	renderer := &Renderer{out: &output, err: &output, color: false, unicode: true, width: 80}
	policy, _ := NewCommandPolicy(ModeReadonly, nil, DefaultReadonlyCommands())
	registry := NewToolRegistry(policy)
	cfg := &Config{
		Model:  ModelConfig{Name: "model", BaseURL: "https://example.internal/v1", APIKey: "secret-value"},
		System: SystemInfo{OS: "linux", Architecture: "amd64"},
		File:   FilePolicyConfig{WorkspaceRoots: []string{"/workspace"}},
	}
	renderer.Banner(cfg, registry, "/worklogs/session.md", 3)
	text := output.String()
	panelIndex := strings.Index(text, "╭")
	brailleIndex := strings.IndexAny(text, "⢀⣀⣤⡶⣿")
	if !strings.Contains(text, "version:") || brailleIndex < 0 || panelIndex < 0 || brailleIndex > panelIndex {
		t.Fatal("80-column banner should put the braille girl above the parameter panel")
	}
	if strings.Count(text, "╭") != 1 || strings.Count(text, "╯") != 1 {
		t.Fatal("80-column parameter area should be one undivided panel")
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if displayWidth(line) > 80 {
			t.Fatalf("80-column banner overflowed: width=%d line=%q", displayWidth(line), line)
		}
	}
}

func TestVeryNarrowBannerOmitsArtWithoutOverflow(t *testing.T) {
	var output bytes.Buffer
	renderer := &Renderer{out: &output, err: &output, color: false, unicode: true, width: 30}
	policy, _ := NewCommandPolicy(ModeReadonly, nil, DefaultReadonlyCommands())
	registry := NewToolRegistry(policy)
	cfg := &Config{
		Model:  ModelConfig{Name: "model", BaseURL: "https://example.internal/v1", APIKey: "secret-value"},
		System: SystemInfo{OS: "linux", Architecture: "amd64"},
		File:   FilePolicyConfig{WorkspaceRoots: []string{"/workspace"}},
	}
	renderer.Banner(cfg, registry, "/worklogs/session.md", 3)
	text := output.String()
	if strings.ContainsAny(text, "⢀⣀⣤⡶⣿") {
		t.Fatal("very narrow banner should omit rather than crop the braille art")
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if displayWidth(line) > 30 {
			t.Fatalf("30-column banner overflowed: width=%d line=%q", displayWidth(line), line)
		}
	}
}

func TestApprovalBoxFramesOptionsWithoutSlashCommands(t *testing.T) {
	var output bytes.Buffer
	renderer := &Renderer{out: &output, err: &output, color: false, unicode: true, width: 72}

	lines := renderer.ApprovalBox("Dangerous command: rm file.txt", 2)
	text := output.String()

	if lines < 8 || !strings.Contains(text, "╭") || !strings.Contains(text, "╰") {
		t.Fatalf("approval prompt was not framed as a box: lines=%d text=%q", lines, text)
	}
	if !strings.Contains(text, "Approval request") || !strings.Contains(text, "> Deny and tell ELIZA what to do") {
		t.Fatalf("approval prompt is missing title or guidance option: %q", text)
	}
	for _, chinese := range []string{"审批", "拒绝", "批准", "确认"} {
		if strings.Contains(text, chinese) {
			t.Fatalf("approval prompt should be English-only, found %q in %q", chinese, text)
		}
	}
	if strings.Contains(text, "/approve") || strings.Contains(text, "/deny") {
		t.Fatalf("approval prompt still references slash commands: %q", text)
	}
}

func TestApprovalBoxUsesCRLFLineEndingsForRawMode(t *testing.T) {
	var output bytes.Buffer
	renderer := &Renderer{out: &output, err: &output, color: false, unicode: true, width: 72}

	renderer.ApprovalBox("Dangerous command: rm file.txt", 0)
	text := output.String()

	if strings.Contains(text, "\n") && containsBareLF(text) {
		t.Fatalf("approval prompt contains bare LF in raw-mode output: %q", text)
	}
	if !strings.Contains(text, "\r\n") {
		t.Fatalf("approval prompt did not use CRLF line endings: %q", text)
	}
}

func TestPromptAndRunningInputBarAreVisible(t *testing.T) {
	var output bytes.Buffer
	renderer := &Renderer{out: &output, err: &output, color: false, unicode: true, width: 72}

	renderer.Prompt(ModeReadonly, "default")
	renderer.RunningInputBar(ModeReadonly, "default")
	text := output.String()

	for _, want := range []string{"╭─ INPUT", "USER [readonly/default]", "╭─ GUIDE", "RUNNING [readonly/default]", "/cancel"} {
		if !strings.Contains(text, want) {
			t.Fatalf("input bar missing %q: %q", want, text)
		}
	}
}

func TestRenderInputBufferLinesWrapsSoftLines(t *testing.T) {
	lines, cursorLine, cursorCol := renderInputBufferLines("╰─ ", []rune("现在是功能测试。去浏览一下baidu.com"), len([]rune("现在是功能测试。去浏览一下baidu.com")), 24)
	if len(lines) < 2 {
		t.Fatalf("expected soft wrapping, got %#v", lines)
	}
	for _, line := range lines {
		if displayWidth(line) > 24 {
			t.Fatalf("input line overflowed: width=%d line=%q", displayWidth(line), line)
		}
	}
	if cursorLine != len(lines)-1 {
		t.Fatalf("cursor should be on last soft line: cursor=%d lines=%d", cursorLine, len(lines))
	}
	if cursorCol <= 0 || cursorCol > 24 {
		t.Fatalf("cursor col out of range: %d", cursorCol)
	}
}

func TestStatusRedrawsActiveInputOverlay(t *testing.T) {
	var output bytes.Buffer
	renderer := &Renderer{out: &output, err: &output, color: false, unicode: true, width: 48}

	renderer.Prompt(ModeAutopilot, "default")
	renderer.updateInput([]rune("partial input"), len([]rune("partial input")))
	renderer.Status("RUNNING", "step 2")

	text := output.String()
	if !strings.Contains(text, "\x1b[0J") || !strings.Contains(text, "RUNNING  step 2") || !strings.Contains(text, "partial input") {
		t.Fatalf("status did not clear and redraw input overlay: %q", text)
	}
}

func containsBareLF(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] == '\n' && (index == 0 || text[index-1] != '\r') {
			return true
		}
	}
	return false
}
