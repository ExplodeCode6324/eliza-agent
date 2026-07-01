//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func readPendingTerminalInput() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", nil
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return "", err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		return "", err
	}
	defer unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)

	buf := make([]byte, 4096)
	var out []byte
	for {
		n, err := unix.Read(fd, buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err == nil {
			if n == 0 {
				break
			}
			continue
		}
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			break
		}
		if err == unix.EINTR {
			continue
		}
		return string(out), err
	}
	return string(out), nil
}
