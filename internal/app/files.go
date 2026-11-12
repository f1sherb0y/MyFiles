package app

import (
	"fmt"
	"os"
	"slices"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/clock"
	"myfiles/internal/fsys"
	"myfiles/internal/workspace"
)

// The columns of a folder's rows, and of the list of drives, which keeps
// their places and widths under other titles.
var (
	fileColumns = []ui.TableColumn{
		{ID: colName, MinWidth: 120, Sortable: true},
		{ID: colModified, Width: 128, MinWidth: 60, Sortable: true},
		{ID: colType, Width: 132, MinWidth: 50, Sortable: true},
		{ID: colSize, Width: 82, MinWidth: 50, Align: ui.End, Sortable: true},
	}
	fileTitles   = [...]func() string{locales.ColumnName, locales.ColumnModified, locales.ColumnType, locales.ColumnSize}
	driveTitles  = [...]func() string{locales.ColumnName, locales.ColumnFree, locales.ColumnFileSystem, locales.ColumnTotal}
	trashTitles  = [...]func() string{locales.ColumnName, locales.ColumnDeleted, locales.ColumnFrom, locales.ColumnSize}
	driveColumns = []ui.TableColumn{
		{ID: colName, MinWidth: 120},
		{ID: colModified, Width: 128, MinWidth: 60},
		{ID: colType, Width: 132, MinWidth: 50},
		{ID: colSize, Width: 82, MinWidth: 50, Align: ui.End},
	}
)

// tracing prints the parts of each folder's way to the screen.
var tracing = os.Getenv("MYFILES_TRACE") != ""

// files builds a tab's folder, in the view the tab shows it in.
func (w *Window) files(c *ui.Context, p *workspace.Pane, t *Tab, focused bool) {
	a := w.app
	drives := t.Path == fsys.Computer
	if !drives && (t.sort != t.sorted || t.filter != t.filtered) {
		if t.sort != t.sorted {
			a.set.SortBy, a.set.SortDescending = t.sort.Column, t.sort.Descending
			a.saveSoon()
		}
		t.review(a.set.ShowHidden, a.set.DirsFirst)
	}

	if !t.shownAt.IsZero() {
		// The folder's rows reach the screen with this frame: how long
		// that took, from the moment the folder was asked for.
		asked := t.shownAt
		t.shownAt = 0
		t.trace.built = clock.Now()
		c.OnPresented(func() {
			t.shown = clock.Since(asked)
			if tracing {
				tr := &t.trace
				fmt.Fprintf(os.Stderr, "trace %-28s load %6.2f  post %6.2f  build %6.2f  present %6.2f  = %6.2f ms\n", t.Path,
					ms(tr.loaded.Sub(asked)), ms(tr.arrived.Sub(tr.loaded)), ms(tr.built.Sub(tr.arrived)),
					ms(clock.Now().Sub(tr.built)), ms(t.shown))
			}
			if a.bench != nil {
				a.bench.shown(w, t)
			}
			w.invalidate()
		})
	}

	t.building = 0
	t.entryBoxes = t.entryBoxes[:0]
	if t.mode == viewDetails {
		w.details(c, t, focused)
	} else {
		w.grid(c, t, focused)
	}
	t.span = t.building
	if len(t.view) == 0 && !t.loading && t.Path != "" {
		msg := locales.FolderEmpty()
		if t.bin() {
			msg = locales.TrashEmptyState()
		}
		if t.filter != "" {
			msg = locales.FilterNoMatch()
		}
		ui.Text(c, msg).Absolute().Top(48).Left(0).Right(0).TextAlign(ui.Center).
			TextColor(c.Theme().TextMuted).PassThrough()
	}
}

