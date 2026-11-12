package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"myfiles/internal/fsys"
	"myfiles/internal/workspace"
)

// tabDrag is a tab being dragged: to another place among the tabs, to
// another pane, or to an edge of one, which splits it.
type tabDrag struct{ id workspace.TabID }

// springDelay is how long files are held over a tab before it shows.
const springDelay = 600 * time.Millisecond

// strip builds a pane's row of tabs. Along the top of the window it is
// the title bar: its empty part moves the window.
func (w *Window) strip(c *ui.Context, p *workspace.Pane, e edges, bar ui.TitleBar, focused bool) {
	th := c.Theme()
	a := w.app
	strip := ui.Row(c.Key("strip")).Height(stripHeight).Shrink(0).AlignItems(ui.End).
		Background(th.Surface).PaddingStart(4)
	corner := e.top && e.right
	if corner {
		// Clear of the window's buttons.
		strip.PaddingEnd(bar.Right)
	}
	if e.top {
		strip.DragWindow()
		if e.left {
			strip.PaddingStart(max(bar.Left, 4))
		}
	}
	// A tab dropped past the last one goes to the end.
	if d, ok := ui.Drop[tabDrag](strip); ok {
		id, pane := d.id, p.ID
		w.defer_(func() {
			w.layout.MoveTab(id, pane, -1)
			w.changed()
		})
	}
	_, tabOver := ui.DragOver[tabDrag](strip)

	strip.Children(func() {
		for i, id := range p.Tabs {
			if t := w.tabs[id]; t != nil {
				w.tab(c, p, t, i, i == p.Active, focused)
			}
		}
		if tabOver {
			// Where it would go.
			ui.Box(c.Key("end")).Width(2).Height(stripHeight - 10).MarginY(3).Background(th.Accent).Radius(1)
		}
		ui.Row(c.Key("new")).Height(stripHeight-4).Padding(0, 3).Children(func() {
			if iconButton(c, "tab", icoPlus, "New tab (Ctrl+T)").Clicked() {
				path := fsys.Home()
				if cur := w.activeTab(p); cur != nil && cur.Path != "" {
					path = cur.Path
				}
				pane := p.ID
				w.defer_(func() { w.newTab(pane, path, "") })
			}
		})
		ui.Spacer(c)
		if corner {
			ui.Row(c.Key("window")).Height(stripHeight-4).Padding(0, 6).Gap(2).Children(func() {
				if iconButton(c, "sidebar", icoSidebar, "Sidebar (Ctrl+B)").Clicked() {
					a.set.Sidebar = !a.set.Sidebar
					a.saveSoon()
				}
				if iconButton(c, "settings", icoSettings, "Settings (Ctrl+,)").Clicked() {
					w.openSettings()
				}
			})
		}
	})
}

