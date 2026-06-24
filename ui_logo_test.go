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
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if displayWidth(line) > 132 {
			t.Fatalf("wide banner line overflowed: width=%d line=%q", displayWidth(line), line)
		}
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
