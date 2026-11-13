package app

import (
	_ "embed"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"myfiles/internal/config"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
	"myfiles/internal/workspace"
)

// iconPNG is the program's icon, which its windows show in the taskbar
// (drawn by tools/mkicon).
//
//go:embed icon.png
var iconPNG []byte

// Window is a window: panes of tabs, and what its dialogs show.
type Window struct {
	app *App
	// win is the native window, nil until the window is shown; mu
	// guards it and pending, what goroutines asked to run before.
	win     frame
	mu      sync.Mutex
	pending []func()
	layout  *workspace.Layout
	tabs    map[workspace.TabID]*Tab
	// main is set for the window whose tabs the session keeps.
	main bool
	// reopen are the folders of the tabs closed, the last closed last.
	reopen []string

	// building is set while the view builds, and viewed once it has.
	building, viewed bool
	// later are functions to run once the view is built: what changes
	// the panes and tabs the view is looping over.
	later []func()

	// zone is where in a pane a dragged tab would go, and zonePane the
	// pane, as the pointer last was over one.
	zone     workspace.Zone
	zonePane workspace.PaneID
	// tabAfter tells that a tab dragged over another would go after it.
	tabAfter bool
	// within tells which panes held the keyboard focus in the last
	// frame, to tell when it comes into one.
	within map[workspace.PaneID]bool
	// sys is the system's menu of what the last context menu was for.
	sys *sysMenu

	// spring is the tab files are dragged over, and springAt since
	// when: held there, the tab shows, to drop into its folder.
	spring   workspace.TabID
	springAt time.Time

	// ask is the question in the window, nil for none.
	ask *question
	// pick is the question which program opens a file, while it shows.
	pick *chooser

	// places and volumes are what the sidebar lists, read off the main
	// thread; sidebarAt is when.
	volumes []fsys.Volume
	// linux tells that Linux distributions are installed, and pinAfter
	// that a pinned folder dragged over another goes after it.
	linux     bool
	pinAfter  bool
	sidebarAt time.Time

	// status is a message the status bars show for a while, as an
	// operation's error, until statusUntil.
	status      string
	statusUntil time.Time
}

// question is something asked over the window, with what to do on yes.
type question struct {
	open           bool
	title, message string
	confirm        string
	yes            func()
}

// update runs fn on the main thread and draws the window again, from
// any goroutine. Before the window has a native window to draw in, fn
// waits for it: folders are read while the window is being made.
func (w *Window) update(fn func()) {
	w.mu.Lock()
	win := w.win
	if win == nil {
		w.pending = append(w.pending, fn)
	}
	w.mu.Unlock()
	if win != nil {
		win.Update(fn)
	}
}

// invalidate draws the window again.
func (w *Window) invalidate() {
	w.mu.Lock()
	win := w.win
	w.mu.Unlock()
	if win != nil {
		win.Invalidate()
	}
}

// attach gives the window the native window it draws in, and runs what
// waited for it.
func (w *Window) attach(win frame) {
	w.mu.Lock()
	w.win = win
	pending := w.pending
	w.pending = nil
	w.mu.Unlock()
	for _, fn := range pending {
		win.Update(fn)
	}
}

// show gives a window made with window its native window, on screen.
// What the window shows first is there before it: its first frame has the
// folders asked for, where they are read in time.
func (a *App) show(w *Window) {
	opts := mygo.WindowOptions{
		Title:          "MyFiles",
		Width:          1180,
		Height:         720,
		TitleBarStyle:  mygo.TitleBarHidden,
		TitleBarHeight: stripHeight,
		Content:        ui.View(w.view),
	}
	if w.main && !a.ephemeral {
		opts.StateKey = "main"
	}
	win := mygo.NewWindow(opts)
	win.SetMinimumSize(420, 260)
	_ = win.SetIcon(iconPNG)
	win.OnFocus(func() {
		// The window used last is the one new folders open in.
		a.remove(w)
		a.windows = append(a.windows, w)
	})
	win.OnClosed(func() { w.closed() })
	w.attach(win)
}

