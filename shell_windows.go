//go:build windows

package main

import (
	"context"
	"os/exec"
	"syscall"
)

func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe", "/C", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200}
	return cmd
}