// tab builds a tab's header.
func (w *Window) tab(c *ui.Context, p *workspace.Pane, t *Tab, index int, active, focused bool) {
	th := c.Theme()
	id := t.ID
	tab := ui.Row(c.Key(id)).Height(stripHeight-4).Basis(190).Shrink(1).MinWidth(44).MaxWidth(220).
		Padding(0, 4, 0, 8).Gap(6).Radius(6, 6, 0, 0).Role(ui.RoleTab).Label(t.title()).Tooltip(t.tip())
	switch {
	case active:
		tab.Background(th.Background).TextColor(th.Text)
	default:
		tab.TextColor(th.TextMuted)
		tab.Draw(func(pt *ui.Painter, r ui.Rect) {
			if tab.Hovered() {
				pt.FillRadii(r, [4]float32{6, 6, 0, 0}, th.SurfaceHover)
			}
		})
	}
	if active && focused {
		// The tab the keyboard acts on, among those of every pane.
		tab.DrawOver(func(pt *ui.Painter, r ui.Rect) {
			pt.Fill(ui.Rect{X: r.X + 6, Y: r.Y, W: r.W - 12, H: 2}, th.Accent, 1)
		})
	}
	if tab.Pressed() && (!active || !focused) {
		w.show(id)
	}
	if b, ok := tab.PressedButton(); ok && b == 2 {
		w.defer_(func() { w.closeTab(id) })
	}
	tab.Drag(tabDrag{id})
	if tab.Dragging() {
		tab.Opacity(0.45)
	}

	// Another tab dropped on this one goes before it, or after it when
	// let go over its second half.
	if d, over := ui.DragOver[tabDrag](tab); over && d.id != id {
		after := false
		if x, _, in := tab.PointerPosition(); in {
			after = x > tab.Bounds().W/2
		}
		w.tabAfter = after
		tab.DrawOver(func(pt *ui.Painter, r ui.Rect) {
			x := r.X
			if after {
				x = r.X + r.W - 2
			}
			pt.Fill(ui.Rect{X: x, Y: r.Y + 3, W: 2, H: r.H - 6}, th.Accent, 1)
		})
	}
	if d, ok := ui.Drop[tabDrag](tab); ok && d.id != id {
		moved, pane, at := d.id, p.ID, index
		if w.tabAfter {
			at++
		}
		w.defer_(func() {
			w.layout.MoveTab(moved, pane, at)
			if t := w.tabs[moved]; t != nil {
				t.wantFocus = true
			}
			w.changed()
		})
	}

	// Files held over a tab show its folder, to be dropped in a folder
	// of it; let go on the tab, they go into its folder.
	if t.Path != "" && t.Path != fsys.Computer {
		if w.takeFiles(tab, t.Path) {
			tab.Background(th.Accent.Alpha(0.22))
			switch {
			case active:
			case w.spring != id:
				w.spring, w.springAt = id, c.Now()
				c.After(springDelay)
			case c.Now().Sub(w.springAt) >= springDelay:
				w.show(id)
			default:
				c.After(springDelay - c.Now().Sub(w.springAt))
			}
		} else if w.spring == id {
			w.spring = 0
		}
	}

	tab.ContextMenu(func(m *ui.Menu) { w.tabMenu(m, p, t) })
	tab.Children(func() {
		icon, _ := w.app.icons.forPath(t.Path, fsys.AttrDir)
		if t.settings {
			ui.Icon(c, icoSettings).FontSize(14).Shrink(0)
		} else if t.Path == fsys.Computer || t.Path == "" {
			ui.Icon(c, icoComputer).FontSize(14).Shrink(0)
		} else if icon != nil {
			ui.Image(c, icon).Size(iconSize, iconSize).Shrink(0)
		}
		ui.Text(c, t.title()).SingleLine().Grow(1).MinWidth(0)
		x := ui.ButtonBase(c.Key("close")).Size(18, 18).Shrink(0).Radius(4).Center().Label("Close tab").TabStop(false)
		x.Draw(func(pt *ui.Painter, r ui.Rect) {
			if x.Hovered() {
				pt.Fill(r, th.SurfacePressed, 4)
			}
		})
		x.Children(func() { ui.Icon(c, icoClose).FontSize(12) })
		if x.Clicked() {
			w.defer_(func() { w.closeTab(id) })
		}
	})
}

// tip is what resting the pointer on a tab shows: where it is.
func (t *Tab) tip() string {
	if t.settings {
		return "Settings"
	}
	if t.Path == fsys.Computer {
		return "This PC"
	}
	return t.Path
}

// tabMenu is the context menu of a tab's header.
func (w *Window) tabMenu(m *ui.Menu, p *workspace.Pane, t *Tab) {
	id, pane, path := t.ID, p.ID, t.Path
	if m.Item("New Tab").Shortcut(ui.Cmd, ui.KeyT).Chosen() {
		w.defer_(func() { w.newTab(pane, path, "") })
	}
	if m.Item("Duplicate Tab").Disabled(t.settings).Chosen() {
		w.defer_(func() { w.newTab(pane, path, "") })
	}
	m.Separator()
	alone := len(p.Tabs) < 2
	if m.Item("Split Right").Disabled(alone).Shortcut(ui.Cmd, ui.KeyBackslash).Chosen() {
		w.defer_(func() { w.layout.Split(pane, id, workspace.ZoneRight); w.changed() })
	}
	if m.Item("Split Down").Disabled(alone).Shortcut(ui.Cmd|ui.Shift, ui.KeyBackslash).Chosen() {
		w.defer_(func() { w.layout.Split(pane, id, workspace.ZoneBottom); w.changed() })
	}
	if m.Item("Split Left").Disabled(alone).Chosen() {
		w.defer_(func() { w.layout.Split(pane, id, workspace.ZoneLeft); w.changed() })
	}
	if m.Item("Split Up").Disabled(alone).Chosen() {
		w.defer_(func() { w.layout.Split(pane, id, workspace.ZoneTop); w.changed() })
	}
	m.Separator()
	if m.Item("Copy Path").Disabled(path == "").Chosen() {
		w.copyText(path)
	}
	m.Separator()
	if m.Item("Close Tab").Shortcut(ui.Cmd, ui.KeyW).Chosen() {
		w.defer_(func() { w.closeTab(id) })
	}
	if m.Item("Close Other Tabs").Disabled(alone).Chosen() {
		w.defer_(func() {
			if p := w.layout.Pane(pane); p != nil {
				for _, other := range append([]workspace.TabID(nil), p.Tabs...) {
					if other != id {
						w.closeTab(other)
					}
				}
			}
		})
	}
}