// window makes a window's state, with the tabs of a session or none,
// before it has a native window to draw in.
func (a *App) window(session *config.Session) *Window {
	w := &Window{app: a, tabs: map[workspace.TabID]*Tab{}, main: len(a.windows) == 0}
	w.within = map[workspace.PaneID]bool{}
	w.layout = workspace.New()
	if session != nil {
		w.layout = session.Layout
		for _, st := range session.Tabs {
			if w.layout.PaneOf(st.ID) == nil {
				continue
			}
			t := &Tab{ID: st.ID, cursor: -1, mode: viewOf(st.View)}
			t.sort = ui.SortOrder{Column: a.set.SortBy, Descending: a.set.SortDescending}
			t.sorted = t.sort
			t.settings = st.Path == settingsPath
			w.tabs[t.ID] = t
			if st.ID >= a.nextTab {
				a.nextTab = st.ID + 1
			}
		}
	}
	a.windows = append(a.windows, w)
	w.start(session)
	return w
}

// closed lets go of what a window that closed had going.
func (w *Window) closed() {
	w.save()
	for _, t := range w.tabs {
		w.release(t)
	}
	w.app.remove(w)
}

// start reads the folders of a session's tabs, and the sidebar.
func (w *Window) start(session *config.Session) {
	if session != nil {
		for _, st := range session.Tabs {
			if t := w.tabs[st.ID]; t != nil && !t.settings {
				w.navigate(t, st.Path, navReplace, "")
			}
		}
	}
	w.loadSidebar()
}

// raise brings the window to the front.
func (w *Window) raise() {
	if w.win.IsMinimized() {
		w.win.Restore()
	}
	w.win.Show()
	w.win.Focus()
}

// handle is the window's native handle, for what the system shows over
// it.
func (w *Window) handle() shell.Window {
	if w.win == nil {
		return 0
	}
	return shell.Window(w.win.NativeHandle())
}

// defer_ runs fn once the view is built.
func (w *Window) defer_(fn func()) { w.later = append(w.later, fn) }

// activeTab is the tab a pane shows.
func (w *Window) activeTab(p *workspace.Pane) *Tab {
	id, ok := p.ActiveTab()
	if !ok {
		return nil
	}
	return w.tabs[id]
}

// current is the tab the keyboard acts on: the one showing in the pane
// with the focus.
func (w *Window) current() *Tab {
	p := w.layout.Focused()
	if p == nil {
		return nil
	}
	return w.activeTab(p)
}

// newTab opens a folder in a new tab of a pane, after the tab showing
// there, and shows it. choose names an entry to choose in it.
func (w *Window) newTab(pane workspace.PaneID, path, choose string) workspace.TabID {
	a := w.app
	if path == "" {
		// From a tab that shows no folder, as the settings.
		path = fsys.Home()
	}
	t := &Tab{ID: a.nextTab, cursor: -1, mode: viewOf(a.set.View)}
	a.nextTab++
	t.sort = ui.SortOrder{Column: a.set.SortBy, Descending: a.set.SortDescending}
	t.sorted = t.sort
	w.tabs[t.ID] = t
	at := -1
	if p := w.layout.Pane(pane); p != nil && len(p.Tabs) > 0 {
		at = p.Active + 1
	}
	w.layout.AddTab(pane, t.ID, at)
	if p := w.layout.PaneOf(t.ID); p != nil {
		w.layout.Focus = p.ID
	}
	w.navigate(t, path, navReplace, choose)
	w.changed()
	return t.ID
}

// closeTab closes a tab; the window closes with its last one.
func (w *Window) closeTab(id workspace.TabID) {
	t := w.tabs[id]
	if t == nil {
		return
	}
	if t.place() != "" {
		w.reopen = append(w.reopen, t.place())
	}
	w.release(t)
	delete(w.tabs, id)
	if !w.layout.RemoveTab(id) {
		w.win.Close()
		return
	}
	if cur := w.current(); cur != nil {
		cur.wantFocus = true
	}
	w.changed()
}

