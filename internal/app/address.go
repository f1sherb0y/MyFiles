package app

import (
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"myfiles/internal/fsys"
	"myfiles/internal/workspace"
)

// toolbar builds the row under a pane's tabs: the way back, forward and
// up, the address, and the filter of the rows.
func (w *Window) toolbar(c *ui.Context, p *workspace.Pane, t *Tab) {
	ui.Row(c.Key("bar")).Height(barHeight).Shrink(0).Padding(0, 4).Gap(1).Children(func() {
		if iconButton(c, "back", icoBack, "Back (Alt+Left)").Disabled(len(t.back) == 0).Clicked() {
			w.goBack(t)
		}
		if iconButton(c, "forward", icoForward, "Forward (Alt+Right)").Disabled(len(t.forward) == 0).Clicked() {
			w.goForward(t)
		}
		if iconButton(c, "up", icoUp, "Up (Alt+Up)").Disabled(t.Path == fsys.Computer).Clicked() {
			w.up(t)
		}
		if iconButton(c, "refresh", icoRefresh, "Refresh (F5)").Clicked() {
			w.reload(t)
		}
		w.address(c, t)
		w.filterField(c, t)
		iconButton(c, "view", icoView, "View").Menu(func(m *ui.Menu) { w.viewMenu(m, t) })
	})
}

// crumb is a component of the address, with where it leads.
type crumb struct {
	name, path string
}

// crumbsOf lists the components of a path, from the list of drives down.
func crumbsOf(path string) []crumb {
	out := []crumb{{"This PC", fsys.Computer}}
	if path == fsys.Computer || path == "" {
		return out
	}
	for _, part := range fsys.Split(path) {
		out = append(out, crumb{part.Name, part.Path})
	}
	return out
}

// crumbWidth estimates the room a crumb takes, with the chevron before
// it: enough to tell how many fit, without measuring text every frame.
func crumbWidth(name string, fontSize float32) float32 {
	var w float32
	for _, r := range name {
		if r >= 0x2E80 {
			w += fontSize
		} else {
			w += fontSize * 0.53
		}
	}
	return w + 10 + 10
}

// address builds the address of the folder shown. Each component is a
// button that goes to its folder; a click beside them, or Ctrl+L, turns
// the address into a text to type, paste or copy.
func (w *Window) address(c *ui.Context, t *Tab) {
	th := c.Theme()
	box := ui.Row(c.Key("address")).Grow(1).Basis(0).MinWidth(60).Height(controlSize).MarginX(4).
		Radius(4).Background(th.Surface).Clip()
	if t.editing {
		box.Border(1, th.Accent)
		box.Children(func() {
			in := ui.TextInputBase(c.Key("input"), &t.addr).Grow(1).Height(controlSize-2).Padding(0, 6).
				Label("Address").AutoFocus()
			switch {
			case t.fresh:
				t.fresh = false
				in.Focus()
				in.SetTextSelection(0, utf8.RuneCountInString(t.addr))
			case in.Submitted():
				t.editing = false
				t.wantFocus = true
				w.goTo(t, t.addr)
			case in.Shortcut(0, ui.KeyEscape):
				t.editing = false
				t.wantFocus = true
			case in.FocusLeft():
				t.editing = false
			}
		})
		return
	}

	all := crumbsOf(t.Path)
	// The components that fit, the deepest first to stay: those left
	// out are behind an ellipsis. The room is the pane's address's in the
	// last frame, whichever tab showed then: a new tab's first frame
	// leaves out what its later ones do.
	first := 0
	if room := box.Bounds().W - 18; room > 0 {
		var used float32
		first = len(all) - 1
		for i := len(all) - 1; i >= 0; i-- {
			used += crumbWidth(all[i].name, th.FontSize)
			if used > room && i < len(all)-1 {
				break
			}
			first = i
		}
	}
	box.ContextMenu(func(m *ui.Menu) {
		if m.Item("Copy Address").Chosen() {
			w.copyText(t.tip())
		}
		if m.Item("Edit Address").Shortcut(ui.Cmd, ui.KeyL).Chosen() {
			w.editAddress(t)
		}
	})
	box.Children(func() {
		ui.Box(c).Width(2).Shrink(0)
		if first > 0 {
			hidden := all[:first]
			more := w.crumbButton(c, "more", "…", false)
			more.Menu(func(m *ui.Menu) {
				for i := len(hidden) - 1; i >= 0; i-- {
					if m.Item(hidden[i].name).ID(hidden[i].path).Chosen() {
						w.navigate(t, hidden[i].path, navPush, "")
					}
				}
			})
			ui.Icon(c, icoChevron).FontSize(10).TextColor(th.TextMuted).Shrink(0)
		}
		for i := first; i < len(all); i++ {
			part := all[i]
			if i > first {
				ui.Icon(c, icoChevron).FontSize(10).TextColor(th.TextMuted).Shrink(0)
			}
			last := i == len(all)-1
			b := w.crumbButton(c, part.path, part.name, last)
			if b.Clicked() {
				if last {
					w.reload(t)
				} else {
					// The folder left is chosen in the one above it.
					focus := ""
					if i+1 < len(all) {
						focus = all[i+1].name
						if part.path == fsys.Computer {
							focus = all[i+1].path
						}
					}
					w.navigate(t, part.path, navPush, focus)
				}
			}
			if btn, ok := b.PressedButton(); ok && btn == 2 {
				path := part.path
				if pane := w.layout.PaneOf(t.ID); pane != nil {
					id := pane.ID
					w.defer_(func() { w.newTab(id, path, "") })
				}
			}
			if part.path != fsys.Computer && w.takeFiles(b, part.path) {
				b.Background(th.Accent.Alpha(0.25))
			}
		}
		rest := ui.Box(c.Key("rest")).Grow(1).MinWidth(12).Height(controlSize).Cursor(ui.CursorText).Label("Edit address")
		if rest.Clicked() {
			w.editAddress(t)
		}
	})
}

// crumbButton is a component of the address.
func (w *Window) crumbButton(c *ui.Context, key any, name string, current bool) ui.Element {
	th := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Height(controlSize-4).Padding(0, 5).Radius(3).Shrink(0).Center().TabStop(false)
	if current {
		b.Shrink(1).MinWidth(20)
	}
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		switch {
		case b.Pressed():
			p.Fill(r, th.SurfacePressed, 3)
		case b.Hovered():
			p.Fill(r, th.SurfaceHover, 3)
		}
	})
	b.Children(func() {
		text := ui.Text(c, name).SingleLine()
		if current {
			text.FontWeight(600).MinWidth(0)
		}
	})
	return b
}

// editAddress turns the address into a text input holding the path.
func (w *Window) editAddress(t *Tab) {
	t.editing, t.fresh = true, true
	t.addr = t.Path
	if t.Path == fsys.Computer {
		t.addr = ""
	}
	w.invalidate()
}

// filterField is the field that narrows the rows to the names holding
// what is typed.
func (w *Window) filterField(c *ui.Context, t *Tab) {
	if t.Path == fsys.Computer {
		return
	}
	in := ui.TextInput(c.Key("filter"), &t.filter).Placeholder("Filter").Label("Filter").
		Width(150).Shrink(1).MinWidth(60).Height(controlSize).Padding(0, 6).TabStop(false).Bind(&t.filterRef)
	switch {
	case in.Shortcut(0, ui.KeyEscape):
		t.filter = ""
		t.wantFocus = true
	case in.Submitted(), in.Shortcut(0, ui.KeyDown):
		t.wantFocus = true
	}
}