// details builds the folder as a table, which builds only the rows in
// view, whatever the folder holds.
func (w *Window) details(c *ui.Context, t *Tab, focused bool) {
	a := w.app
	drives := t.Path == fsys.Computer

	st := &t.list
	st.Key = func(row int) any {
		if row >= len(t.view) {
			return ""
		}
		return t.entry(row).Name
	}
	st.Label = func(row int) string {
		if row >= len(t.view) {
			return ""
		}
		if t.bin() {
			// The name it had, not the one it is kept under.
			return t.trash[t.view[row]].Name
		}
		return t.entry(row).Name
	}
	st.Selection = &t.sel
	st.Selected = &t.cursor
	st.KeepOffset = true
	st.Columns = a.columns
	columns := titled(&a.fileCols, fileColumns, fileTitles[:])
	if drives {
		columns = titled(&a.driveCols, driveColumns, driveTitles[:])
		st.Sort, st.DragData, st.RowBuilt = nil, nil, nil
	} else if t.bin() {
		// What was deleted: sorted and chosen as files are, and dragged
		// nowhere.
		columns = titled(&a.trashCols, fileColumns, trashTitles[:])
		st.Sort, st.DragData = &t.sort, nil
		st.RowBuilt = func(row ui.Element, i int) {
			if i < len(t.view) {
				w.bandEntry(t, row, t.entry(i).Name)
			}
		}
	} else {
		st.Sort = &t.sort
		st.DragOptions = transfer.DragOptions{Operations: transfer.Copy | transfer.Move}
		st.DragData = func(rows []int) transfer.Data { return t.dragData(rows) }
		// A directory's row takes the files dropped on it.
		st.RowBuilt = func(row ui.Element, i int) {
			if i >= len(t.view) {
				return
			}
			e := t.entry(i)
			w.bandEntry(t, row, e.Name)
			if e.IsDir() && w.takeFiles(row, t.pathOf(e)) {
				row.Background(c.Theme().Accent)
			}
		}
	}
	st.CellMenu = func(m *ui.Menu, row, col int) { w.entryMenu(m, t, row) }

	ui.Themed(c, &a.themes.table).Grow(1).MinHeight(0).Children(func() {
		table := ui.Table(c.Key(t.ID), st, columns, len(t.view), func(row, col int) {
			w.cell(c, t, row, col)
		}).Grow(1).MinHeight(0).Label(locales.Files())
		if !drives {
			w.bandOn(c, t, table)
		}
		a.columns = st.Columns
		if table.Changed() {
			t.selStale = true
			a.set.Columns, a.set.ColumnOrder = st.Columns.Widths, st.Columns.Order
			a.saveSoon()
		}
		if table.Submitted() {
			w.openChosen(t)
		}
		switch {
		case t.bin():
			table.ContextMenu(func(m *ui.Menu) { w.trashFolderMenu(m, t) })
		case !drives:
			table.ContextMenu(func(m *ui.Menu) { w.folderMenu(m, t) })
		}
		if t.wantFocus && focused && !t.editing {
			if st.Focus(c) {
				t.wantFocus = false
			}
		}
		if t.rename != "" {
			if row := t.rowOf(t.rename); row >= 0 {
				st.Edit(t.rename)
				t.rename = ""
			}
		}
		w.fileKeys(c, t, func(mods ui.Modifiers, key ui.Key) bool { return st.Shortcut(c, mods, key) })
	})
}

// cell builds a cell of a row.
func (w *Window) cell(c *ui.Context, t *Tab, row, col int) {
	a := w.app
	th := c.Theme()
	if row >= len(t.view) {
		return
	}
	i := t.view[row]
	e := &t.entries[i]
	tx := &t.text[i]
	drives := t.Path == fsys.Computer
	switch col {
	case 0:
		t.building++
		tx.fresh(iconSize, a.icons.epoch)
		if tx.icon == nil {
			if icon, final := a.icons.at(iconSize).forEntry(t.Path, e); final {
				tx.icon = icon
			} else if icon != nil {
				ui.Image(c, icon).Size(iconSize, iconSize).Shrink(0).MarginEnd(6)
			} else {
				ui.Box(c).Size(iconSize, iconSize).Shrink(0).MarginEnd(6)
			}
		}
		if tx.icon != nil {
			img := ui.Image(c, tx.icon).Size(iconSize, iconSize).Shrink(0).MarginEnd(6)
			if e.Hidden() || len(a.cut) > 0 && a.cut[t.pathOf(e)] {
				img.Opacity(0.45)
			}
		}
		switch {
		case drives:
			ui.Text(c, t.volumes[i].Title()).SingleLine().Grow(1).MinWidth(0)
		case t.bin():
			ui.Text(c, t.trash[i].Name).SingleLine().Grow(1).MinWidth(0)
		default:
			name := e.Name
			text := ui.EditableText(c, &name).Grow(1)
			if e.Hidden() {
				text.TextColor(th.TextMuted)
			}
			if text.Changed() && name != e.Name && name != "" {
				w.renameTo(t, e, name)
			}
		}
	case 1:
		if drives {
			v := &t.volumes[i]
			if v.Ready {
				ui.Text(c, formatSize(v.Free)).SingleLine().TextColor(th.TextMuted)
			}
			return
		}
		tx.make(e)
		ui.Text(c, tx.modified).SingleLine().TextColor(th.TextMuted)
	case 2:
		if drives {
			ui.Text(c, t.volumes[i].FileSystem).SingleLine().TextColor(th.TextMuted)
			return
		}
		if t.bin() {
			ui.Text(c, t.trash[i].From).SingleLine().TextColor(th.TextMuted)
			return
		}
		ui.Text(c, a.icons.typeName(e)).SingleLine().TextColor(th.TextMuted)
	case 3:
		if drives {
			if v := &t.volumes[i]; v.Ready {
				ui.Text(c, formatSize(v.Total)).SingleLine().TextColor(th.TextMuted)
			}
			return
		}
		tx.make(e)
		if t.bin() && e.IsDir() && e.Size > 0 {
			// The Recycle Bin noted how much a folder held.
			ui.Text(c, formatSize(e.Size)).SingleLine().TextColor(th.TextMuted)
			return
		}
		ui.Text(c, tx.size).SingleLine().TextColor(th.TextMuted)
	}
}

