//go:build !windows

package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestReadPromptLineRawPTYHandlesCursorDeleteAndCJK(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()

	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()
	os.Stdin = slave

	var output bytes.Buffer
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, master)
		close(copyDone)
	}()

	renderer := &Renderer{out: slave, err: slave, color: false, unicode: true, width: 48}
	resultCh := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, err := renderer.ReadPromptLine(ModeReadonly, "default")
		resultCh <- struct {
			line string
			err  error
		}{line: line, err: err}
	}()

	time.Sleep(100 * time.Millisecond)
	_, _ = master.Write([]byte("abcdef"))
	_, _ = master.Write([]byte("\x1b[D\x1b[D"))
	_, _ = master.Write([]byte{127})
	_, _ = master.Write([]byte("中"))
	time.Sleep(pasteNewlineGap + 40*time.Millisecond)
	_, _ = master.Write([]byte{13})

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.line != "abc中ef" {
			t.Fatalf("prompt result mismatch: %q", result.line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for raw PTY prompt")
	}

	_ = slave.Close()
	_ = master.Close()
	<-copyDone

	text := output.String()
	for _, want := range []string{"╭─ INPUT", "╰─ abcdef", "╰─ abc中ef", "\x1b[0J"} {
		if !strings.Contains(text, want) {
			t.Fatalf("PTY redraw output missing %q: %q", want, text)
		}
	}
	if containsBareLF(text) {
		t.Fatalf("raw PTY output contains bare LF: %q", text)
	}
}
