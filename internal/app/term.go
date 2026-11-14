package app

import (
	"os"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/fsys"
	"myfiles/internal/ghostty"
	"myfiles/internal/workspace"
)

// terminalPath stands for a terminal where a session keeps the folders of
// its tabs, before the folder the terminal is in: no folder's path starts
// so.
const terminalPath = "::terminal:"

// termTab is what a tab that shows a terminal has in place of a folder.
type termTab struct {
	// dir is the folder the shell starts in.
	dir string
	// t is the terminal, nil until the tab first shows: a tab a session
	// brought back starts no shell before it is looked at. starting is set
	// while it is being made, and err is why there is none.
	t        *terminal.Terminal
	starting bool
	err      error
	// title is what the shell calls its window.
	title string
	// ref is the terminal's view, to give it the keyboard.
	ref ui.Handle
}

// termKey keys a terminal's view by its tab: two terminals shown in turn
// in a pane are two views.
type termKey struct{ id workspace.TabID }

// name is what a terminal's tab shows: what the shell calls its window,
// unless that is only the shell's own file, as PowerShell's is.
func (tt *termTab) name() string {
	title := strings.TrimSpace(tt.title)
	if title == "" || strings.HasSuffix(strings.ToLower(title), ".exe") {
		return locales.Terminal()
	}
	return title
}

// where is the folder the terminal is in: the one its shell says it went
// to, of shells that say, or else the one it started in.
func (tt *termTab) where() string {
	if tt.t != nil {
		if dir := tt.t.Dir(); dir != "" {
			return dir
		}
	}
	return tt.dir
}

// termDir is the folder a terminal opened from a tab starts in: the tab's,
// or the user's own for a tab at no folder, as the list of drives.
func termDir(t *Tab) string {
	if t != nil {
		if dir := t.place(); real(dir) && dir != fsys.Computer {
			return dir
		}
	}
	return fsys.Home()
}

// addTerminal makes a terminal's tab in a pane, after the tab showing
// there, with the keyboard.
func (w *Window) addTerminal(pane workspace.PaneID, dir string) workspace.TabID {
	a := w.app
	t := &Tab{ID: a.nextTab, cursor: -1, term: &termTab{dir: dir}, wantFocus: true}
	a.nextTab++
	w.tabs[t.ID] = t
	at := -1
	if p := w.layout.Pane(pane); p != nil && len(p.Tabs) > 0 {
		at = p.Active + 1
	}
	w.layout.AddTab(pane, t.ID, at)
	if p := w.layout.PaneOf(t.ID); p != nil {
		w.layout.Focus = p.ID
	}
	w.changed()
	return t.ID
}

// terminalUnder is the terminal in the pane right under a pane: the one
// showing there, or else the first of its tabs. nil when there is none.
func (w *Window) terminalUnder(p *workspace.Pane) *Tab {
	under := w.layout.Under(p.ID)
	if under == nil {
		return nil
	}
	if t := w.activeTab(under); t != nil && t.term != nil {
		return t
	}
	for _, id := range under.Tabs {
		if t := w.tabs[id]; t != nil && t.term != nil {
			return t
		}
	}
	return nil
}

// toggleTerminal goes between a folder and its terminal: from a folder to
// the terminal under it, which opens in the lower half of the folder's
// pane, in the folder, when there is none; and from a terminal back to
// the pane over it.
func (w *Window) toggleTerminal() {
	p, t := w.layout.Focused(), w.current()
	if p == nil || t == nil {
		return
	}
	if t.term != nil {
		over := w.layout.Over(p.ID)
		if over == nil {
			over = w.layout.Neighbor(false)
		}
		w.focusPane(over)
		return
	}
	if under := w.terminalUnder(p); under != nil {
		w.show(under.ID)
		return
	}
	w.layout.Split(p.ID, w.addTerminal(p.ID, termDir(t)), workspace.ZoneBottom)
	w.changed()
}

