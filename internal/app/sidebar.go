package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
)

// sidebarRow is the height of a row of the sidebar, sidebarIndent how far
// a level of the tree is in from the one above, and sidebarKidsMax how
// many folders of one folder the tree lists.
const (
	sidebarRow     = 24
	sidebarIndent  = 12
	sidebarKidsMax = 500
)

// pinDrag is a pinned folder being dragged to another place among them.
type pinDrag struct{ path string }

// sideRow is a row of the sidebar.
type sideRow struct {
	// path is the folder, or the place of the system, the row is of.
	path, name string
	// note shows after the name, as a drive's free space.
	note string
	// depth is how deep in the tree the row is; arrow shows the row can
	// open, and open that it has.
	depth       int
	arrow, open bool
	// pinned and recent tell the rows of those lists from the tree's.
	pinned, recent bool
}

// sidebar builds what is beside the panes: the folders pinned, those used
// lately, and the tree of the drives, the network and the Linux
// distributions. A click shows a folder in the tab with the focus, a
// middle click in a tab of its own, and files dropped on one go into it.
func (w *Window) sidebar(c *ui.Context) {
	a := w.app
	th := c.Theme()
	if c.WindowActive() && time.Since(w.sidebarAt) > 15*time.Second {
		// Drives come and go, and fill up.
		w.loadSidebar()
	}
	cur := w.current()
	at := ""
	if cur != nil {
		at = cur.place()
	}
	ui.Column(c.Key("sidebar")).Grow(1).MinWidth(0).MinHeight(0).Background(th.Surface).Children(func() {
		// Along the top of the window, the sidebar is its title bar too.
		bar := c.TitleBar()
		ui.Row(c.Key("title")).Height(stripHeight).Shrink(0).DragWindow().Padding(0, 10, 0, max(bar.Left, 10)).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "MyFiles").FontWeight(600).TextColor(th.TextMuted).SingleLine().Shrink(0)
			// A newer version in place: beside the name, where it is seen.
			w.updateButton(c)
		})
		ui.Scroll(c.Key("places")).Grow(1).MinHeight(0).Padding(2, 4, 8, 4).Children(func() {
			// The folders every user has, then those the user chose.
			favorites := false
			for _, path := range a.set.Pinned {
				if a.ofSystem(path) {
					w.sideRow(c, sideRow{path: path, name: a.nameOf(path), pinned: true}, at)
				} else {
					favorites = true
				}
			}
			if favorites {
				w.sideHeader(c, locales.SidebarFavorites())
				for _, path := range a.set.Pinned {
					if !a.ofSystem(path) {
						w.sideRow(c, sideRow{path: path, name: a.nameOf(path), pinned: true}, at)
					}
				}
			}
			if recent := a.recentFolders(); len(recent) > 0 {
				w.sideHeader(c, locales.SidebarRecent())
				for _, path := range recent {
					w.sideRow(c, sideRow{path: path, name: a.nameOf(path), recent: true}, at)
				}
			}
			ui.Divider(c).MarginY(6)
			w.tree(c, fsys.Computer, 0, at)
			w.tree(c, shell.Network, 0, at)
			if w.linux {
				w.tree(c, shell.Linux, 0, at)
			}
		})
	})
}

// sideHeader titles a part of the sidebar.
func (w *Window) sideHeader(c *ui.Context, title string) {
	th := c.Theme()
	ui.Text(c, title).FontSize(th.FontSize-1).FontWeight(600).TextColor(th.TextMuted).Padding(10, 8, 3, 8)
}

// tree builds a node of the tree and, when it is open, what it holds.
func (w *Window) tree(c *ui.Context, path string, depth int, at string) {
	a := w.app
	row := sideRow{path: path, depth: depth, arrow: true, open: a.side.open[path]}
	switch {
	case path == fsys.Computer:
		row.name = locales.ThisPC()
	case strings.HasPrefix(path, "::"):
		row.name = a.nameOf(path)
		if row.name == "" && path == shell.Linux {
			row.name = "Linux"
		}
	default:
		row.name = treeName(path)
		if v := w.volume(path); v != nil {
			row.name = v.Title()
			if v.Ready && v.Total > 0 {
				row.note = formatSize(v.Free)
			}
		}
	}
	var kids *sideKids
	if row.open {
		kids = a.kidsOf(path)
		// Nothing to open in a folder without folders.
		row.arrow = !kids.loaded || len(kids.paths) > 0
	} else if k := a.side.kids[path]; k != nil && k.loaded && len(k.paths) == 0 && k.err == nil {
		row.arrow = false
	}
	w.sideRow(c, row, at)
	if kids == nil {
		return
	}
	for i, child := range kids.paths {
		if i == sidebarKidsMax {
			break
		}
		w.tree(c, child, depth+1, at)
	}
}

