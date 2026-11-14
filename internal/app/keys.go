package app

import (
	"github.com/egoist/mygo/ui"

	"myfiles/internal/cli"
	"myfiles/internal/fsys"
	"myfiles/internal/workspace"
)

// shortcuts handles the keys of the window, wherever the focus is. Those
// of the rows of files, as Delete, are theirs (fileKeys).
func (w *Window) shortcuts(c *ui.Context) {
	a := w.app
	p, t := w.layout.Focused(), w.current()
	if p == nil {
		return
	}
	// Ctrl+` goes between a folder and the terminal under it, which it
	// opens; with Shift it opens one more.
	switch {
	case c.Shortcut(ui.Ctrl, ui.KeyBackquote):
		w.toggleTerminal()
	case c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyBackquote):
		w.newTerminal()
	case c.Shortcut(ui.Ctrl, ui.KeyTab):
		w.cycleTab(true)
	case c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab):
		w.cycleTab(false)
	}
	if t != nil && t.term != nil {
		// Every other key is the terminal's, for the program in it: a
		// shell has its own Ctrl+W, Ctrl+R and Ctrl+L. Ctrl+Shift+W closes
		// the tab, as in Windows Terminal.
		if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyW) {
			w.closeTab(t.ID)
		}
		return
	}
	switch {
	case c.Shortcut(ui.Cmd, ui.KeyT):
		path := fsys.Home()
		if t != nil && t.place() != "" {
			path = t.place()
		}
		w.newTab(p.ID, path, "")
	case c.Shortcut(ui.Cmd, ui.KeyN):
		a.open(&cli.Command{NewWindow: true}, false)
	case c.Shortcut(ui.Cmd|ui.Shift, ui.KeyT):
		if n := len(w.reopen); n > 0 {
			path := w.reopen[n-1]
			w.reopen = w.reopen[:n-1]
			w.newTab(p.ID, path, "")
		}
	case c.Shortcut(ui.Ctrl, ui.KeyPageDown):
		w.cycleTab(true)
	case c.Shortcut(ui.Ctrl, ui.KeyPageUp):
		w.cycleTab(false)
	case c.Shortcut(0, ui.KeyF6):
		w.focusPane(w.layout.Neighbor(true))
	case c.Shortcut(ui.Shift, ui.KeyF6):
		w.focusPane(w.layout.Neighbor(false))
	case c.Shortcut(ui.Cmd, ui.KeyB):
		a.set.Sidebar = !a.set.Sidebar
		a.saveSoon()
	case c.Shortcut(ui.Cmd, ui.KeyComma):
		w.openSettings()
	case c.Shortcut(ui.Cmd, ui.KeyH):
		w.setHidden(!a.set.ShowHidden)
	}
	if t == nil {
		return
	}
	// Ctrl+1 to Ctrl+9 show the pane's tabs by their place.
	for i, key := range [...]ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9} {
		if i < len(p.Tabs) && c.Shortcut(ui.Cmd, key) {
			w.show(p.Tabs[i])
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyW) || c.Shortcut(ui.Ctrl, ui.KeyF4) {
		w.closeTab(t.ID)
		return
	}
	if t.settings {
		// The rest are a folder's.
		return
	}
	switch {
	case c.Shortcut(ui.Cmd, ui.KeyL), c.Shortcut(ui.Alt, ui.KeyD), c.Shortcut(0, ui.KeyF4):
		w.editAddress(t)
	case c.Shortcut(ui.Alt, ui.KeyLeft), c.Shortcut(0, ui.KeyBack):
		w.goBack(t)
	case c.Shortcut(ui.Alt, ui.KeyRight), c.Shortcut(0, ui.KeyForward):
		w.goForward(t)
	case c.Shortcut(ui.Alt, ui.KeyUp):
		w.up(t)
	case c.Shortcut(0, ui.KeyF5), c.Shortcut(ui.Cmd, ui.KeyR):
		w.reload(t)
	case c.Shortcut(ui.Cmd, ui.KeyF), c.Shortcut(0, ui.KeyF3):
		w.setEverywhere(t, false)
	case c.Shortcut(ui.Cmd|ui.Shift, ui.KeyF):
		w.setEverywhere(t, true)
	case c.Shortcut(ui.Cmd|ui.Shift, ui.KeyN):
		w.newFolder(t)
	case c.Shortcut(ui.Cmd, ui.KeyBackslash):
		w.splitCurrent(workspace.ZoneRight)
	case c.Shortcut(ui.Cmd|ui.Shift, ui.KeyBackslash):
		w.splitCurrent(workspace.ZoneBottom)
	}
	// Ctrl+Shift+1 to 6 choose the view, as in the system's file manager.
	for mode, key := range [viewModes]ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6} {
		if c.Shortcut(ui.Cmd|ui.Shift, key) {
			w.setView(t, viewMode(mode))
		}
	}
}
