package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"cairn/internal/shell"
)

// frame is the native window a Window draws in: what it asks of it
// besides drawing. Tests give a Window one that shows nothing.
type frame interface {
	// Update runs fn on the main thread and draws the window again;
	// Invalidate only draws it again. Both may be called from any
	// goroutine.
	Update(fn func())
	Invalidate()
	SetTitle(title string)
	NativeHandle() uintptr
	Close()
	IsMinimized() bool
	Restore()
	Show()
	Focus()
	CapturePage() ([]byte, error)
}

// desktop is what the program asks of the system outside its windows.
// Tests replace it.
type desktop interface {
	// post runs fn on the main thread, without waiting for it.
	post(fn func())
	// write puts data on the clipboard; released runs once something
	// else took its place.
	write(data transfer.Data, released func()) error
	read(formats ...transfer.Format) (transfer.Data, error)
	formats() []transfer.Format
	writeText(s string)
	// appearance makes the program "light", "dark", or as the system.
	appearance(name string)

	// What follows shows something of the system's or starts another
	// program: tests must never do either.

	// open opens a file with its program, started in dir.
	open(owner shell.Window, path, dir string) error
	openWith(owner shell.Window, path string) error
	properties(owner shell.Window, paths []string) error
	// systemMenu shows the system's context menu where the pointer is.
	systemMenu(owner shell.Window, paths []string, background string) (shell.MenuAction, error)
	// openInSystem hands a place to the system's file manager.
	openInSystem(place string) error
}

// system is the desktop the program runs on.
type system struct{}

func (system) post(fn func()) { go mygo.RunOnMain(fn) }

func (system) write(data transfer.Data, released func()) error {
	return mygo.Clipboard.Write(data, mygo.ClipboardOptions{OnRelease: released})
}

func (system) read(formats ...transfer.Format) (transfer.Data, error) {
	return mygo.Clipboard.Read(formats...)
}

func (system) formats() []transfer.Format { return mygo.Clipboard.Formats() }

func (system) writeText(s string) { mygo.Clipboard.WriteText(s) }

func (system) open(owner shell.Window, path, dir string) error {
	return shell.Open(owner, path, dir)
}

func (system) openWith(owner shell.Window, path string) error { return shell.OpenWith(owner, path) }

func (system) properties(owner shell.Window, paths []string) error {
	return shell.Properties(owner, paths)
}

func (system) systemMenu(owner shell.Window, paths []string, background string) (shell.MenuAction, error) {
	// Negative coordinates: where the pointer is.
	return shell.ContextMenu(owner, paths, background, -1, -1)
}

func (system) openInSystem(place string) error { return shell.OpenInSystem(place) }

func (system) appearance(name string) {
	switch name {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}