// treeName is what the tree calls a folder: its own name, a computer's
// without its slashes, and for what a computer shares, the share's.
func treeName(path string) string {
	if strings.HasPrefix(path, `\\`) {
		rest := strings.Trim(path[2:], `\`)
		if i := strings.LastIndexByte(rest, '\\'); i >= 0 {
			return rest[i+1:]
		}
		return rest
	}
	return fsys.Base(path)
}

// volume is the drive a path is the root of, nil for any other path.
func (w *Window) volume(path string) *fsys.Volume {
	for i := range w.volumes {
		if w.volumes[i].Root == path {
			return &w.volumes[i]
		}
	}
	return nil
}

// sideRow builds a row of the sidebar.
func (w *Window) sideRow(c *ui.Context, r sideRow, current string) {
	a := w.app
	th := c.Theme()
	path := r.path
	here := current != "" && fsys.SameName(path, current)
	// The same folder may show twice, pinned and in the tree.
	key := "tree:" + path
	switch {
	case r.pinned:
		key = "pin:" + path
	case r.recent:
		key = "recent:" + path
	}
	row := ui.Row(c.Key(key)).Height(sidebarRow).Shrink(0).Padding(0, 8, 0, 4+float32(r.depth)*sidebarIndent).Gap(5).Radius(4).
		Role(ui.RoleButton).Label(r.name).Tooltip(placeTip(path, r.note))
	switch {
	case here:
		row.Background(th.Background.Mix(th.Accent, 0.22))
	default:
		row.Draw(func(p *ui.Painter, rect ui.Rect) {
			if row.Hovered() {
				p.Fill(rect, th.SurfaceHover, 4)
			}
		})
	}
	opens := path == shell.Network || path == shell.Linux || isComputer(path)
	if row.Clicked() {
		switch {
		case opens:
			w.defer_(func() { a.toggle(path) })
		default:
			if t := w.current(); t != nil {
				w.navigate(t, path, navPush, "")
			}
		}
	}
	folder := path == fsys.Computer || path == shell.RecycleBin || holdsFolders(path)
	if b, ok := row.PressedButton(); ok && b == 2 && folder {
		if p := w.layout.Focused(); p != nil {
			pane := p.ID
			w.defer_(func() { w.newTab(pane, path, "") })
		}
	}
	if holdsFolders(path) && w.takeFiles(row, path) {
		row.Background(th.Accent.Alpha(0.3))
	}
	if r.pinned {
		w.reorderPin(c, row, path)
	}
	row.ContextMenu(func(m *ui.Menu) { w.sideMenu(m, r) })
	row.Children(func() {
		switch {
		case !r.arrow:
			if r.depth > 0 || !r.pinned && !r.recent {
				ui.Box(c).Size(14, 14).Shrink(0)
			}
		default:
			arrow := ui.ButtonBase(c.Key("arrow")).Size(14, 14).Shrink(0).Radius(3).Center().TabStop(false)
			icon := icoChevron
			if r.open {
				icon = icoChevronDown
			}
			arrow.Children(func() { ui.Icon(c, icon).FontSize(10).TextColor(th.TextMuted) })
			if arrow.Clicked() {
				w.defer_(func() { a.toggle(path) })
			}
		}
		iconPath := path
		if path == fsys.Computer {
			iconPath = shell.ThisPC
		}
		if icon, _ := a.icons.forPath(iconPath, fsys.AttrDir); icon != nil {
			ui.Image(c, icon).Size(iconSize, iconSize).Shrink(0)
		} else {
			ui.Box(c).Size(iconSize, iconSize).Shrink(0)
		}
		ui.Text(c, r.name).SingleLine().Grow(1).MinWidth(0)
		if r.note != "" {
			ui.Text(c, r.note).SingleLine().FontSize(th.FontSize - 1).TextColor(th.TextMuted).Shrink(0)
		}
	})
}

// reorderPin lets a pinned folder be dragged to another's place: before
// it, or after it when let go over its lower half.
func (w *Window) reorderPin(c *ui.Context, row ui.Element, path string) {
	a := w.app
	th := c.Theme()
	row.Drag(pinDrag{path})
	if row.Dragging() {
		row.Opacity(0.45)
	}
	// Among its own: the folders of the system, or the favorites.
	same := func(other string) bool { return other != path && a.ofSystem(other) == a.ofSystem(path) }
	if d, over := ui.DragOver[pinDrag](row); over && same(d.path) {
		after := false
		if _, y, in := row.PointerPosition(); in {
			after = y > row.Bounds().H/2
		}
		w.pinAfter = after
		row.DrawOver(func(p *ui.Painter, r ui.Rect) {
			y := r.Y
			if after {
				y = r.Y + r.H - 2
			}
			p.Fill(ui.Rect{X: r.X + 4, Y: y, W: r.W - 8, H: 2}, th.Accent, 1)
		})
	}
	if d, ok := ui.Drop[pinDrag](row); ok && same(d.path) {
		moved, after := d.path, w.pinAfter
		w.defer_(func() { a.movePin(moved, path, after) })
	}
}

// sideMenu is the context menu of a row of the sidebar.
func (w *Window) sideMenu(m *ui.Menu, r sideRow) {
	a := w.app
	path := r.path
	folder := holdsFolders(path)
	if folder || path == fsys.Computer {
		if m.Item(locales.MenuOpenNewTab()).Chosen() {
			if p := w.layout.Focused(); p != nil {
				pane := p.ID
				w.defer_(func() { w.newTab(pane, path, "") })
			}
		}
		m.Separator()
	}
	switch {
	case a.pinned(path):
		if m.Item(locales.SidebarUnpin()).Chosen() {
			w.defer_(func() { a.unpin(path) })
		}
	case folder:
		if m.Item(locales.SidebarPin()).Chosen() {
			w.defer_(func() { a.pin(path) })
		}
	}
	if r.recent {
		if m.Item(locales.SidebarForget()).Chosen() {
			w.defer_(func() { a.forgetRecent(path) })
		}
	}
	if folder {
		m.Separator()
		if m.Item(locales.MenuCopyPath()).Chosen() {
			w.copyText(path)
		}
	}
}

func placeTip(path, note string) string {
	switch {
	case path == fsys.Computer:
		return locales.ThisPC()
	case strings.HasPrefix(path, "::"):
		return ""
	case note != "":
		return locales.PlaceFree(path, note)
	}
	return path
}