// make formats what a row shows of its entry, once.
func (tx *rowText) make(e *fsys.Entry) {
	if tx.made {
		return
	}
	tx.made = true
	tx.modified = formatTime(e.ModTime)
	if !e.IsDir() {
		tx.size = formatSize(e.Size)
	}
}

// fileKeys handles the keys of the entries, while they have the focus:
// pressed reports a key pressed there.
func (w *Window) fileKeys(c *ui.Context, t *Tab, pressed func(mods ui.Modifiers, key ui.Key) bool) {
	if c.FocusTakesText() {
		// A name is being typed: the keys are its text's.
		return
	}
	drives := t.Path == fsys.Computer
	switch {
	case pressed(0, ui.KeyBackspace):
		w.up(t)
	case pressed(ui.Alt, ui.KeyEnter):
		w.properties(t)
	case pressed(0, ui.KeyEscape):
		// The choice goes, and what was cut is no longer.
		t.sel.Clear()
		t.selStale = true
		w.app.uncut()
	case drives:
	case t.bin():
		// Nothing there is copied, pasted or opened: put back, or deleted.
		if pressed(0, ui.KeyDelete) || pressed(ui.Shift, ui.KeyDelete) {
			w.deleteTrashChosen(t)
		}
	case pressed(0, ui.KeyDelete):
		w.deleteChosen(t, false)
	case pressed(ui.Shift, ui.KeyDelete):
		w.deleteChosen(t, true)
	case pressed(ui.Cmd, ui.KeyC):
		w.copyChosen(t, false)
	case pressed(ui.Cmd, ui.KeyX):
		w.copyChosen(t, true)
	case pressed(ui.Cmd, ui.KeyV):
		w.paste(t.Path)
	case pressed(ui.Cmd|ui.Shift, ui.KeyC):
		w.copyPaths(t)
	}
}

// openChosen opens what is chosen: one directory in the tab, more of
// them in tabs of their own, and files with their programs.
func (w *Window) openChosen(t *Tab) {
	if t.bin() {
		// What is deleted opens once it is put back.
		return
	}
	chosen := t.chosen()
	if len(chosen) == 0 && t.cursor >= 0 && t.cursor < len(t.view) {
		chosen = []*fsys.Entry{t.entry(t.cursor)}
	}
	const most = 16
	if len(chosen) > most {
		w.say(locales.TooManyToOpen())
		return
	}
	dirs := 0
	for _, e := range chosen {
		if e.IsDir() {
			dirs++
		}
	}
	for _, e := range chosen {
		switch {
		case !e.IsDir():
			w.openEntry(t, e)
		case dirs == 1:
			w.openEntry(t, e)
		default:
			if p := w.layout.PaneOf(t.ID); p != nil {
				path, pane := t.pathOf(e), p.ID
				w.defer_(func() { w.newTab(pane, path, "") })
			}
		}
	}
}

