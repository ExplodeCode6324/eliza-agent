package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	bracketedPasteEnable  = "\x1b[?2004h"
	bracketedPasteDisable = "\x1b[?2004l"
	bracketedPasteStart   = "[200~"
	bracketedPasteEnd     = "\x1b[201~"

	pasteNewlineGap     = 50 * time.Millisecond
	pasteCollapseBytes  = 500
	pasteReferenceLabel = "Pasted text"
)

var (
	terminalFallback     = bufio.NewReader(os.Stdin)
	pasteReferenceID     int
	pasteReferenceRegexp = regexp.MustCompile(`\[Pasted text #\d+: \d+ lines -> ([^\]]+)\]`)
)

func readLineInput() (string, error) {
	if err := enterRawTerminal(); err != nil {
		return readTerminalLineFallback()
	}
	defer exitRawTerminal()
	enableBracketedPaste()
	defer disableBracketedPaste()
	return readLineRaw()
}

func readLineRaw() (string, error) {
	var buf []rune
	pos := 0
	fd := os.Stdin
	data := make([]byte, 1)
	lastByteTime := time.Now()
	dirtyBurstInput := false

	for {
		n, err := fd.Read(data)
		if err != nil || n == 0 {
			return "", err
		}
		b := data[0]
		now := time.Now()
		gap := now.Sub(lastByteTime)
		lastByteTime = now
		pasteNewline := gap < pasteNewlineGap

		switch {
		case b == 3:
			return "", fmt.Errorf("interrupted")

		case b == 13:
			if pasteNewline {
				insertText(&buf, &pos, "\n")
				dirtyBurstInput = true
				continue
			}
			return submitInputBuffer(buf)

		case b == 127:
			collapseBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput)
			if pos > 0 {
				pos--
				buf = append(buf[:pos], buf[pos+1:]...)
				redrawLine(buf, pos)
			}

		case b == 27:
			collapseBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput)
			seq, ok := readEscSequence(fd)
			if !ok {
				continue
			}
			switch seq {
			case bracketedPasteStart:
				pasted, err := readBracketedPaste(fd)
				if err != nil {
					return "", err
				}
				insertPaste(&buf, &pos, pasted)
				dirtyBurstInput = false
				redrawLine(buf, pos)
			case "[D":
				if pos > 0 {
					pos--
					redrawLine(buf, pos)
				}
			case "[C":
				if pos < len(buf) {
					pos++
					redrawLine(buf, pos)
				}
			case "[H":
				pos = 0
				redrawLine(buf, pos)
			case "[F":
				pos = len(buf)
				redrawLine(buf, pos)
			case "[3~":
				if pos < len(buf) {
					buf = append(buf[:pos], buf[pos+1:]...)
					redrawLine(buf, pos)
				}
			}

		case b == 1:
			collapseBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput)
			pos = 0
			redrawLine(buf, pos)
		case b == 5:
			collapseBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput)
			pos = len(buf)
			redrawLine(buf, pos)
		case b == 21:
			buf = nil
			pos = 0
			redrawLine(buf, pos)

		case b >= 32 && b <= 126:
			collapseIdleBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput, gap)
			insertText(&buf, &pos, string(rune(b)))
			if dirtyBurstInput {
				continue
			}
			redrawLine(buf, pos)

		case b == '\n':
			insertText(&buf, &pos, "\n")
			dirtyBurstInput = true

		case b >= 128:
			collapseIdleBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput, gap)
			r, ok := readUTF8Rune(fd, data, b)
			if !ok {
				continue
			}
			insertText(&buf, &pos, string(r))
			if dirtyBurstInput {
				continue
			}
			redrawLine(buf, pos)
		}
	}
}

