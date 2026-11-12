package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"myfiles/internal/config"
	"myfiles/internal/shell"
	"myfiles/internal/watch"
	"myfiles/internal/workspace"
)

// fakeFrame stands in for a native window: what goroutines ask it to run
// on the main thread waits in a queue the test runs.
type fakeFrame struct {
	mu     sync.Mutex
	queue  []func()
	title  string
	closed bool
}

func (f *fakeFrame) Update(fn func()) {
	f.mu.Lock()
	f.queue = append(f.queue, fn)
	f.mu.Unlock()
}
func (f *fakeFrame) Invalidate()                  { f.Update(func() {}) }
func (f *fakeFrame) SetTitle(title string)        { f.title = title }
func (f *fakeFrame) NativeHandle() uintptr        { return 0 }
func (f *fakeFrame) Close()                       { f.closed = true }
func (f *fakeFrame) IsMinimized() bool            { return false }
func (f *fakeFrame) Restore()                     {}
func (f *fakeFrame) Show()                        {}
func (f *fakeFrame) Focus()                       {}
func (f *fakeFrame) CapturePage() ([]byte, error) { return nil, nil }

// take returns what waits in the queue, and empties it.
func (f *fakeFrame) take() []func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.queue
	f.queue = nil
	return q
}

// fakeDesk is a clipboard in memory, and a main thread that is the
// test's.
type fakeDesk struct {
	frame   *fakeFrame
	data    transfer.Data
	text    string
	locale  string
	bin     []shell.TrashItem
	quick   []shell.QuickFolder
	linux   []string
	network []string
	// newer is the version an update check finds, "" for none.
	newer    string
	released func()

	mu       sync.Mutex
	launched []string
	// menus counts the system's menus read.
	menus int
	// gone is the ID of a program that is no longer installed.
	gone       string
	defaultErr error
	defaults   []string
}

func (d *fakeDesk) post(fn func()) { d.frame.Update(fn) }
func (d *fakeDesk) write(data transfer.Data, released func()) error {
	if r := d.released; r != nil {
		d.released = nil
		r()
	}
	d.data, d.released, d.text = data.Snapshot(), released, ""
	return nil
}
func (d *fakeDesk) read(formats ...transfer.Format) (transfer.Data, error) {
	return d.data.Materialize(formats)
}
func (d *fakeDesk) formats() []transfer.Format { return d.data.Formats() }
func (d *fakeDesk) writeText(s string) {
	_ = d.write(transfer.TextData(s), nil)
	d.text = s
}
func (d *fakeDesk) appearance(string)    {}
func (d *fakeDesk) languages() []string  { return []string{"en", "zh-Hans"} }
func (d *fakeDesk) chosenLocale() string { return d.locale }
func (d *fakeDesk) setLocale(tag string) { d.locale = tag }

// Nothing of the system's shows in a test, and no program starts: what
// would have is noted.
func (d *fakeDesk) open(_ shell.Window, path, _ string) error {
	if strings.HasSuffix(path, ".pic") {
		// A type no program is set to open.
		d.note("ask " + path)
		return shell.ErrChoose
	}
	d.note("open " + path)
	return nil
}
func (d *fakeDesk) apps(path string, all bool) []shell.App {
	var apps []shell.App
	switch {
	case strings.HasSuffix(path, ".txt"):
		apps = []shell.App{{Name: "Notes", ID: "notes.exe"}, {Name: "Editor", ID: "editor.exe"}}
	case strings.HasSuffix(path, ".pic"):
		apps = []shell.App{{Name: "Viewer", ID: "viewer.exe"}, {Name: "Painter", ID: "painter.exe"}}
	}
	if all {
		apps = append(apps, shell.App{Name: "Browser", ID: "browser.exe"})
	}
	return apps
}
func (d *fakeDesk) menuIcon([]byte) (ui.MenuIcon, error) { return nil, nil }

// loadMenu is a system's menu of a few items, as other programs add
// them, which notes what is run.
func (d *fakeDesk) loadMenu(_ shell.Window, paths []string, background string) (systemItems, error) {
	d.mu.Lock()
	d.menus++
	d.mu.Unlock()
	return &fakeMenu{d: d, of: strings.Join(paths, ", ") + background}, nil
}

type fakeMenu struct {
	d  *fakeDesk
	of string
}

