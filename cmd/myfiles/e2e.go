//go:build e2e

package main

// Builds for end-to-end tests listen, when MYGO_AUTOMATION names a socket,
// for another process to drive the window.
import _ "github.com/egoist/mygo/plugins/automation"
