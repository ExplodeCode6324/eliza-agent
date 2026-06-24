//go:build !windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var originalTermios *syscall.Termios

func makeRaw(fd int) (*syscall.Termios, error) {
	var old syscall.Termios
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&old)), 0, 0, 0); err != 0 {
		return nil, err
	}
	new := old
	new.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	new.Oflag &^= syscall.OPOST
	new.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	new.Cflag &^= syscall.CSIZE | syscall.PARENB
	new.Cflag |= syscall.CS8
	new.Cc[syscall.VMIN] = 1
	new.Cc[syscall.VTIME] = 0
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&new)), 0, 0, 0); err != 0 {
		return nil, err
	}
	return &old, nil
}

func restoreTerminal(fd int, old *syscall.Termios) {
	if old != nil {
		syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
			uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(old)), 0, 0, 0)
	}
}

func enterRawTerminal() error {
	if originalTermios != nil {
		return nil
	}
	old, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	originalTermios = old
	return nil
}

func exitRawTerminal() {
	if originalTermios != nil {
		restoreTerminal(int(os.Stdin.Fd()), originalTermios)
		originalTermios = nil
	}
}
