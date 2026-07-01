package main

import (
	"bufio"
	"bytes"
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
	return readLineRawWith(redrawLine, submitInputBuffer)
}

func readLineRawWith(redraw func([]rune, int), submit func([]rune) (string, error)) (string, error) {
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
			return submit(buf)

		case b == 127:
			collapseBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput)
			if pos > 0 {
				pos--
				buf = append(buf[:pos], buf[pos+1:]...)
				redraw(buf, pos)
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
				redraw(buf, pos)
			case "[D":
				if pos > 0 {
					pos--
					redraw(buf, pos)
				}
			case "[C":
				if pos < len(buf) {
					pos++
					redraw(buf, pos)
				}
			case "[H":
				pos = 0
				redraw(buf, pos)
			case "[F":
				pos = len(buf)
				redraw(buf, pos)
			case "[3~":
				if pos < len(buf) {
					buf = append(buf[:pos], buf[pos+1:]...)
					redraw(buf, pos)
				}
			}

		case b == 1:
			collapseBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput)
			pos = 0
			redraw(buf, pos)
		case b == 5:
			collapseBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput)
			pos = len(buf)
			redraw(buf, pos)
		case b == 21:
			buf = nil
			pos = 0
			redraw(buf, pos)

		case b >= 32 && b <= 126:
			collapseIdleBurstBufferForDisplay(&buf, &pos, &dirtyBurstInput, gap)
			insertText(&buf, &pos, string(rune(b)))
			if dirtyBurstInput {
				continue
			}
			redraw(buf, pos)

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
			redraw(buf, pos)
		}
	}
}

func readApprovalChoice(render func(int) int, optionCount int) (int, error) {
	terminalMu.Lock()
	defer terminalMu.Unlock()
	if optionCount <= 0 {
		return 0, fmt.Errorf("approval options are empty")
	}
	if err := enterRawTerminal(); err != nil {
		render(0)
		return 0, err
	}
	defer exitRawTerminal()
	return readApprovalChoiceRaw(render, optionCount)
}

func readApprovalChoiceRaw(render func(int) int, optionCount int) (int, error) {
	if optionCount <= 0 {
		return 0, fmt.Errorf("approval options are empty")
	}
	selected := 0
	renderedLines := 0
	redraw := func() {
		if renderedLines > 0 {
			fmt.Fprintf(os.Stderr, "\r\x1b[%dA\x1b[0J", renderedLines)
		}
		renderedLines = render(selected)
	}
	redraw()

	fd := os.Stdin
	data := make([]byte, 1)
	for {
		n, err := fd.Read(data)
		if err != nil || n == 0 {
			return selected, err
		}
		switch data[0] {
		case 3:
			fmt.Fprint(os.Stderr, "\r\n")
			return 0, nil
		case 13, '\n':
			fmt.Fprint(os.Stderr, "\r\n")
			return selected, nil
		case 27:
			seq, ok := readEscSequence(fd)
			if !ok {
				continue
			}
			switch seq {
			case "[A", "OA":
				selected = (selected + optionCount - 1) % optionCount
				redraw()
			case "[B", "OB":
				selected = (selected + 1) % optionCount
				redraw()
			}
		}
	}
}

func submitInputBuffer(buf []rune) (string, error) {
	content, filePath, err := submitInputContent(buf)
	if err != nil {
		return "", err
	}
	if content == "" {
		fmt.Fprint(os.Stderr, "\r\n")
		return "", nil
	}
	if filePath != "" {
		fmt.Fprintf(os.Stderr, "\r\n[FILE] %s\r\n", filePath)
		return content, nil
	}
	fmt.Fprint(os.Stderr, "\r\n")
	return content, nil
}

func submitInputBufferQuiet(buf []rune) (string, error) {
	content, _, err := submitInputContent(buf)
	return content, err
}

func submitInputContent(buf []rune) (string, string, error) {
	content := strings.TrimRight(string(runesToBytes(buf)), "\n")
	if content == "" {
		return "", "", nil
	}

	content, cleanup := expandPasteReferences(content)
	for _, path := range cleanup {
		_ = os.Remove(path)
	}

	if shouldStoreInputAsFile(content) {
		path, err := writeStdinTempFile(content)
		if err != nil {
			return "", "", fmt.Errorf("write temp input file: %w", err)
		}
		return "FILE:" + path, path, nil
	}

	return content, "", nil
}

func feedPendingInputBytes(buf []rune, pos int, chunk []byte, redraw func([]rune, int)) ([]rune, int, []string, bool, error) {
	var submitted []string
	interrupted := false
	for index := 0; index < len(chunk); {
		b := chunk[index]
		switch {
		case b == 3:
			interrupted = true
			index++

		case b == 13:
			content, err := submitInputBufferQuiet(buf)
			if err != nil {
				return buf, pos, submitted, interrupted, err
			}
			if strings.TrimSpace(content) != "" {
				submitted = append(submitted, content)
			}
			buf = nil
			pos = 0
			redraw(buf, pos)
			index++

		case b == '\n':
			insertText(&buf, &pos, "\n")
			redraw(buf, pos)
			index++

		case b == 127:
			if pos > 0 {
				pos--
				buf = append(buf[:pos], buf[pos+1:]...)
				redraw(buf, pos)
			}
			index++

		case b == 27:
			advanced := false
			if bytes.HasPrefix(chunk[index:], []byte("\x1b[200~")) {
				start := index + len("\x1b[200~")
				if end := bytes.Index(chunk[start:], []byte(bracketedPasteEnd)); end >= 0 {
					pasted := string(chunk[start : start+end])
					insertPaste(&buf, &pos, pasted)
					redraw(buf, pos)
					index = start + end + len(bracketedPasteEnd)
					advanced = true
				}
			}
			if !advanced {
				seq, consumed := pendingEscSequence(chunk[index:])
				if consumed <= 0 {
					index++
					continue
				}
				switch seq {
				case "[D":
					if pos > 0 {
						pos--
						redraw(buf, pos)
					}
				case "[C":
					if pos < len(buf) {
						pos++
						redraw(buf, pos)
					}
				case "[H":
					pos = 0
					redraw(buf, pos)
				case "[F":
					pos = len(buf)
					redraw(buf, pos)
				case "[3~":
					if pos < len(buf) {
						buf = append(buf[:pos], buf[pos+1:]...)
						redraw(buf, pos)
					}
				}
				index += consumed
			}

		case b == 1:
			pos = 0
			redraw(buf, pos)
			index++
		case b == 5:
			pos = len(buf)
			redraw(buf, pos)
			index++
		case b == 21:
			buf = nil
			pos = 0
			redraw(buf, pos)
			index++

		case b >= 32 && b <= 126:
			insertText(&buf, &pos, string(rune(b)))
			redraw(buf, pos)
			index++

		case b >= 128:
			r, size := utf8.DecodeRune(chunk[index:])
			if r == utf8.RuneError && size <= 1 {
				index++
				continue
			}
			insertText(&buf, &pos, string(r))
			redraw(buf, pos)
			index += size

		default:
			index++
		}
	}
	return buf, pos, submitted, interrupted, nil
}

func pendingEscSequence(chunk []byte) (string, int) {
	if len(chunk) < 2 || chunk[0] != 27 {
		return "", 0
	}
	if chunk[1] != '[' && chunk[1] != 'O' {
		return string(chunk[1:2]), 2
	}
	limit := min(len(chunk), 6)
	for index := 2; index < limit; index++ {
		if chunk[index] >= 0x40 && chunk[index] <= 0x7E {
			return string(chunk[1 : index+1]), index + 1
		}
	}
	return "", 0
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
