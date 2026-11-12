package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/clock"
	"myfiles/internal/workspace"
)

// edges tells which edges of the window a pane touches: along the top
// its tab strip is the window's title bar, and in the top right corner it
// keeps clear of the window's buttons.
type edges struct{ top, left, right, bottom bool }

// splitKey keys the element of a split, apart from a pane's.
type splitKey struct{ n *workspace.Node }

// view builds the window.
func (w *Window) view(c *ui.Context) {
	a := w.app
	if !w.viewed {
		w.viewed = true
		mark("first view")
		c.OnPresented(func() { mark("first frame") })
	}
	w.building = true
	c.SetTheme(a.theme(c))
	// Menus are drawn in the window, in its theme and at its density,
	// rather than the system's, which also beep at keys they do not know.
	c.SetMenuPresentation(ui.PopoverMenus)
	w.shortcuts(c)
	bar := c.TitleBar()
	main := func() {
		ui.Column(c.Key("panes")).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			w.node(c, w.layout.Root, edges{top: true, right: true, bottom: true, left: !a.set.Sidebar}, bar)
		})
	}
	if a.set.Sidebar {
		width := a.set.SidebarWidth
		ui.Split(c.Key("side"), &width, func() { w.sidebar(c) }, main).Fill().SplitReset(168)
		if width != a.set.SidebarWidth {
			a.set.SidebarWidth = width
			a.saveSoon()
		}
	} else {
		ui.Row(c.Key("main")).Fill().AlignItems(ui.Stretch).Children(main)
	}
	w.dialogs(c)
	w.building = false

	if len(w.later) > 0 {
		// What changes the tabs and the folders they show runs now,
		// and the view builds again in this frame to show it.
		later := w.later
		w.later = nil
		for _, fn := range later {
			fn()
		}
		c.Rebuild()
	}
	if !w.statusUntil.IsZero() {
		if left := time.Until(w.statusUntil); left > 0 {
			c.After(left)
		} else {
			w.status, w.statusUntil = "", time.Time{}
		}
	}
}

// node builds a part of the layout: a pane, or two parts with a divider
// the user drags.
func (w *Window) node(c *ui.Context, n *workspace.Node, e edges, bar ui.TitleBar) {
	if n.Pane != nil {
		w.pane(c, n.Pane, e, bar)
		return
	}
	first, second := e, e
	if n.Stacked {
		second.top, first.bottom = false, false
	} else {
		first.right, second.left = false, false
	}
	share := n.Ratio * 100
	a := func() { w.node(c, n.A, first, bar) }
	b := func() { w.node(c, n.B, second, bar) }
	var s ui.Element
	if n.Stacked {
		s = ui.SplitVerticalPercent(c.Key(splitKey{n}), &share, a, b)
	} else {
		s = ui.SplitPercent(c.Key(splitKey{n}), &share, a, b)
	}
	s.Grow(1).MinWidth(0).MinHeight(0).SplitReset(50)
	if r := share / 100; r != n.Ratio && r > 0 && r < 1 {
		n.Ratio = r
		w.changed()
	}
}

// pane builds a pane: its tabs, and the folder of the one showing.
func (w *Window) pane(c *ui.Context, p *workspace.Pane, e edges, bar ui.TitleBar) {
	th := c.Theme()
	focused := w.layout.Focus == p.ID
	col := ui.Column(c.Key(p.ID)).Grow(1).MinWidth(0).MinHeight(0).Background(th.Background)
	// The pane the user presses in, or brings the keyboard into, is
	// the one the keys and new tabs go to.
	within := col.FocusWithin()
	entered := within && !w.within[p.ID]
	w.within[p.ID] = within
	if !focused && (col.PressedInside() || entered) {
		w.focusPane(p)
		focused = true
	}
	t := w.activeTab(p)
	col.Children(func() {
		w.strip(c, p, e, bar, focused)
		if t == nil {
			return
		}
		if t.settings {
			w.settingsPage(c)
			return
		}
		w.toolbar(c, p, t)
		ui.Divider(c)
		w.body(c, p, t, focused)
		ui.Divider(c)
		w.statusBar(c, t, e.left && e.bottom)
	})
}

