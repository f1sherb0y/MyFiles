//go:build !windows

package shell

import (
	"image"
	"sync"

	"myfiles/internal/fsys"
)

// On the platforms the package has not been written for yet, a worker is
// a plain goroutine and everything else fails with ErrUnsupported.

// worker runs the functions queued for it in order, on one goroutine.
type worker struct {
	q *queue
}

type queue struct {
	mu     sync.Mutex
	wake   sync.Cond
	fns    []func()
	closed bool
}

func newWorker() worker {
	q := &queue{}
	q.wake.L = &q.mu
	go q.loop()
	return worker{q}
}

func (w worker) do(fn func()) {
	w.q.mu.Lock()
	defer w.q.mu.Unlock()
	if !w.q.closed {
		w.q.fns = append(w.q.fns, fn)
		w.q.wake.Signal()
	}
}

func (w worker) close() {
	w.q.mu.Lock()
	defer w.q.mu.Unlock()
	w.q.closed = true
	w.q.wake.Signal()
}

func (q *queue) loop() {
	for {
		q.mu.Lock()
		for len(q.fns) == 0 && !q.closed {
			q.wake.Wait()
		}
		fns := q.fns
		q.fns = nil
		q.mu.Unlock()
		if len(fns) == 0 {
			return
		}
		for _, fn := range fns {
			fn()
		}
	}
}

func icon(path string, attr fsys.Attr, px int, byType bool) (*image.RGBA, error) {
	return nil, ErrUnsupported
}

func ownIcon(name string, attr fsys.Attr) bool { return false }

func typeName(ext string, dir bool) string { return "" }

func open(owner Window, path, dir string) error { return ErrUnsupported }

func apps(path string, all bool) []App { return nil }

func openIn(owner Window, path string, app App) error { return ErrUnsupported }

func setDefaultApp(path string, app App) error { return ErrUnsupported }

func properties(owner Window, paths []string) error { return ErrUnsupported }

func transfer(owner Window, sources []string, destDir string, move bool) error {
	return ErrUnsupported
}

func remove(owner Window, paths []string, permanent bool) error { return ErrUnsupported }

func rename(owner Window, path, newName string) error { return ErrUnsupported }

func newFolder(dir, name string) (string, error) { return "", ErrUnsupported }

func newFile(dir, name string) (string, error) { return "", ErrUnsupported }

func contextMenu(Window, []string, string, int, int) (MenuAction, error) {
	return MenuDone, ErrUnsupported
}

type systemMenu struct{ items []MenuItem }

func loadMenu(Window, []string, string) (*SystemMenu, error) { return nil, ErrUnsupported }

func (s *systemMenu) sub(int) []MenuItem { return nil }

func (s *systemMenu) invoke(int) (MenuAction, error) { return MenuDone, ErrUnsupported }

func (s *systemMenu) close() {}

func openInSystem(string) error { return ErrUnsupported }

const placeRecycleBin, placeComputer, placeNetwork, placeLinux = "::trash", "::computer", "::network", "::linux"

func trash() ([]TrashItem, error)             { return nil, ErrUnsupported }
func restoreTrash([]string) ([]string, error) { return nil, ErrUnsupported }
func deleteTrash([]string) error              { return ErrUnsupported }
func emptyTrash(Window) error                 { return ErrUnsupported }

func displayName(string) string       { return "" }
func quickAccess() []QuickFolder      { return nil }
func distros() []string               { return nil }
func computers() []string             { return nil }
func shares(string) ([]string, error) { return nil, ErrUnsupported }

func thumbnail(string, int) (*image.RGBA, error) { return nil, ErrUnsupported }

func defaultFileManager(exe string) Default { return Default{} }

func setDefaultFileManager(exe string, d Default) error { return ErrUnsupported }

// There is no marker of a cut on the clipboard, and no sound to turn off.

const cutFormat = ""

func cutData(cut bool) []byte { return nil }

func isCut(data []byte) bool { return false }

func quiet() {}

func trashFolders() []string { return nil }
