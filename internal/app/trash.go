package app

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/clock"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
)

// The Recycle Bin shows in a tab as a folder does, though it is none: its
// rows are what was deleted on every drive, each named by where it is
// kept now, which tells two files of one name apart, and shown by the
// name it had. What can be done there is its own: put back, delete for
// good, empty.

// bin tells whether the tab shows the Recycle Bin.
func (t *Tab) bin() bool { return t.Path == shell.RecycleBin }

// real tells a folder of the file system from the places that are none:
// the list of drives, the Recycle Bin.
func real(path string) bool { return path != "" && !strings.HasPrefix(path, "::") }

// loadTrash reads the Recycle Bin and makes its rows. It runs off the
// main thread.
func (a *App) loadTrash(filter string, o fsys.Order) *loaded {
	start := clock.Now()
	l := &loaded{path: shell.RecycleBin}
	items, err := a.desk.trash()
	if err != nil {
		l.err = err
		return l
	}
	l.trash = items
	l.entries = make([]fsys.Entry, len(items))
	for i, it := range items {
		e := fsys.Entry{Name: it.Path, Size: it.Size, ModTime: it.Deleted}
		if it.Dir {
			e.Attr = fsys.AttrDir
		}
		l.entries[i] = e
	}
	l.view = trashView(items, filter, o)
	l.took = clock.Since(start)
	return l
}

// trashView makes the rows of the Recycle Bin: the items whose names hold
// the filter, in the order asked for, which the items' own fields give,
// not those of the files they are kept as.
func trashView(items []shell.TrashItem, filter string, o fsys.Order) []int32 {
	filter = strings.ToLower(filter)
	view := make([]int32, 0, len(items))
	for i := range items {
		if filter == "" || containsFold(items[i].Name, filter) {
			view = append(view, int32(i))
		}
	}
	slices.SortStableFunc(view, func(x, y int32) int {
		a, b := &items[x], &items[y]
		var c int
		switch o.By {
		case fsys.BySize:
			c = cmp.Compare(a.Size, b.Size)
		case fsys.ByModified:
			c = cmp.Compare(a.Deleted, b.Deleted)
		case fsys.ByType:
			c = strings.Compare(strings.ToLower(a.From), strings.ToLower(b.From))
		}
		if c == 0 {
			c = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		if o.Descending {
			c = -c
		}
		return c
	})
	return view
}

// trashMenu is the context menu of what is chosen in the Recycle Bin.
func (w *Window) trashMenu(m *ui.Menu, t *Tab) {
	if m.Item(locales.TrashRestore()).Chosen() {
		w.defer_(func() { w.restoreChosen(t) })
	}
	if m.Item(locales.TrashDeleteForever()).Shortcut(0, ui.KeyDelete).Chosen() {
		w.defer_(func() { w.deleteTrashChosen(t) })
	}
	m.Separator()
	if m.Item(locales.MenuCopyPath()).Chosen() {
		// Where it was, which is what the user knows it by.
		var paths []string
		for _, i := range t.view {
			if t.sel.Has(t.entries[i].Name) {
				paths = append(paths, fsys.Join(t.trash[i].From, t.trash[i].Name))
			}
		}
		w.copyText(strings.Join(paths, "\r\n"))
	}
}

// trashFolderMenu is the context menu of the room beside the items.
func (w *Window) trashFolderMenu(m *ui.Menu, t *Tab) {
	if m.Item(locales.TrashEmpty()).Disabled(len(t.entries) == 0).Chosen() {
		w.defer_(func() { w.emptyTrash(t) })
	}
	m.Separator()
	m.Submenu(locales.View(), func(m *ui.Menu) { w.viewMenu(m, t) })
	if m.Item(locales.MenuRefresh()).Shortcut(0, ui.KeyF5).Chosen() {
		w.reload(t)
	}
}

// restoreChosen puts what is chosen back where it was.
func (w *Window) restoreChosen(t *Tab) {
	paths := t.chosenPaths()
	if len(paths) == 0 || !t.bin() {
		return
	}
	a := w.app
	go func() {
		restored, err := a.desk.restoreTrash(paths)
		w.report(err)
		w.update(func() {
			a.refreshTrash()
			// The folders they came back to show them.
			var dirs []string
			for _, path := range restored {
				if dir, ok := fsys.Parent(path); ok && !slices.Contains(dirs, dir) {
					dirs = append(dirs, dir)
				}
			}
			a.refreshFolders(dirs)
		})
	}()
}

// deleteTrashChosen deletes what is chosen for good, once the user said
// so: nothing brings it back.
func (w *Window) deleteTrashChosen(t *Tab) {
	paths := t.chosenPaths()
	if len(paths) == 0 || !t.bin() {
		return
	}
	what := locales.Items(len(paths))
	if len(paths) == 1 {
		for i := range t.entries {
			if t.entries[i].Name == paths[0] {
				what = "“" + t.trash[i].Name + "”"
			}
		}
	}
	a := w.app
	w.ask = &question{open: true, title: locales.DeleteForeverTitle(what), message: locales.DeleteForeverMessage(),
		confirm: locales.MenuDelete(), yes: func() {
			go func() {
				err := a.desk.deleteTrash(paths)
				w.report(err)
				w.update(a.refreshTrash)
			}()
		}}
	w.invalidate()
}

// emptyTrash deletes all the Recycle Bin holds, once the user said so.
func (w *Window) emptyTrash(t *Tab) {
	a := w.app
	owner := w.handle()
	w.ask = &question{open: true, title: locales.TrashEmptyTitle(len(t.entries)), message: locales.DeleteForeverMessage(),
		confirm: locales.TrashEmpty(), yes: func() {
			go func() {
				err := a.desk.emptyTrash(owner)
				w.report(err)
				w.update(a.refreshTrash)
			}()
		}}
	w.invalidate()
}

// refreshTrash reads the Recycle Bin again in the tabs that show it: no
// folder of it can be watched for changes.
func (a *App) refreshTrash() {
	for _, w := range a.windows {
		for _, t := range w.tabs {
			if t.bin() {
				w.refresh(t)
			}
		}
	}
}

// refreshFolders reads folders again in the tabs that show them.
func (a *App) refreshFolders(dirs []string) {
	for _, w := range a.windows {
		for _, t := range w.tabs {
			if slices.ContainsFunc(dirs, func(dir string) bool { return fsys.SameName(dir, t.Path) }) {
				w.refresh(t)
			}
		}
	}
}
