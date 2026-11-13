//go:build !windows

package lnk

// ansi reads bytes as they are, where there is no code page to ask for.
func ansi(b []byte) string { return string(b) }