func submitInputBuffer(buf []rune) (string, error) {
	content := strings.TrimRight(string(runesToBytes(buf)), "\n")
	if content == "" {
		fmt.Fprint(os.Stderr, "\r\n")
		return "", nil
	}

	content, cleanup := expandPasteReferences(content)
	for _, path := range cleanup {
		_ = os.Remove(path)
	}

	if shouldStoreInputAsFile(content) {
		path, err := writeStdinTempFile(content)
		if err != nil {
			return "", fmt.Errorf("write temp input file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "\r\n[FILE] %s\r\n", path)
		return "FILE:" + path, nil
	}

	fmt.Fprint(os.Stderr, "\r\n")
	return content, nil
}

func insertPaste(buf *[]rune, pos *int, pasted string) {
	pasted = normalizePastedText(pasted)
	if pasted == "" {
		return
	}
	if shouldStoreInputAsFile(pasted) {
		path, err := writeStdinTempFile(pasted)
		if err == nil {
			pasteReferenceID++
			placeholder := fmt.Sprintf("[%s #%d: %d lines -> %s]", pasteReferenceLabel, pasteReferenceID, lineCount(pasted), path)
			if *pos > 0 && (*buf)[*pos-1] != ' ' {
				placeholder = " " + placeholder
			}
			insertText(buf, pos, placeholder)
			return
		}
	}
	insertText(buf, pos, pasted)
}

func readBracketedPaste(fd *os.File) (string, error) {
	data := make([]byte, 1)
	var builder strings.Builder
	tail := make([]byte, 0, len(bracketedPasteEnd))
	for {
		n, err := fd.Read(data)
		if err != nil || n == 0 {
			return builder.String(), err
		}
		builder.WriteByte(data[0])
		tail = append(tail, data[0])
		if len(tail) > len(bracketedPasteEnd) {
			tail = tail[1:]
		}
		if string(tail) == bracketedPasteEnd {
			content := builder.String()
			return content[:len(content)-len(bracketedPasteEnd)], nil
		}
	}
}

func readUTF8Rune(fd *os.File, data []byte, lead byte) (rune, bool) {
	byteLen := utf8ByteCount(lead)
	if byteLen < 2 {
		return utf8.RuneError, false
	}
	seq := make([]byte, byteLen)
	seq[0] = lead
	for i := 1; i < byteLen; i++ {
		n, err := fd.Read(data)
		if err != nil || n == 0 {
			return utf8.RuneError, false
		}
		seq[i] = data[0]
	}
	r, size := utf8.DecodeRune(seq)
	if r == utf8.RuneError && size <= 1 {
		return utf8.RuneError, false
	}
	return r, true
}

func insertText(buf *[]rune, pos *int, text string) {
	runes := []rune(text)
	if len(runes) == 0 {
		return
	}
	*buf = append(*buf, make([]rune, len(runes))...)
	copy((*buf)[*pos+len(runes):], (*buf)[*pos:])
	copy((*buf)[*pos:], runes)
	*pos += len(runes)
}

func collapseBurstBufferForDisplay(buf *[]rune, pos *int, dirty *bool) {
	if !*dirty {
		return
	}
	*dirty = false
	content := string(runesToBytes(*buf))
	if !shouldStoreInputAsFile(content) {
		return
	}
	path, err := writeStdinTempFile(content)
	if err != nil {
		return
	}
	pasteReferenceID++
	placeholder := fmt.Sprintf("[%s #%d: %d lines -> %s]", pasteReferenceLabel, pasteReferenceID, lineCount(content), path)
	*buf = []rune(placeholder)
	*pos = len(*buf)
}

func collapseIdleBurstBufferForDisplay(buf *[]rune, pos *int, dirty *bool, gap time.Duration) {
	if gap >= pasteNewlineGap {
		collapseBurstBufferForDisplay(buf, pos, dirty)
	}
}

func expandPasteReferences(content string) (string, []string) {
	var cleanup []string
	expanded := pasteReferenceRegexp.ReplaceAllStringFunc(content, func(match string) string {
		parts := pasteReferenceRegexp.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		path := parts[1]
		data, err := os.ReadFile(path)
		if err != nil {
			return match
		}
		cleanup = append(cleanup, path)
		return string(data)
	})
	return expanded, cleanup
}

func shouldStoreInputAsFile(content string) bool {
	return strings.Contains(content, "\n") || len(content) > pasteCollapseBytes
}

func normalizePastedText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\x1b[200~", "")
	text = strings.ReplaceAll(text, "\x1b[201~", "")
	text = strings.ReplaceAll(text, "^[[200~", "")
	text = strings.ReplaceAll(text, "^[[201~", "")
	return text
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func enableBracketedPaste() {
	fmt.Fprint(os.Stderr, bracketedPasteEnable)
}

func disableBracketedPaste() {
	fmt.Fprint(os.Stderr, bracketedPasteDisable)
}

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

func runesToBytes(runes []rune) []byte {
	var buf []byte
	for _, r := range runes {
		buf = utf8.AppendRune(buf, r)
	}
	return buf
}

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

func redrawLine(buf []rune, pos int) {
	content := string(runesToBytes(buf))
	newlines := 0
	for _, r := range buf {
		if r == '\n' {
			newlines++
		}
	}

	if newlines > 0 {
		fmt.Fprintf(os.Stderr, "\r\x1b[%dA\x1b[0J", newlines)
	} else {
		fmt.Fprint(os.Stderr, "\r\x1b[0K")
	}

	fmt.Fprint(os.Stderr, content)

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

func readTerminalLineFallback() (string, error) {
	return terminalFallback.ReadString('\n')
}
