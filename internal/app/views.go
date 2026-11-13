package app

import (
	"unicode/utf8"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/fsys"
)

// viewMode is how a tab shows its folder.
type viewMode uint8

const (
	// viewDetails is a table: a row an entry, with its date, type and
	// size in columns that sort.
	viewDetails viewMode = iota
	// viewList is names alone, with small icons, in as many columns as
	// fit: the most entries in the least room.
	viewList
	// viewTiles is a larger icon beside the name, the type and the size.
	viewTiles
	// viewIcons, viewLargeIcons and viewHugeIcons are icons above their
	// names.
	viewIcons
	viewLargeIcons
	viewHugeIcons
	viewModes
)

// viewSpec is what a view mode is called and how large it draws.
type viewSpec struct {
	// id is the name settings and sessions keep, and title the one
	// menus show.
	id    string
	title func() string
	// icon is the side of the icons, in DIPs.
	icon int
	// width is the least width of an item, and height its height; for
	// the list, whose items are as high as a table's rows, height is 0.
	width, height float32
}

var viewSpecs = [viewModes]viewSpec{
	viewDetails:    {id: "details", title: locales.ViewDetails, icon: iconSize},
	viewList:       {id: "list", title: locales.ViewList, icon: iconSize, width: 210},
	viewTiles:      {id: "tiles", title: locales.ViewTiles, icon: 32, width: 230, height: 50},
	viewIcons:      {id: "icons", title: locales.ViewIcons, icon: 48, width: 92, height: 92},
	viewLargeIcons: {id: "large", title: locales.ViewLargeIcons, icon: 96, width: 128, height: 142},
	viewHugeIcons:  {id: "huge", title: locales.ViewHugeIcons, icon: 192, width: 216, height: 240},
}

// viewOf is the view mode of a name, the table for one it does not know.
func viewOf(id string) viewMode {
	for m, spec := range viewSpecs {
		if spec.id == id {
			return viewMode(m)
		}
	}
	return viewDetails
}

// setView shows a tab's folder another way. New tabs take the way last
// chosen.
func (w *Window) setView(t *Tab, m viewMode) {
	if m >= viewModes || t.mode == m {
		return
	}
	t.mode = m
	if t.find != nil {
		// Asked for while a search shows: the folder's view too.
		t.find.mode = m
	}
	t.grid = ui.GridState{}
	t.renaming = ""
	t.wantFocus = true
	// The entry under the cursor stays in sight.
	if t.cursor >= 0 && t.cursor < len(t.view) {
		t.list.ScrollTo(t.cursor, ui.Center)
		t.scrollGrid = true
	}
	w.app.set.View = viewSpecs[m].id
	w.app.saveSoon()
	w.changed()
}

// viewMenu lists the view modes, the tab's marked.
func (w *Window) viewMenu(m *ui.Menu, t *Tab) {
	keys := [viewModes]ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6}
	for mode, spec := range viewSpecs {
		if m.Item(spec.title()).Radio(t.mode == viewMode(mode)).Shortcut(ui.Cmd|ui.Shift, keys[mode]).Chosen() {
			w.setView(t, viewMode(mode))
		}
	}
}