// body builds what a pane shows of its tab's folder, and takes what is
// dropped on it: files, into the folder, and tabs, which split the pane
// or join it.
func (w *Window) body(c *ui.Context, p *workspace.Pane, t *Tab, focused bool) {
	th := c.Theme()
	body := ui.Column(c.Key("body")).Grow(1).MinHeight(0).MinWidth(0)

	zone := workspace.ZoneNone
	if d, over := ui.DragOver[tabDrag](body); over {
		if x, y, in := body.PointerPosition(); in {
			b := body.Bounds()
			zone = workspace.ZoneAt(x/max(b.W, 1), y/max(b.H, 1))
		}
		// A pane's only tab cannot split it, and its own tab dropped
		// on its middle goes nowhere.
		if src := w.layout.PaneOf(d.id); src == p && (len(p.Tabs) == 1 || zone == workspace.ZoneCenter) {
			zone = workspace.ZoneNone
		}
		w.zone, w.zonePane = zone, p.ID
	}
	if d, ok := ui.Drop[tabDrag](body); ok && w.zonePane == p.ID && w.zone != workspace.ZoneNone {
		id, pane, at := d.id, p.ID, w.zone
		w.defer_(func() {
			if w.layout.Split(pane, id, at) {
				if t := w.tabs[id]; t != nil {
					t.wantFocus = true
				}
				w.changed()
			}
		})
	}

	// Ctrl and the wheel go through the views, from the table to the
	// largest icons, as in the system's file manager.
	body.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputScroll || ev.Mods&ui.Ctrl == 0 || ev.DY == 0 {
			return false
		}
		next := int(t.mode) + 1
		if ev.DY > 0 {
			next = int(t.mode) - 1
		}
		if next >= 0 && next < int(viewModes) {
			w.setView(t, viewMode(next))
			w.invalidate()
		}
		return true
	})

	filesOver := false
	if real(t.Path) && t.err == nil {
		filesOver = w.takeFiles(body, t.Path)
	}
	body.Children(func() {
		switch {
		case t.err != nil:
			ui.Column(c).Grow(1).Center().Gap(6).Padding(16).Children(func() {
				ui.Text(c, locales.FolderUnreadable()).Bold()
				ui.Text(c, t.err.Error()).TextColor(th.TextMuted).Wrap().TextAlign(ui.Center)
			})
		default:
			w.files(c, p, t, focused)
		}
		if zone != workspace.ZoneNone {
			w.zonePreview(c, zone)
		}
	})
	if filesOver {
		body.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Stroke(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}, th.Accent, 3, 2)
		})
	}
}

// zonePreview shows where in the pane a dragged tab would go.
func (w *Window) zonePreview(c *ui.Context, zone workspace.Zone) {
	th := c.Theme()
	v := ui.Box(c.Key("zone")).Absolute().PassThrough().Radius(6).
		Background(th.Accent.Alpha(0.16)).Border(1.5, th.Accent.Alpha(0.7))
	const inset = 4
	v.Top(inset).Left(inset).Right(inset).Bottom(inset)
	switch zone {
	case workspace.ZoneLeft:
		v.RightPercent(50)
	case workspace.ZoneRight:
		v.LeftPercent(50)
	case workspace.ZoneTop:
		v.BottomPercent(50)
	case workspace.ZoneBottom:
		v.TopPercent(50)
	}
}

// statusBar tells what the folder holds and what is chosen.
func (w *Window) statusBar(c *ui.Context, t *Tab, corner bool) {
	th := c.Theme()
	if t.selStale {
		t.measure()
	}
	left := ""
	switch {
	case t.Path == "":
	case t.sel.Len() > 0:
		left = locales.StatusSelected(t.sel.Len())
		if files := t.sel.Len() - t.selDirs; files > 0 {
			left += "  ·  " + formatSize(t.selBytes)
		}
	default:
		left = locales.Items(len(t.view))
		if t.filter != "" {
			left = locales.StatusItemsOf(len(t.view), len(t.entries)-t.hidden)
		}
		if t.hidden > 0 {
			left += "  ·  " + locales.StatusHidden(t.hidden)
		}
	}
	right := ""
	switch {
	case w.status != "":
		right = w.status
	case t.loading && clock.Since(t.asked) > 150*time.Millisecond:
		right = locales.StatusReading()
	case t.shown > 0:
		right = formatDuration(t.shown)
	}
	if t.loading {
		// To say so, should the folder take long.
		c.After(160 * time.Millisecond)
	}
	ui.Row(c.Key("status")).Height(statusHeight).Shrink(0).Padding(0, 8).Gap(12).FontSize(th.FontSize - 1).Children(func() {
		if corner {
			// The window's bottom left, where the sidebar is not.
			w.updateButton(c)
		}
		ui.Text(c, left).SingleLine().TextColor(th.TextMuted).Shrink(0)
		ui.Spacer(c)
		color := th.TextMuted
		if w.status != "" {
			color = th.Danger
		}
		ui.Text(c, right).SingleLine().TextColor(color).MinWidth(0)
	})
}