// newTerminal opens one more terminal: a tab beside the terminal with the
// keyboard, or beside the one under the folder with it, and where a
// folder has none under it, its first.
func (w *Window) newTerminal() {
	p, t := w.layout.Focused(), w.current()
	if p == nil || t == nil {
		return
	}
	if t.term != nil {
		w.addTerminal(p.ID, t.term.where())
		return
	}
	if under := w.terminalUnder(p); under != nil {
		w.show(under.ID)
		w.addTerminal(w.layout.PaneOf(under.ID).ID, termDir(t))
		return
	}
	w.toggleTerminal()
}

// folderTab is the tab a folder asked for from outside the panes, as by
// the sidebar, shows in: the one with the keyboard, or, from a terminal,
// the one over it or else any that shows a folder. nil when no tab does.
func (w *Window) folderTab() *Tab {
	t := w.current()
	if t == nil || t.term == nil {
		return t
	}
	if p := w.layout.Focused(); p != nil {
		if over := w.layout.Over(p.ID); over != nil {
			if t := w.activeTab(over); t != nil && t.term == nil && !t.settings {
				return t
			}
		}
	}
	for _, p := range w.layout.Panes() {
		if t := w.activeTab(p); t != nil && t.term == nil && !t.settings {
			return t
		}
	}
	return nil
}

// startTerminal makes a tab's terminal, whose shell starts once its view
// shows, at the view's size. The emulator is a library the program has in
// it, written to the disk before the first terminal of a copy that has
// none beside it: off the main thread.
func (w *Window) startTerminal(t *Tab) {
	tt := t.term
	tt.starting = true
	id := t.ID
	mine := func() bool { return w.tabs[id] == t && t.term == tt }
	go func() {
		err := ghostty.Ensure()
		w.update(func() {
			tt.starting = false
			if !mine() {
				return
			}
			if err != nil {
				tt.err = err
				return
			}
			dir := tt.dir
			if info, serr := os.Stat(dir); serr != nil || !info.IsDir() {
				// Gone since, as the folder of a tab a session brought back.
				dir = fsys.Home()
			}
			var term *terminal.Terminal
			term, tt.err = terminal.New(terminal.Options{
				Command: w.app.termCommand,
				Dir:     dir,
				OnTitle: func(title string) {
					w.update(func() {
						if mine() && tt.title != title {
							tt.title = title
							w.changed()
						}
					})
				},
				OnExit: func(code int) {
					w.update(func() {
						if !mine() || tt.t != term {
							return
						}
						if code < 0 {
							// The shell never ran: the terminal says why.
							return
						}
						// A shell that ended takes its tab with it.
						w.closeTab(id)
					})
				},
			})
			if tt.err == nil {
				tt.t = term
				t.wantFocus = true
			}
		})
	}()
}

// stopTerminal ends a tab's shell.
func (t *Tab) stopTerminal() {
	if t.term != nil && t.term.t != nil {
		term := t.term.t
		t.term.t = nil
		_ = term.Close()
	}
}

// terminalPage builds what a pane shows of a terminal's tab: the terminal,
// filling the pane under its tabs.
func (w *Window) terminalPage(c *ui.Context, p *workspace.Pane, t *Tab, focused bool) {
	th := c.Theme()
	tt := t.term
	if tt.t == nil && tt.err == nil && !tt.starting {
		w.startTerminal(t)
	}
	body := ui.Column(c.Key(termKey{t.ID})).Grow(1).MinHeight(0).MinWidth(0)
	zone := w.tabZone(body, p)
	body.Children(func() {
		switch {
		case tt.t != nil:
			terminal.View(c, tt.t).Grow(1).MinHeight(0).MinWidth(0).Bind(&tt.ref)
			if t.wantFocus && focused {
				tt.ref.Focus(c)
				t.wantFocus = false
			}
		case tt.err != nil:
			ui.Column(c).Grow(1).Center().Gap(6).Padding(16).Children(func() {
				ui.Text(c, locales.TerminalFailed()).Bold()
				ui.Text(c, tt.err.Error()).TextColor(th.TextMuted).Wrap().TextAlign(ui.Center)
			})
		}
		if zone != workspace.ZoneNone {
			w.zonePreview(c, zone)
		}
	})
}
