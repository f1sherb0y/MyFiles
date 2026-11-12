package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"myfiles/internal/shell"
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
	// apps lists the programs that open a file, off the main thread, and
	// menuIcon makes of a PNG picture an icon a menu's item shows.
	apps(path string) []shell.App
	menuIcon(png []byte) (ui.MenuIcon, error)
	// loadMenu reads the system's context menu, which it does not show,
	// off the main thread.
	loadMenu(owner shell.Window, paths []string, background string) (systemItems, error)

	// What follows shows something of the system's or starts another
	// program: tests must never do either.

	// open opens a file with its program, started in dir.
	open(owner shell.Window, path, dir string) error
	// openWith asks, in the system's dialog, which program opens a file,
	// and openIn opens it with one of those apps lists.
	openWith(owner shell.Window, path string) error
	openIn(owner shell.Window, path string, app shell.App) error
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

func (system) apps(path string) []shell.App { return shell.Apps(path) }

func (system) loadMenu(owner shell.Window, paths []string, background string) (systemItems, error) {
	menu, err := shell.LoadMenu(owner, paths, background)
	if err != nil {
		return nil, err
	}
	return menu, nil
}

func (system) menuIcon(png []byte) (ui.MenuIcon, error) {
	icon, err := mygo.NewMenuIcon(png, false)
	if err != nil {
		return nil, err
	}
	return icon, nil
}

func (system) openIn(owner shell.Window, path string, app shell.App) error {
	return shell.OpenIn(owner, path, app)
}

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
