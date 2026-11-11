// Package shell is what a file manager asks of the desktop it runs on:
// the icons and type names of files, opening them, copying, moving and
// deleting them as the system's own file manager does (with its progress,
// its questions about conflicts, and its trash), the system's context
// menu, and making this program the one that opens folders.
//
// This file is the whole of what the rest of the program sees. Each
// platform implements it in files of its own (shell_windows.go and the
// like); nothing outside this package calls the system for these.
//
// Unless said otherwise, functions may be called from any goroutine and
// block until done: call them off the main thread.
package shell

import (
	"errors"
	"image"

	"cairn/internal/fsys"
)

// ErrCanceled is returned when the user gave up an operation, as by
// closing its progress window or answering a question with Cancel.
var ErrCanceled = errors.New("shell: canceled")

// ErrUnsupported is returned by what a platform cannot do.
var ErrUnsupported = errors.ErrUnsupported

// Window is the native handle of the window that owns what an operation
// shows (an HWND on Windows), or 0 for none.
type Window uintptr

// Worker is a thread that calls the system for icons and the like: the
// calls of one worker run in order, on one thread set up as the system
// needs (a COM apartment on Windows). Several workers run side by side.
type Worker struct{ w worker }

// NewWorker starts a worker.
func NewWorker() *Worker { return &Worker{w: newWorker()} }

// Do runs fn on the worker's thread, after those queued before it, and
// returns at once.
func (w *Worker) Do(fn func()) { w.w.do(fn) }

// Close stops the worker once what is queued has run.
func (w *Worker) Close() { w.w.close() }

// Icon is the small icon of a file or a directory, as the system's file
// manager shows it, about px pixels wide (the nearest size the system
// keeps; the caller scales it).
//
// With byType, the file is not looked at: the icon is that of its type,
// by its name's extension and whether attr says it is a directory, which
// costs no disk access and works for files that do not exist. Without it
// the file itself is asked, for those with icons of their own (OwnIcon).
//
// Equal pictures are the same *image.RGBA: callers key their own caches
// by the pointer. The image is premultiplied RGBA and must not be
// changed. Call it on a Worker.
func Icon(path string, attr fsys.Attr, px int, byType bool) (*image.RGBA, error) {
	return icon(path, attr, px, byType)
}

// Thumbnail is a picture of a file's content, as the system's file
// manager shows pictures and videos, at most px pixels wide and high, in
// the file's proportions. Files the system makes no such picture of have
// none, and an error. The image is premultiplied RGBA. It reads the file,
// unless the system kept the picture from before: call it on a Worker,
// and not one that icons wait on.
func Thumbnail(path string, px int) (*image.RGBA, error) { return thumbnail(path, px) }

// OwnIcon reports whether an entry may have an icon of its own rather
// than its type's: programs, shortcuts, icon files, and directories that
// name one. It looks at the name and the attributes alone.
func OwnIcon(name string, attr fsys.Attr) bool { return ownIcon(name, attr) }

// TypeName is what the system calls files with an extension (with its
// dot, in lower case, or "" for none), as "Text Document", or directories.
// It looks at no file. Call it on a Worker.
func TypeName(ext string, dir bool) string { return typeName(ext, dir) }

// Open opens a file with the program for its type, or runs it, as a
// double click in the system's file manager does. dir is the directory
// the program starts in. What goes wrong is returned, and shown by
// nothing: the system's message boxes, and their sounds, stay away. For a
// file of a type no program opens, the system asks which should.
func Open(owner Window, path, dir string) error { return open(owner, path, dir) }

// OpenWith asks the user which program opens the file.
func OpenWith(owner Window, path string) error { return openWith(owner, path) }

// Properties shows the system's properties of files or directories.
func Properties(owner Window, paths []string) error { return properties(owner, paths) }

// Copy copies files and directories into a directory, with the system's
// progress window and its questions when names are taken. Copies made in
// the directory the sources are in get new names ("report - Copy.txt").
func Copy(owner Window, sources []string, destDir string) error {
	return transfer(owner, sources, destDir, false)
}

// Move moves files and directories into a directory, as Copy copies.
func Move(owner Window, sources []string, destDir string) error {
	return transfer(owner, sources, destDir, true)
}

// Delete moves files and directories to the trash, or with permanent
// deletes them for good. It does not ask whether to delete: the caller
// asks before deleting for good. The system still asks about what cannot
// go to the trash, as a file too large for it, rather than deleting it
// for good unasked.
func Delete(owner Window, paths []string, permanent bool) error {
	return remove(owner, paths, permanent)
}

// Rename gives a file or directory another name in its directory.
func Rename(owner Window, path, newName string) error { return rename(owner, path, newName) }

// NewFolder makes a directory in dir, named name or, when that is taken,
// name with a number as the system's file manager numbers them
// ("New folder (2)"), and returns its path.
func NewFolder(dir, name string) (string, error) { return newFolder(dir, name) }

// NewFile makes an empty file in dir, named as NewFolder names.
func NewFile(dir, name string) (string, error) { return newFile(dir, name) }

// MenuAction is something chosen in the system's context menu that the
// system leaves to the view the menu was shown for.
type MenuAction uint8

const (
	// MenuDone: nothing was chosen, or the system did what was.
	MenuDone MenuAction = iota
	// MenuRename: the user chose to rename what the menu is for, which
	// the caller does in place.
	MenuRename
)

// ContextMenu shows the system's own context menu for files or
// directories of one directory at a point of the screen, in pixels, or
// where the pointer is when x or y is negative, and runs what the user
// chooses, but what it returns for the caller to do. It returns once the
// menu closed. With no paths but a directory in background, the menu is
// that of the directory's background (New, Paste, …).
func ContextMenu(owner Window, paths []string, background string, x, y int) (MenuAction, error) {
	return contextMenu(owner, paths, background, x, y)
}

// OpenInSystem hands a place to the system's own file manager: one that
// is no directory, as the Recycle Bin, which this program cannot show.
func OpenInSystem(place string) error { return openInSystem(place) }

// Default is which requests to open folders come to this program rather
// than to the system's file manager.
type Default struct {
	// Folders: opening a folder from another program, the desktop or a
	// "show in folder" command.
	Folders bool
	// Shortcut: the system's key for its file manager (Win+E on
	// Windows), and starting the file manager with no folder.
	Shortcut bool
}

// DefaultFileManager reports what this program, at exe, is registered to
// open now.
func DefaultFileManager(exe string) Default { return defaultFileManager(exe) }

// SetDefaultFileManager registers the program at exe for what d says, and
// gives the rest back to the system's file manager as it was before. It
// changes the current user's settings alone, and needs no elevation.
func SetDefaultFileManager(exe string, d Default) error { return setDefaultFileManager(exe, d) }

// CutFormat is the clipboard format that says whether files on the
// clipboard were cut or copied, and CutData its content: what the
// system's file manager writes and reads, so that files cut here paste
// there as a move and the other way around.
const CutFormat = cutFormat

// CutData is the content of CutFormat for files cut, or copied.
func CutData(cut bool) []byte { return cutData(cut) }

// IsCut reads the content of CutFormat, and false when there is none.
func IsCut(data []byte) bool { return isCut(data) }

// Quiet turns off the sounds the system plays for this program on its
// own, where it can: on Windows, the click of navigation. Call it once,
// as the program starts.
func Quiet() { quiet() }
