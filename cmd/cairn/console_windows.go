package main

import (
	"os"
	"syscall"
)

// attachConsole gives the program the console it was started from, if
// any, for what it prints: built as a windowed program, it starts without
// one, so that opening a folder flashes no console window.
func attachConsole() {
	if _, err := os.Stdout.Stat(); err == nil {
		// Output already goes somewhere, as to a pipe or a file.
		return
	}
	const attachParentProcess = ^uintptr(0)
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := proc.Call(attachParentProcess); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}
