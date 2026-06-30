package main

import (
	"os"
	"strings"
	"testing"
)

func TestInsertPasteCollapsesMultilineToReference(t *testing.T) {
	var buf []rune
	pos := 0

	insertPaste(&buf, &pos, "line1\r\nline2")

	placeholder := string(runesToBytes(buf))
	if !strings.Contains(placeholder, "[Pasted text #") || strings.Contains(placeholder, "line1\nline2") {
		t.Fatalf("paste was not collapsed to a file reference: %q", placeholder)
	}
	expanded, cleanup := expandPasteReferences(placeholder)
	defer removeAll(cleanup)
	if expanded != "line1\nline2" {
		t.Fatalf("expanded paste mismatch: %q", expanded)
	}
	if len(cleanup) != 1 {
		t.Fatalf("expected one paste file cleanup path, got %d", len(cleanup))
	}
}

func TestSubmitInputBufferExpandsPasteReferenceIntoFinalFile(t *testing.T) {
	path, err := writeStdinTempFile("alpha\nbeta")
	if err != nil {
		t.Fatal(err)
	}
	placeholder := "[Pasted text #99: 2 lines -> " + path + "]"

	result, err := submitInputBuffer([]rune(placeholder))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, "FILE:") {
		t.Fatalf("expected final file input, got %q", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("intermediate paste file was not removed: %v", err)
	}
	finalPath := strings.TrimPrefix(result, "FILE:")
	defer os.Remove(finalPath)
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\nbeta" {
		t.Fatalf("final file content mismatch: %q", string(data))
	}
}

func TestReadBracketedPasteStopsAtEndMarker(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	go func() {
		defer writer.Close()
		_, _ = writer.WriteString("hello\nworld" + bracketedPasteEnd)
	}()

	got, err := readBracketedPaste(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello\nworld" {
		t.Fatalf("bracketed paste content mismatch: %q", got)
	}
}

func removeAll(paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}
