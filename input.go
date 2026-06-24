package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// ─── Cursor-aware line input ───────────────────────────────────────
//
// Enter raw terminal mode, read bytes one at a time, support:
//   - Arrow keys (left/right) for cursor movement
//   - Backspace / Delete (rune-aware for CJK)
//   - Home / End / Ctrl+A / Ctrl+E
//   - Ctrl+U to clear line
//   - Paste detection → multi-line / long text → temp file
//   - Enter to submit
//
// Falls back to bufio reader if raw mode is unavailable (Windows, pipes).
//
// KNOWN LIMITATION (2026-06-24):
// redrawLine() uses manual ANSI escape codes for multi-line clearing and
// cursor positioning.  When the paste buffer contains many \n characters,
// the cursor-up (\x1b[NA) + clear-to-end (\x1b[0J) sequence can cause
// residual visual artifacts on some terminals (faint repeat lines).
//
// TODO:  Create a 'with-bubbletea' branch that replaces the raw terminal
// input layer with github.com/charmbracelet/bubbletea (~30k★, Elm-like
// Model/Update/View architecture).  Bubble Tea has built-in textarea
// components with paste handling, cursor management, and proper viewport
// control.  The pure-stdlib build would remain on main as the zero-dep
// reference implementation.

// readLineInput reads a line from stdin with full cursor editing support.
func readLineInput() (string, error) {
	if err := enterRawTerminal(); err != nil {
		return readTerminalLineFallback()
	}
	defer exitRawTerminal()
	return readLineRaw()
}

// readLineRaw reads a line in raw terminal mode with rune-aware cursor support.
func readLineRaw() (string, error) {
	var buf []rune
	pos := 0 // cursor position in runes

	fd := os.Stdin
	data := make([]byte, 1)

	lastByteTime := time.Now() // 粘贴检测：记录字节到达间隔

	for {
		n, err := fd.Read(data)
		if err != nil || n == 0 {
			return "", err
		}
		b := data[0]
		now := time.Now()
		gap := now.Sub(lastByteTime)
		lastByteTime = now

		switch {
		case b == 3: // Ctrl+C
			return "", fmt.Errorf("interrupted")

		case b == 13: // Enter / CR
			// 粘贴检测: 距上一字节间隔 < 50ms → 粘贴中的换行 → 插入 \n, 不提交
			if gap < 50*time.Millisecond {
				buf = append(buf, 0)
				copy(buf[pos+1:], buf[pos:])
				buf[pos] = '\n'
				pos++
				// 粘贴时不重绘; 下一个手动输入会触发完整 redraw
				continue
			}
			content := strings.TrimRight(string(runesToBytes(buf)), "\n")
			if content == "" {
				fmt.Fprint(os.Stderr, "\r\n")
				return "", nil
			}
			if strings.Contains(content, "\n") || len(content) > 500 {
				path, err := writeStdinTempFile(content)
				if err != nil {
					return "", fmt.Errorf("写入临时文件失败: %w", err)
				}
				fmt.Fprintf(os.Stderr, "\r\n[FILE] %s\r\n", path)
				return "FILE:" + path, nil
			}
			fmt.Fprint(os.Stderr, "\r\n")
			return string(runesToBytes(buf)), nil

		case b == 127: // Backspace — delete one rune before cursor
			if pos > 0 {
				pos--
				buf = append(buf[:pos], buf[pos+1:]...)
				redrawLine(buf, pos)
			}

		case b == 27: // ESC sequence
			seq, ok := readEscSequence(fd)
			if !ok {
				continue
			}
			switch seq {
			case "[D": // Left
				if pos > 0 {
					pos--
					redrawLine(buf, pos)
				}
			case "[C": // Right
				if pos < len(buf) {
					pos++
					redrawLine(buf, pos)
				}
			case "[H": // Home
				pos = 0
				redrawLine(buf, pos)
			case "[F": // End
				pos = len(buf)
				redrawLine(buf, pos)
			case "[3~": // Delete — delete one rune at cursor
				if pos < len(buf) {
					buf = append(buf[:pos], buf[pos+1:]...)
					redrawLine(buf, pos)
				}
			}

		case b == 1: // Ctrl+A = Home
			pos = 0
			redrawLine(buf, pos)
		case b == 5: // Ctrl+E = End
			pos = len(buf)
			redrawLine(buf, pos)
		case b == 21: // Ctrl+U = clear line
			buf = nil
			pos = 0
			redrawLine(buf, pos)

		case b >= 32 && b <= 126: // ASCII printable
			r := rune(b)
			buf = append(buf, 0)
			copy(buf[pos+1:], buf[pos:])
			buf[pos] = r
			pos++
			redrawLine(buf, pos)

		case b == '\n': // newline (paste)
			buf = append(buf, 0)
			copy(buf[pos+1:], buf[pos:])
			buf[pos] = '\n'
			pos++
			// 粘贴时跳过重绘防闪烁; 手动输入时正常重绘
			if gap >= 50*time.Millisecond {
				redrawLine(buf, pos)
			}

		case b >= 128: // Multi-byte UTF-8 leading byte
			// Determine how many bytes in this UTF-8 sequence
			byteLen := utf8ByteCount(b)
			if byteLen < 2 {
				continue // invalid
			}
			// Read remaining bytes
			seq := make([]byte, byteLen)
			seq[0] = b
			for i := 1; i < byteLen; i++ {
				n, err := fd.Read(data)
				if err != nil || n == 0 {
					break
				}
				seq[i] = data[0]
			}
			r, size := utf8.DecodeRune(seq)
			if r == utf8.RuneError && size <= 1 {
				continue // invalid sequence, skip
			}
			buf = append(buf, 0)
			copy(buf[pos+1:], buf[pos:])
			buf[pos] = r
			pos++
			redrawLine(buf, pos)

		default:
			// Ignore other control chars
		}
	}
}

