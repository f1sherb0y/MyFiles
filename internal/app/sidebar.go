package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"myfiles/internal/fsys"
)

// sidebarRow is the height of a row of the sidebar.
const sidebarRow = 24

// sidebar builds the list of places and drives beside the panes. A click
// shows one in the tab with the focus, a middle click in a tab of its
// own, and files dropped on one go into it.
func (w *Window) sidebar(c *ui.Context) {
	th := c.Theme()
	if c.WindowActive() && time.Since(w.sidebarAt) > 15*time.Second {
		// Drives come and go, and fill up.
		w.loadSidebar()
	}
	cur := w.current()
	at := ""
	if cur != nil {
		at = cur.Path
	}
	ui.Column(c.Key("sidebar")).Grow(1).MinWidth(0).MinHeight(0).Background(th.Surface).Children(func() {
		// Along the top of the window, the sidebar is its title bar too.
		bar := c.TitleBar()
		ui.Row(c.Key("title")).Height(stripHeight).Shrink(0).DragWindow().Padding(0, 10, 0, max(bar.Left, 10)).Children(func() {
			ui.Text(c, "MyFiles").FontWeight(600).TextColor(th.TextMuted).SingleLine()
		})
		ui.Scroll(c.Key("places")).Grow(1).MinHeight(0).Padding(2, 4, 8, 4).Children(func() {
			w.place(c, fsys.Computer, "This PC", "", at)
			for i := range w.places {
				p := &w.places[i]
				w.place(c, p.Path, p.Name, "", at)
			}
			if len(w.volumes) > 0 {
				ui.Text(c, "Drives").FontSize(th.FontSize-1).FontWeight(600).TextColor(th.TextMuted).Padding(10, 8, 3, 8)
			}
			for i := range w.volumes {
				v := &w.volumes[i]
				free := ""
				if v.Ready && v.Total > 0 {
					free = formatSize(v.Free)
				}
				w.place(c, v.Root, v.Title(), free, at)
			}
		})
	})
}

// place builds a row of the sidebar. note is shown after the name, as a
// drive's free space.
func (w *Window) place(c *ui.Context, path, name, note, current string) {
	th := c.Theme()
	here := fsys.SameName(path, current)
	row := ui.Row(c.Key(path)).Height(sidebarRow).Shrink(0).Padding(0, 8).Gap(7).Radius(4).
		Role(ui.RoleButton).Label(name).Tooltip(placeTip(path, note))
	switch {
	case here:
		row.Background(th.Background.Mix(th.Accent, 0.22))
	default:
		row.Draw(func(p *ui.Painter, r ui.Rect) {
			if row.Hovered() {
				p.Fill(r, th.SurfaceHover, 4)
			}
		})
	}
	if row.Clicked() {
		if t := w.current(); t != nil {
			w.navigate(t, path, navPush, "")
		}
	}
	if b, ok := row.PressedButton(); ok && b == 2 {
		if p := w.layout.Focused(); p != nil {
			pane := p.ID
			w.defer_(func() { w.newTab(pane, path, "") })
		}
	}
	if path != fsys.Computer && w.takeFiles(row, path) {
		row.Background(th.Accent.Alpha(0.3))
	}
	row.Children(func() {
		if path == fsys.Computer {
			ui.Icon(c, icoComputer).FontSize(15).Shrink(0)
		} else if icon, _ := w.app.icons.forPath(path, fsys.AttrDir); icon != nil {
			ui.Image(c, icon).Size(iconSize, iconSize).Shrink(0)
		} else {
			ui.Box(c).Size(iconSize, iconSize).Shrink(0)
		}
		ui.Text(c, name).SingleLine().Grow(1).MinWidth(0)
		if note != "" {
			ui.Text(c, note).SingleLine().FontSize(th.FontSize - 1).TextColor(th.TextMuted).Shrink(0)
		}
	})
}

func placeTip(path, note string) string {
	if path == fsys.Computer {
		return "This PC"
	}
	if note != "" {
		return path + "  ·  " + note + " free"
	}
	return path
}