// grid builds a tab's folder as items in a grid: the list, the tiles and
// the icons. Only the items in view are built, whatever the folder holds.
func (w *Window) grid(c *ui.Context, t *Tab, focused bool) {
	a := w.app
	th := c.Theme()
	spec := &viewSpecs[t.mode]
	drives := t.Path == fsys.Computer
	icons := a.icons.at(spec.icon)

	st := &t.grid
	st.Key = func(i int) any {
		if i >= len(t.view) {
			return ""
		}
		return t.entry(i).Name
	}
	st.Label = func(i int) string {
		if i >= len(t.view) {
			return ""
		}
		if drives {
			return t.volumes[t.view[i]].Title()
		}
		if t.bin() {
			return t.trash[t.view[i]].Name
		}
		if t.searching() {
			return t.result(t.view[i]).Name
		}
		return t.entry(i).Name
	}
	st.Selection = &t.sel
	st.Selected = &t.cursor
	st.Border = 1
	st.Gap = 4
	height := spec.height
	if t.mode == viewList {
		st.Gap = -1
		height = a.set.RowHeight
	}
	if drives || t.bin() {
		st.DragData = nil
	} else {
		st.DragOptions = transfer.DragOptions{Operations: transfer.Copy | transfer.Move}
		st.DragData = func(items []int) transfer.Data { return t.dragData(items) }
	}
	st.ItemBuilt = func(item ui.Element, i int) {
		if i >= len(t.view) {
			return
		}
		e := t.entry(i)
		w.bandEntry(t, item, e.Name)
		// A directory takes the files dropped on it.
		if real(t.Path) && e.IsDir() && w.takeFiles(item, t.pathOf(e)) {
			item.Background(th.Accent.Alpha(0.35))
		}
		name := e.Name
		item.ContextMenu(func(m *ui.Menu) {
			// The menu acts on what is chosen: the item it opens on,
			// unless that is one of several chosen.
			row := t.rowOf(name)
			if row < 0 {
				return
			}
			if !t.sel.Has(name) {
				t.sel.Clear()
				t.sel.Add(name)
				t.cursor, t.selStale = row, true
			}
			w.entryMenu(m, t, row)
		})
	}
	if t.scrollGrid && t.cursor >= 0 && t.cursor < len(t.view) {
		t.scrollGrid = false
		st.ScrollTo(t.cursor, ui.Center)
	}

	g := ui.GridView(c.Key(t.ID), st, len(t.view), spec.width, height, func(i int) {
		w.item(c, t, icons, i)
	}).Grow(1).MinHeight(0).Padding(2, 6).Label(locales.Files())
	w.bandOn(c, t, g)
	w.takeTyping(c, t, g, true)
	if g.Changed() {
		t.selStale = true
	}
	if g.Submitted() && t.renaming == "" {
		w.openChosen(t)
	}
	switch {
	case t.bin():
		g.ContextMenu(func(m *ui.Menu) { w.trashFolderMenu(m, t) })
	case !drives && !t.searching():
		g.ContextMenu(func(m *ui.Menu) { w.folderMenu(m, t) })
	}
	if t.wantFocus && focused && !t.editing && t.renaming == "" {
		g.Focus()
		t.wantFocus = false
	}
	if t.rename != "" {
		// Renaming is asked for: the item shows its name as a text to
		// edit.
		if row := t.rowOf(t.rename); row >= 0 && real(t.Path) {
			t.renaming, t.draft, t.renameFresh = t.rename, t.rename, true
			st.ScrollIntoView(row)
		}
		t.rename = ""
	}
	if g.Shortcut(0, ui.KeyF2) && t.renaming == "" && real(t.Path) && t.cursor >= 0 && t.cursor < len(t.view) {
		t.rename = t.entry(t.cursor).Name
		c.Invalidate()
	}
	w.fileKeys(c, t, g.Shortcut)
}

// dragData is the files of rows, as a drag carries them to other panes,
// windows and programs.
func (t *Tab) dragData(rows []int) transfer.Data {
	paths := make([]string, 0, len(rows))
	for _, row := range rows {
		if row < len(t.view) {
			paths = append(paths, t.pathOf(t.entry(row)))
		}
	}
	data, _ := transfer.FileData(paths...)
	return data
}