func (m *fakeMenu) Items() []shell.MenuItem {
	return []shell.MenuItem{
		{Text: "Open", ID: 1, Verb: "open"},
		{Separator: true},
		{Text: "Compress", Sub: 1},
		{Text: "Scan for Viruses", ID: 2},
		{Separator: true},
		{Text: "Delete", ID: 3, Verb: "delete"},
		{Text: "Rename", ID: 4, Verb: "rename"},
	}
}
func (m *fakeMenu) Submenu(sub int) []shell.MenuItem {
	return []shell.MenuItem{{Text: "Add to Archive", ID: 10}, {Text: "Extract Here", ID: 11, Disabled: true}}
}
func (m *fakeMenu) Invoke(id int) (shell.MenuAction, error) {
	m.d.note("command " + strconv.Itoa(id) + " of " + m.of)
	return shell.MenuDone, nil
}
func (m *fakeMenu) Close() {}
func (d *fakeDesk) openIn(_ shell.Window, path string, app shell.App) error {
	d.mu.Lock()
	gone := app.ID == d.gone
	d.mu.Unlock()
	if gone {
		return shell.ErrChoose
	}
	d.note("open " + path + " in " + app.ID)
	return nil
}

func (d *fakeDesk) setDefaultApp(path string, app shell.App) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.defaults = append(d.defaults, path+" in "+app.ID)
	return d.defaultErr
}

func (d *fakeDesk) defaultCalls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.defaults)
}
func (d *fakeDesk) properties(_ shell.Window, paths []string) error {
	d.note("properties " + strings.Join(paths, ", "))
	return nil
}
func (d *fakeDesk) systemMenu(_ shell.Window, paths []string, background string) (shell.MenuAction, error) {
	d.note("menu " + strings.Join(paths, ", ") + background)
	return shell.MenuDone, nil
}
func (d *fakeDesk) openInSystem(place string) error {
	d.note("system " + place)
	return nil
}

func (d *fakeDesk) note(what string) {
	d.mu.Lock()
	d.launched = append(d.launched, what)
	d.mu.Unlock()
}

// launches returns what would have been shown or started, and forgets
// it.
func (d *fakeDesk) launches() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	l := d.launched
	d.launched = nil
	return l
}

// harness is a window of the app in a test: a view without a native
// window, in a folder of its own.
type harness struct {
	t    *testing.T
	a    *App
	w    *Window
	tt   *ui.Tester
	f    *fakeFrame
	desk *fakeDesk
	// root is the folder the test's files are in.
	root string
	// presented, when set, runs after each frame is drawn.
	presented func()
}

// newHarness makes a folder holding the entries named (those ending in a
// slash are directories, and may hold others, as "docs/a.txt"), and a
// window showing it.
func newHarness(t *testing.T, entries ...string) *harness {
	t.Helper()
	root := t.TempDir()
	// The folder as the system names it, without short names.
	if long, err := filepath.EvalSymlinks(root); err == nil {
		root = long
	}
	for _, e := range entries {
		path := filepath.Join(root, filepath.FromSlash(e))
		if e[len(e)-1] == '/' {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(e), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeFrame{}
	desk := &fakeDesk{frame: f}
	a := &App{set: config.Defaults(), nextTab: 1, desk: desk, ephemeral: true}
	a.set.Sidebar = false
	a.side = newPlaces(&a.set, nil)
	a.hub = watch.New()
	t.Cleanup(a.hub.Close)
	a.icons = newIcons(1, desk.post, func() {}, nil)
	w := a.window(nil)
	w.attach(f)
	h := &harness{t: t, a: a, w: w, f: f, desk: desk, root: root}
	w.newTab(w.layout.Focus, root, "")
	h.tt = ui.NewTester(func(c *ui.Context) {
		if h.presented != nil {
			c.OnPresented(h.presented)
		}
		w.view(c)
	}, 900, 600)
	h.settle()
	t.Cleanup(func() {
		if l := desk.launches(); len(l) > 0 {
			t.Errorf("the test would have shown or started: %q", l)
		}
		for _, tab := range w.tabs {
			w.release(tab)
		}
		// Let the watches let go of the folder before it is removed.
		time.Sleep(30 * time.Millisecond)
	})
	return h
}

// settle runs what goroutines asked of the main thread, and frames, until
// nothing more comes: folders read, operations done, icons fetched.
func (h *harness) settle() {
	h.t.Helper()
	quiet := 0
	for i := 0; i < 400 && quiet < 3; i++ {
		q := h.f.take()
		for _, fn := range q {
			fn()
		}
		busy := len(q) > 0
		for _, t := range h.w.tabs {
			busy = busy || t.loading || t.reload
		}
		if busy {
			quiet = 0
		} else {
			quiet++
		}
		h.tt.Frame()
		if quiet < 3 {
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// wait settles until cond holds, failing the test when it does not within
// a few seconds: for what a file operation does on another thread.
func (h *harness) wait(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s; rows %v", what, h.rows(h.w.current()))
		}
		h.settle()
		time.Sleep(20 * time.Millisecond)
	}
	h.settle()
}

// rows lists the names a tab shows, in order.
func (h *harness) rows(t *Tab) []string {
	names := make([]string, len(t.view))
	for i := range t.view {
		names[i] = t.entry(i).Name
	}
	return names
}

// center is the middle of the element showing a text or label.
func (h *harness) center(text string) (x, y float32) {
	h.t.Helper()
	r, ok := h.tt.Find(text)
	if !ok {
		h.t.Fatalf("no %q on screen; texts %q", text, h.tt.Texts())
	}
	return r.X + r.W/2, r.Y + r.H/2
}

// click clicks the element showing a text.
func (h *harness) click(text string) {
	h.t.Helper()
	x, y := h.center(text)
	h.tt.ClickAt(x, y)
	h.settle()
}

// doubleClick double-clicks the element showing a text.
func (h *harness) doubleClick(text string) {
	h.t.Helper()
	x, y := h.center(text)
	h.tt.ClickAt(x, y)
	h.tt.ClickAt(x, y)
	h.settle()
}

// key presses a key.
func (h *harness) key(mods ui.Modifiers, key ui.Key) {
	h.tt.Key(mods, key)
	h.settle()
}

// drag presses at one point, moves through others, and lets go at the
// last.
func (h *harness) drag(points ...[2]float32) {
	h.tt.Press(points[0][0], points[0][1])
	for _, p := range points[1:] {
		h.tt.Move(p[0], p[1])
	}
	last := points[len(points)-1]
	h.tt.Release(last[0], last[1])
	h.settle()
}

// exists reports whether a path under the root exists.
func (h *harness) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(rel)))
	return err == nil
}

