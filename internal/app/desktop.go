package app

import (
	"context"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"

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
	// languages are the languages the program is written in, chosenLocale
	// the one chosen for it, "" while it follows the system's, and
	// setLocale chooses one, and keeps the choice.
	languages() []string
	chosenLocale() string
	setLocale(tag string)
	// apps lists the programs the system recommends for opening a file,
	// or all that open files, off the main thread, and menuIcon makes of
	// a PNG picture an icon a menu's item shows.
	apps(path string, all bool) []shell.App
	menuIcon(png []byte) (ui.MenuIcon, error)
	// What the sidebar shows of the system, read off the main thread:
	// what it calls a folder or a place, the folders of its file manager's
	// Quick access, the Linux distributions, the computers of the network
	// and the folders one shares.
	displayName(path string) string
	quickAccess() []shell.QuickFolder
	distros() []string
	computers() []string
	shares(computer string) ([]string, error)
	// What the Recycle Bin holds, and what is done there, off the main
	// thread: put back where it was, deleted for good, emptied.
	trash() ([]shell.TrashItem, error)
	// trashFolders are the folders the Recycle Bin keeps its items in,
	// one a drive, to watch for what other programs delete.
	trashFolders() []string
	restoreTrash(paths []string) ([]string, error)
	deleteTrash(paths []string) error
	emptyTrash(owner shell.Window) error
	// update looks for a newer version: its number, "" when there is
	// none or the program cannot update itself, and what downloads it and
	// puts it in place of this one, to run from the next start. relaunch
	// starts the program again.
	update(ctx context.Context) (version string, install func(context.Context) error, err error)
	relaunch()
	// loadMenu reads the system's context menu, which it does not show,
	// off the main thread.
	loadMenu(owner shell.Window, paths []string, background string) (systemItems, error)

	// What follows shows something of the system's or starts another
	// program: tests must never do either.

	// open opens a file with its program, started in dir, or returns
	// shell.ErrChoose for one no program is set to open; openIn opens
	// it with one of those apps lists.
	open(owner shell.Window, path, dir string) error
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

func (system) update(ctx context.Context) (string, func(context.Context) error, error) {
	if !mygo.Updater.Enabled() {
		return "", nil, nil
	}
	if dir := os.Getenv("MYFILES_UPDATE_DIR"); dir != "" {
		// For trying an update before it is published: the files a build
		// wrote, read from a folder. They are checked as the published
		// ones are, against the key built into the program.
		mygo.Updater.SetSource(updateDir(dir))
	}
	up, err := mygo.Updater.Check(ctx)
	if err != nil || up == nil {
		return "", nil, err
	}
	return up.Version, func(ctx context.Context) error { return up.Install(ctx, nil) }, nil
}

// updateDir serves a build's update files from the folder they are in.
type updateDir string

func (d updateDir) Manifest(context.Context, bool) (io.ReadCloser, error) {
	return os.Open(filepath.Join(string(d), "update-"+runtime.GOOS+"-"+runtime.GOARCH+".json"))
}

// Open reads the file a URL of the manifest names, by its last part.
func (d updateDir) Open(_ context.Context, url string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(string(d), path.Base(url)))
}

func (system) displayName(path string) string           { return shell.DisplayName(path) }
func (system) quickAccess() []shell.QuickFolder         { return shell.QuickAccess() }
func (system) distros() []string                        { return shell.Distros() }
func (system) computers() []string                      { return shell.Computers() }
func (system) shares(computer string) ([]string, error) { return shell.Shares(computer) }

func (system) trashFolders() []string                        { return shell.TrashFolders() }
func (system) trash() ([]shell.TrashItem, error)             { return shell.Trash() }
func (system) restoreTrash(paths []string) ([]string, error) { return shell.RestoreTrash(paths) }
func (system) deleteTrash(paths []string) error              { return shell.DeleteTrash(paths) }
func (system) emptyTrash(owner shell.Window) error           { return shell.EmptyTrash(owner) }

func (system) relaunch() { mygo.App.Relaunch() }

func (system) languages() []string  { return mygo.App.Languages() }
func (system) chosenLocale() string { return mygo.App.ChosenLocale() }
func (system) setLocale(tag string) { mygo.App.SetLocale(tag) }

func (system) apps(path string, all bool) []shell.App {
	if all {
		return shell.AllApps(path)
	}
	return shell.Apps(path)
}

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