// item builds an entry of a grid.
func (w *Window) item(c *ui.Context, t *Tab, icons *iconSet, row int) {
	if row >= len(t.view) {
		return
	}
	a := w.app
	th := c.Theme()
	i := t.view[row]
	e := &t.entries[i]
	tx := &t.text[i]
	drives := t.Path == fsys.Computer
	size := float32(icons.size)

	t.building++
	if tx.fresh(icons.size, a.icons.epoch) {
		tx.thumb, tx.thumbDone = nil, false
	}
	icon := tx.icon
	if icon == nil {
		var final bool
		if icon, final = icons.forEntry(t.iconOf(i)); final {
			tx.icon = icon
		}
	}
	// A picture of the file's content takes the icon's place once made.
	if !tx.thumbDone {
		tx.thumb, tx.thumbDone = icons.thumbnail(t.iconOf(i))
	}
	if tx.thumb != nil {
		icon = tx.thumb
	}
	faded := e.Hidden() || len(a.cut) > 0 && a.cut[t.pathOf(e)]
	picture := func() ui.Element {
		if icon == nil {
			return ui.Box(c).Size(size, size).Shrink(0)
		}
		img := ui.Image(c, icon).Size(size, size).Shrink(0).Fit(ui.Contain)
		if faded {
			img.Opacity(0.45)
		}
		return img
	}
	name := e.Name
	if drives {
		name = t.volumes[i].Title()
	} else if t.bin() {
		name = t.trash[i].Name
	} else if t.searching() {
		name = t.result(i).Name
	}
	// label builds the name, or the text that renames it.
	label := func(lines int, center bool) {
		if t.renaming == e.Name && real(t.Path) {
			w.renameInput(c, t, e, center)
			return
		}
		text := ui.Text(c, name).MinWidth(0)
		if lines == 1 {
			text.SingleLine()
		} else {
			text.Wrap().MaxLines(lines)
		}
		if center {
			text.TextAlign(ui.Center).FillWidth()
		}
		if e.Hidden() {
			text.TextColor(th.TextMuted)
		}
	}

	switch t.mode {
	case viewList:
		ui.Row(c).Grow(1).Padding(0, 6).Gap(6).MinWidth(0).Children(func() {
			picture()
			label(1, false)
		})
	case viewTiles:
		ui.Row(c).Grow(1).Padding(0, 8).Gap(10).MinWidth(0).Children(func() {
			picture()
			ui.Column(c).Grow(1).MinWidth(0).Gap(1).Justify(ui.Center).Children(func() {
				label(1, false)
				small := th.FontSize - 1
				if drives {
					if v := &t.volumes[i]; v.Ready {
						usageBar(c, v).FillWidth().MaxWidth(170).MarginY(2)
						ui.Text(c, locales.DriveFreeOf(formatSize(v.Free), formatSize(v.Total))).SingleLine().FontSize(small).TextColor(th.TextMuted)
					}
					return
				}
				if t.bin() {
					// Where it was, in place of what kind it is.
					ui.Text(c, t.trash[i].From).SingleLine().FontSize(small).TextColor(th.TextMuted)
					tx.make(e)
					ui.Text(c, tx.modified).SingleLine().FontSize(small).TextColor(th.TextMuted)
					return
				}
				if t.searching() {
					// Where it is, in place of what kind it is.
					ui.Text(c, t.where(t.result(i))).SingleLine().FontSize(small).TextColor(th.TextMuted)
				} else {
					ui.Text(c, a.icons.typeName(e)).SingleLine().FontSize(small).TextColor(th.TextMuted)
				}
				if !e.IsDir() {
					tx.make(e)
					ui.Text(c, tx.size).SingleLine().FontSize(small).TextColor(th.TextMuted)
				}
			})
		})
	default:
		ui.Column(c).Grow(1).Padding(6, 4, 2, 4).Gap(4).AlignItems(ui.Center).MinWidth(0).Children(func() {
			picture()
			if !drives {
				label(2, true)
				return
			}
			// A drive: its name on one line, and how full it is under it.
			label(1, true)
			if v := &t.volumes[i]; v.Ready {
				usageBar(c, v).Width(max(size, 64)).Tooltip(locales.DriveFreeOf(formatSize(v.Free), formatSize(v.Total)))
			}
		})
	}
}

// usageBar shows how full a drive is, as the system's file manager does
// under its drives: the part in use filled, in red when little is left.
func usageBar(c *ui.Context, v *fsys.Volume) ui.Element {
	th := c.Theme()
	used := float32(0)
	if v.Total > 0 {
		used = float32(float64(v.Total-v.Free) / float64(v.Total))
	}
	bar := ui.Box(c).Height(5).Shrink(0)
	bar.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, th.SurfacePressed, r.H/2)
		color := th.Accent
		if used > 0.9 {
			color = th.Danger
		}
		if w := r.W * min(max(used, 0), 1); w >= r.H {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, color, r.H/2)
		}
	})
	return bar
}

// renameInput is the text that renames an entry in a grid, in place of
// its name: Enter, or the focus going elsewhere, keeps what was typed,
// and Escape gives it up.
func (w *Window) renameInput(c *ui.Context, t *Tab, e *fsys.Entry, center bool) {
	th := c.Theme()
	in := ui.TextInputBase(c.Key("rename"), &t.draft).Grow(1).MinWidth(40).Padding(1, 4).Radius(3).
		Background(th.Background).Border(1, th.Accent).Label("Name")
	if center {
		in.TextAlign(ui.Center).FillWidth()
	}
	done := func(keep bool) {
		name := t.draft
		t.renaming, t.draft = "", ""
		t.wantFocus = true
		if keep && name != "" && name != e.Name {
			w.renameTo(t, e, name)
		}
	}
	switch {
	case t.renameFresh:
		t.renameFresh = false
		in.Focus()
		// The name before its extension, which is seldom what changes.
		end := utf8.RuneCountInString(t.draft)
		if ext := fsys.ExtOf(t.draft); ext != "" && !e.IsDir() {
			end -= utf8.RuneCountInString(ext)
		}
		in.SetTextSelection(0, end)
	case in.Submitted():
		done(true)
	case in.Shortcut(0, ui.KeyEscape):
		done(false)
	case in.FocusLeft():
		done(true)
	}
}