// utf8ByteCount returns the number of bytes in a UTF-8 sequence
// given the leading byte.
func utf8ByteCount(lead byte) int {
	switch {
	case lead&0x80 == 0:
		return 1
	case lead&0xE0 == 0xC0:
		return 2
	case lead&0xF0 == 0xE0:
		return 3
	case lead&0xF8 == 0xF0:
		return 4
	default:
		return 1
	}
}

// runesToBytes converts a rune slice to a byte slice.
func runesToBytes(runes []rune) []byte {
	var buf []byte
	for _, r := range runes {
		buf = utf8.AppendRune(buf, r)
	}
	return buf
}

// readEscSequence reads an ESC escape sequence from stdin.
func readEscSequence(fd *os.File) (string, bool) {
	data := make([]byte, 1)
	n, err := fd.Read(data)
	if err != nil || n == 0 {
		return "", false
	}
	if data[0] != '[' && data[0] != 'O' {
		return string(data[:1]), true
	}
	seq := string(data[:1])
	for i := 0; i < 4; i++ {
		n, err := fd.Read(data)
		if err != nil || n == 0 {
			return seq, true
		}
		seq += string(data[:1])
		if data[0] >= 0x40 && data[0] <= 0x7E {
			return seq, true
		}
	}
	return seq, true
}

// redrawLine clears all buffer lines and redraws buffer + positions cursor at pos.
// Handles multi-line buffers: counts \n to move cursor up and clear correctly.
func redrawLine(buf []rune, pos int) {
	content := string(runesToBytes(buf))
	newlines := 0
	for _, r := range buf {
		if r == '\n' {
			newlines++
		}
	}

	// Clear: go to start of current line, move up N lines, clear to end of screen
	if newlines > 0 {
		fmt.Fprintf(os.Stderr, "\r\x1b[%dA\x1b[0J", newlines)
	} else {
		fmt.Fprint(os.Stderr, "\r\x1b[0K")
	}

	// Draw content
	fmt.Fprint(os.Stderr, content)

	// Position cursor at pos: find line+column within multi-line buffer
	if pos < len(buf) && pos > 0 {
		lineNum := 0
		lastNewline := -1
		for i := 0; i < pos; i++ {
			if buf[i] == '\n' {
				lineNum++
				lastNewline = i
			}
		}
		prefixRunes := buf[lastNewline+1 : pos]
		prefixWidth := displayWidth(string(runesToBytes(prefixRunes)))
		linesUp := newlines - lineNum
		if linesUp > 0 {
			fmt.Fprintf(os.Stderr, "\r\x1b[%dA\x1b[%dC", linesUp, prefixWidth)
		} else {
			fmt.Fprintf(os.Stderr, "\r\x1b[%dC", prefixWidth)
		}
	}
}

// writeStdinTempFile writes content to a temp file for multi-line/long input.
func writeStdinTempFile(content string) (string, error) {
	f, err := os.CreateTemp("", "eliza-input-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// readTerminalLineFallback is the old bufio-based line reader (pipe mode).
func readTerminalLineFallback() (string, error) {
	return terminalFallback.ReadString('\n')
}

var terminalFallback = bufio.NewReader(os.Stdin)