// path is a path under the root.
func (h *harness) path(rel string) string {
	return filepath.Join(h.root, filepath.FromSlash(rel))
}

// panes lists the tabs of each pane, by the names of their folders.
func (h *harness) panes() [][]string {
	var out [][]string
	for _, p := range h.w.layout.Panes() {
		var names []string
		for _, id := range p.Tabs {
			names = append(names, h.w.tabs[id].title())
		}
		out = append(out, names)
	}
	return out
}

// open opens a folder under the root in a new tab of the focused pane.
func (h *harness) open(rel string) workspace.TabID {
	id := h.w.newTab(h.w.layout.Focus, h.path(rel), "")
	h.settle()
	return id
}

// update finds the version the test has newer, and notes its install.
func (d *fakeDesk) update(context.Context) (string, func(context.Context) error, error) {
	if d.newer == "" {
		return "", nil, nil
	}
	return d.newer, func(context.Context) error { d.note("install " + d.newer); return nil }, nil
}

func (d *fakeDesk) relaunch() { d.note("relaunch") }

// The system a test sees has no Quick access and no network but what the
// test gives it.
func (d *fakeDesk) displayName(string) string        { return "" }
func (d *fakeDesk) quickAccess() []shell.QuickFolder { return d.quick }
func (d *fakeDesk) distros() []string                { return d.linux }
func (d *fakeDesk) computers() []string              { return d.network }
func (d *fakeDesk) shares(computer string) ([]string, error) {
	return []string{"public"}, nil
}

// The Recycle Bin of a test holds what the test puts in it, and notes
// what is done there.
func (d *fakeDesk) trash() ([]shell.TrashItem, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.bin), nil
}
func (d *fakeDesk) restoreTrash(paths []string) ([]string, error) {
	var restored []string
	d.mu.Lock()
	for _, p := range paths {
		for _, it := range d.bin {
			if it.Path == p {
				restored = append(restored, filepath.Join(it.From, it.Name))
			}
		}
	}
	d.bin = slices.DeleteFunc(d.bin, func(it shell.TrashItem) bool { return slices.Contains(paths, it.Path) })
	d.mu.Unlock()
	d.note("restore " + strings.Join(paths, ", "))
	return restored, nil
}
func (d *fakeDesk) deleteTrash(paths []string) error {
	d.mu.Lock()
	d.bin = slices.DeleteFunc(d.bin, func(it shell.TrashItem) bool { return slices.Contains(paths, it.Path) })
	d.mu.Unlock()
	d.note("delete for good " + strings.Join(paths, ", "))
	return nil
}
func (d *fakeDesk) trashFolders() []string { return nil }
func (d *fakeDesk) emptyTrash(shell.Window) error {
	d.mu.Lock()
	d.bin = nil
	d.mu.Unlock()
	d.note("empty the bin")
	return nil
}
