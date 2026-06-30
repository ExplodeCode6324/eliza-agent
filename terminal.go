package main

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

var originalTermState *term.State

func enterRawTerminal() error {
	if originalTermState != nil {
		return nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	originalTermState = old
	return nil
}

func exitRawTerminal() {
	if originalTermState == nil {
		return
	}
	_ = term.Restore(int(os.Stdin.Fd()), originalTermState)
	originalTermState = nil
}
