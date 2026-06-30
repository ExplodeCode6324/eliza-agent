//go:build windows

package main

import (
	"errors"
	"syscall"
)

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(0x1000, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	_ = syscall.CloseHandle(handle)
	return true
}