// release stops what a tab has going.
func (w *Window) release(t *Tab) {
	t.gen++
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	if t.find != nil && t.find.stop != nil {
		t.find.stop()
	}
	if t.unwatch != nil {
		t.unwatch()
		t.unwatch = nil
	}
}

// show makes a tab the one its pane shows, with the focus.
func (w *Window) show(id workspace.TabID) {
	p := w.layout.PaneOf(id)
	if p == nil {
		return
	}
	i := p.Index(id)
	if p.Active != i || w.layout.Focus != p.ID {
		p.Active = i
		w.layout.Focus = p.ID
		if t := w.tabs[id]; t != nil {
			t.wantFocus = true
		}
		w.changed()
	}
}

// focusPane gives a pane the keyboard.
func (w *Window) focusPane(p *workspace.Pane) {
	if p == nil || w.layout.Focus == p.ID {
		return
	}
	w.layout.Focus = p.ID
	if t := w.activeTab(p); t != nil {
		t.wantFocus = true
	}
	w.changed()
}

// cycleTab shows the tab after the one showing in the focused pane, or
// the one before it.
func (w *Window) cycleTab(forward bool) {
	p := w.layout.Focused()
	if p == nil || len(p.Tabs) < 2 {
		return
	}
	n := len(p.Tabs)
	i := (p.Active + 1) % n
	if !forward {
		i = (p.Active + n - 1) % n
	}
	w.show(p.Tabs[i])
}

// splitCurrent opens the folder showing in a new pane beside the focused
// one, or below it.
func (w *Window) splitCurrent(zone workspace.Zone) {
	p, t := w.layout.Focused(), w.current()
	if p == nil || t == nil || t.settings {
		return
	}
	id := w.newTab(p.ID, t.place(), "")
	w.layout.Split(p.ID, id, zone)
	w.changed()
}

// changed notes that the tabs or the panes changed: the title follows,
// and the session is written in a moment.
func (w *Window) changed() {
	if w.win == nil {
		return
	}
	if t := w.current(); t != nil && (t.Path != "" || t.settings) {
		w.win.SetTitle(t.title() + " - MyFiles")
	}
	if !w.building {
		// Changed from outside the view, as by another instance of the
		// program handing over a folder to show: drawn again.
		w.invalidate()
	}
	if !w.main || w.app.ephemeral {
		return
	}
	a := w.app
	a.saveMu.Lock()
	pending := a.saving
	a.saving = true
	a.saveMu.Unlock()
	if pending {
		return
	}
	time.AfterFunc(800*time.Millisecond, func() {
		a.desk.post(func() {
			a.saveMu.Lock()
			a.saving = false
			a.saveMu.Unlock()
			w.save()
		})
	})
}

// save writes the session: the main window's panes and tabs.
func (w *Window) save() {
	if !w.main || w.app.ephemeral || len(w.tabs) == 0 {
		return
	}
	s := config.Session{Layout: w.layout}
	for _, id := range w.layout.Tabs() {
		if t := w.tabs[id]; t != nil {
			path := t.place()
			if t.settings {
				path = settingsPath
			}
			s.Tabs = append(s.Tabs, config.Tab{ID: id, Path: path, View: viewSpecs[t.mode].id})
		}
	}
	_ = s.Save()
}

// say shows a message in the status bars for a few seconds.
func (w *Window) say(msg string) {
	w.status, w.statusUntil = msg, time.Now().Add(6*time.Second)
	w.invalidate()
}

// report shows an operation's error, unless the user gave it up.
func (w *Window) report(err error) {
	if err == nil || err == shell.ErrCanceled {
		return
	}
	w.update(func() { w.say(err.Error()) })
}

// loadSidebar reads the places and drives the sidebar lists, off the
// main thread: a drive that does not answer must not hold the window.
func (w *Window) loadSidebar() {
	w.sidebarAt = time.Now()
	go func() {
		volumes, linux := fsys.Volumes(), len(w.app.desk.distros()) > 0
		w.update(func() {
			w.volumes, w.linux = volumes, linux
			// The nodes of the tree that are open follow what they hold.
			for path := range w.app.side.open {
				w.app.readKids(path)
			}
		})
	}()
}
