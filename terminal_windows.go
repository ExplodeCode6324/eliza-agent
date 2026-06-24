//go:build windows

package main

// Windows: raw terminal support not implemented.
// Falls back to bufio reader (current behavior).

func enterRawTerminal() error { return nil }
func exitRawTerminal()        {}