// entryMenu is the context menu of a row: it acts on the rows chosen,
// which the right click made include this one.
func (w *Window) entryMenu(m *ui.Menu, t *Tab, row int) {
	if row < 0 || row >= len(t.view) {
		return
	}
	if t.bin() {
		w.trashMenu(m, t)
		return
	}
	e := t.entry(row)
	path := t.pathOf(e)
	drives := t.Path == fsys.Computer
	pane := workspace.PaneID(0)
	if p := w.layout.PaneOf(t.ID); p != nil {
		pane = p.ID
	}
	if m.Item(locales.MenuOpen()).Chosen() {
		w.defer_(func() { w.openChosen(t) })
	}
	if e.IsDir() {
		if m.Item(locales.MenuOpenNewTab()).Chosen() {
			w.defer_(func() { w.newTab(pane, path, "") })
		}
		if m.Item(locales.MenuOpenSplit()).Chosen() {
			w.defer_(func() {
				id := w.newTab(pane, path, "")
				w.layout.Split(pane, id, workspace.ZoneRight)
				w.changed()
			})
		}
		if a := w.app; a.pinned(path) {
			if m.Item(locales.SidebarUnpin()).Chosen() {
				w.defer_(func() { a.unpin(path) })
			}
		} else if !drives {
			if m.Item(locales.SidebarPin()).Chosen() {
				w.defer_(func() { a.pin(path) })
			}
		}
	} else {
		// Asked for as the menu opens: there by the time the submenu is.
		ta, known := w.appsFor(path)
		m.Submenu(locales.MenuOpenWith(), func(m *ui.Menu) {
			for i, app := range ta.apps {
				item := m.Item(app.Name).ID(app.ID)
				if i < len(ta.icons) && ta.icons[i] != nil {
					item.Icon(ta.icons[i])
				}
				if item.Chosen() {
					w.openIn(path, app)
				}
			}
			if !known {
				m.Item(locales.MenuOpenWithLooking()).Disabled(true)
			}
			if len(ta.apps) > 0 || !known {
				m.Separator()
			}
			if m.Item(locales.MenuOpenWithOther()).Chosen() {
				w.choose(path, false)
			}
		})
	}
	m.Separator()
	if !drives {
		if m.Item(locales.MenuCut()).Shortcut(ui.Cmd, ui.KeyX).Chosen() {
			w.copyChosen(t, true)
		}
		if m.Item(locales.MenuCopy()).Shortcut(ui.Cmd, ui.KeyC).Chosen() {
			w.copyChosen(t, false)
		}
		if e.IsDir() && m.Item(locales.MenuPasteInto()).Disabled(!w.canPaste()).Chosen() {
			w.paste(path)
		}
	}
	if m.Item(locales.MenuCopyPath()).Shortcut(ui.Cmd|ui.Shift, ui.KeyC).Chosen() {
		w.copyPaths(t)
	}
	if !drives {
		m.Separator()
		if m.Item(locales.MenuRename()).Shortcut(0, ui.KeyF2).Chosen() {
			t.rename = e.Name
		}
		if m.Item(locales.MenuDelete()).Shortcut(0, ui.KeyDelete).Chosen() {
			w.defer_(func() { w.deleteChosen(t, false) })
		}
	}
	m.Separator()
	w.more(m, t, false)
	if m.Item(locales.MenuProperties()).Shortcut(ui.Alt, ui.KeyEnter).Chosen() {
		w.properties(t)
	}
}

// folderMenu is the context menu of the room under the rows: it acts on
// the folder shown.
func (w *Window) folderMenu(m *ui.Menu, t *Tab) {
	if m.Item(locales.MenuNewFolder()).Shortcut(ui.Cmd|ui.Shift, ui.KeyN).Chosen() {
		w.newFolder(t)
	}
	if m.Item(locales.MenuNewTextFile()).Chosen() {
		w.newFile(t)
	}
	m.Separator()
	if m.Item(locales.MenuPaste()).Shortcut(ui.Cmd, ui.KeyV).Disabled(!w.canPaste()).Chosen() {
		w.paste(t.Path)
	}
	if m.Item(locales.MenuCopyFolderPath()).Chosen() {
		w.copyText(t.Path)
	}
	m.Separator()
	m.Submenu(locales.View(), func(m *ui.Menu) { w.viewMenu(m, t) })
	if m.Item(locales.MenuRefresh()).Shortcut(0, ui.KeyF5).Chosen() {
		w.reload(t)
	}
	hidden := w.app.set.ShowHidden
	if m.Item(locales.MenuShowHidden()).Checked(hidden).Shortcut(ui.Cmd, ui.KeyH).Chosen() {
		w.setHidden(!hidden)
	}
	m.Separator()
	w.more(m, t, true)
	if m.Item(locales.MenuProperties()).Chosen() {
		w.folderProperties(t)
	}
}

// titled is columns with their titles in the language the app shows: the
// same slice from frame to frame, made anew when the language changes.
func titled(kept *[]ui.TableColumn, columns []ui.TableColumn, titles []func() string) []ui.TableColumn {
	if len(*kept) == len(columns) && (*kept)[0].Title == titles[0]() {
		return *kept
	}
	out := slices.Clone(columns)
	for i := range out {
		out[i].Title = titles[i]()
	}
	*kept = out
	return out
}
