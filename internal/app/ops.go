package app

import (
	"bytes"
	"errors"
	"image/png"
	"strings"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
)

// fileDrop is what takes files dragged from a pane, another window or
// another program: to copy them, or to move them.
var fileDrop = transfer.DropOptions{
	Formats:    []transfer.Format{transfer.FileList},
	Operations: transfer.Copy | transfer.Move,
}

// takeFiles makes an element take the files dropped on it into a
// directory, and reports whether files are being dragged over it.
func (w *Window) takeFiles(el ui.Element, dir string) bool {
	_, over := ui.DataDragOver(el, fileDrop)
	if drop, ok := ui.DropData(el, fileDrop); ok {
		w.dropFiles(drop, dir)
	}
	return over
}

// dropFiles puts dropped files into a directory. As in the system's file
// manager, they move within a volume and are copied to another one,
// unless the keys held as they dropped ask for one or the other.
func (w *Window) dropFiles(drop transfer.Drop, dir string) {
	files, err := drop.Data.Files()
	if err != nil || len(files) == 0 {
		return
	}
	move := drop.Operation == transfer.Move
	if drop.Suggested == transfer.None {
		move = sameVolume(files, dir)
	}
	var sources []string
	for _, f := range files {
		f, ok := fsys.Clean(f)
		if !ok || fsys.SameName(f, dir) {
			continue
		}
		// Dropped where it already is, a file has nowhere to move.
		if parent, ok := fsys.Parent(f); move && ok && fsys.SameName(parent, dir) {
			continue
		}
		sources = append(sources, f)
	}
	w.transfer(sources, dir, move)
}

// sameVolume reports whether every file is on the volume of dir.
func sameVolume(files []string, dir string) bool {
	root := volumeOf(dir)
	for _, f := range files {
		if !fsys.SameName(volumeOf(f), root) {
			return false
		}
	}
	return true
}

func volumeOf(path string) string {
	if parts := fsys.Split(path); len(parts) > 0 {
		return parts[0].Path
	}
	return path
}

// transfer copies or moves files into a directory, off the main thread,
// with the system's progress and questions; the tabs showing the
// directory then choose what arrived.
func (w *Window) transfer(sources []string, dir string, move bool) {
	if len(sources) == 0 {
		return
	}
	// The system's progress window belongs to no window of ours: one
	// that did would hold its owner still for as long as a copy takes,
	// and beep at every click on it.
	owner := shell.Window(0)
	go func() {
		var err error
		if move {
			err = shell.Move(owner, sources, dir)
		} else {
			err = shell.Copy(owner, sources, dir)
		}
		w.report(err)
		if err != nil {
			return
		}
		names := make([]string, len(sources))
		for i, s := range sources {
			names[i] = fsys.Base(s)
		}
		w.update(func() {
			w.arrived(dir, names, "")
			if move {
				w.app.left(sources)
			}
		})
	}()
}

// left reads again the tabs showing the folders that paths were moved or
// deleted from, in every window, without waiting for the system to tell
// of the change.
func (a *App) left(paths []string) {
	dirs := map[string]bool{}
	for _, p := range paths {
		if parent, ok := fsys.Parent(p); ok {
			dirs[strings.ToLower(parent)] = true
		}
	}
	for _, w := range a.windows {
		for _, t := range w.tabs {
			if dirs[strings.ToLower(t.Path)] {
				w.refresh(t)
			}
		}
	}
}

// arrived reads again the tabs showing a directory in which entries were
// made, chooses those in the focused one, and starts renaming one of
// them when rename names it.
func (w *Window) arrived(dir string, names []string, rename string) {
	cur := w.current()
	for _, t := range w.tabs {
		if !fsys.SameName(t.Path, dir) {
			continue
		}
		if t == cur {
			t.chooseAfter, t.renameAfter = names, rename
			t.wantFocus = true
		}
		w.refresh(t)
	}
}

// copyChosen puts the files chosen on the clipboard, copied or cut, as
// the system's file manager does: they paste in it, and its paste here.
func (w *Window) copyChosen(t *Tab, cut bool) {
	paths := t.chosenPaths()
	if len(paths) == 0 || !real(t.Path) && !t.searching() {
		return
	}
	a := w.app
	files, err := transfer.FileData(paths...)
	if err != nil {
		w.say(err.Error())
		return
	}
	items := append(files.Items(), transfer.NewItem(transfer.Bytes(shell.CutFormat, shell.CutData(cut))))
	a.cutGen++
	gen := a.cutGen
	err = a.desk.write(transfer.New(items...), func() {
		// Something else took the clipboard: nothing is cut any more.
		if a.cutGen == gen && a.cut != nil {
			a.cut = nil
			a.invalidate()
		}
	})
	if err != nil {
		w.say(err.Error())
		return
	}
	a.cut = nil
	if cut {
		a.cut = make(map[string]bool, len(paths))
		for _, p := range paths {
			a.cut[p] = true
		}
	}
	a.invalidate()
}

// uncut forgets what was cut, which then shows as it was.
func (a *App) uncut() {
	if a.cut != nil {
		a.cut = nil
		a.invalidate()
	}
}

// canPaste reports whether the clipboard holds files.
func (w *Window) canPaste() bool {
	for _, f := range w.app.desk.formats() {
		if f == transfer.FileList {
			return true
		}
	}
	return false
}

// paste puts the clipboard's files into a directory: moved when they
// were cut, here or in the system's file manager, else copied.
func (w *Window) paste(dir string) {
	if !real(dir) {
		return
	}
	data, err := w.app.desk.read(transfer.FileList, shell.CutFormat)
	if err != nil {
		return
	}
	files, err := data.Files()
	if err != nil || len(files) == 0 {
		return
	}
	mark, _ := data.Read(shell.CutFormat)
	move := shell.IsCut(mark)
	var sources []string
	for _, f := range files {
		f, ok := fsys.Clean(f)
		if !ok {
			continue
		}
		if parent, ok := fsys.Parent(f); move && ok && fsys.SameName(parent, dir) {
			continue
		}
		sources = append(sources, f)
	}
	if move {
		// What was cut is pasted once.
		w.app.cutGen++
		w.app.cut = nil
		_ = w.app.desk.write(transfer.New(), nil)
	}
	w.transfer(sources, dir, move)
}

// copyText puts text on the clipboard.
func (w *Window) copyText(s string) {
	w.app.cutGen++
	w.app.cut = nil
	w.app.desk.writeText(s)
}

// copyPaths puts the paths of what is chosen on the clipboard as text,
// one a line; of the folder shown when nothing is chosen.
func (w *Window) copyPaths(t *Tab) {
	paths := t.chosenPaths()
	if len(paths) == 0 {
		paths = []string{t.tip()}
	}
	w.copyText(strings.Join(paths, "\r\n"))
}

// deleteChosen moves what is chosen to the trash, or with permanent
// deletes it for good, which it asks about first.
func (w *Window) deleteChosen(t *Tab, permanent bool) {
	if t.bin() {
		// Deleting there is for good, whichever key asked.
		w.deleteTrashChosen(t)
		return
	}
	paths := t.chosenPaths()
	if len(paths) == 0 || t.Path == fsys.Computer {
		return
	}
	owner := shell.Window(0)
	run := func() {
		go func() {
			w.report(shell.Delete(owner, paths, permanent))
			w.update(func() {
				w.app.left(paths)
				// The tabs showing the Recycle Bin show what went there.
				w.app.refreshTrash()
			})
		}()
	}
	what := "“" + fsys.Base(paths[0]) + "”"
	if len(paths) > 1 {
		what = locales.Items(len(paths))
	}
	switch {
	case permanent:
		w.ask = &question{open: true, title: locales.DeleteForeverTitle(what),
			message: locales.DeleteForeverMessage(), confirm: locales.MenuDelete(), yes: run}
	case w.app.set.ConfirmTrash:
		w.ask = &question{open: true, title: locales.TrashTitle(what),
			confirm: locales.TrashConfirm(), yes: run}
	default:
		run()
	}
}

// renameTo gives an entry another name.
func (w *Window) renameTo(t *Tab, e *fsys.Entry, name string) {
	path, dir, owner := t.pathOf(e), t.Path, w.handle()
	go func() {
		if err := shell.Rename(owner, path, name); err != nil {
			w.report(err)
			return
		}
		w.update(func() { w.arrived(dir, []string{name}, "") })
	}()
}

// newFolder makes a directory in the folder shown and starts renaming
// it, as the system's file manager does.
func (w *Window) newFolder(t *Tab) {
	w.create(t, func(dir string) (string, error) { return shell.NewFolder(dir, locales.NewFolderName()) })
}

// newFile makes an empty text file in the folder shown, to be renamed.
func (w *Window) newFile(t *Tab) {
	w.create(t, func(dir string) (string, error) { return shell.NewFile(dir, locales.NewTextFileName()) })
}

func (w *Window) create(t *Tab, make func(dir string) (string, error)) {
	dir := t.Path
	if !real(dir) {
		return
	}
	go func() {
		path, err := make(dir)
		if err != nil {
			w.report(err)
			return
		}
		name := fsys.Base(path)
		w.update(func() { w.arrived(dir, []string{name}, name) })
	}()
}

// properties shows the system's properties of what is chosen, or of the
// folder shown when nothing is.
func (w *Window) properties(t *Tab) {
	paths := t.chosenPaths()
	if len(paths) == 0 {
		w.folderProperties(t)
		return
	}
	owner := w.handle()
	go func() { w.report(w.app.desk.properties(owner, paths)) }()
}

func (w *Window) folderProperties(t *Tab) {
	if !real(t.Path) {
		return
	}
	owner, path := w.handle(), t.Path
	go func() { w.report(w.app.desk.properties(owner, []string{path})) }()
}

// openIn opens a file with one of the programs for its type.
func (w *Window) openIn(path string, app shell.App) {
	w.openWithApp(path, app, false)
}

func (w *Window) openWithApp(path string, app shell.App, always bool) {
	owner := w.handle()
	go func() {
		var defaultErr error
		if always {
			if err := w.app.desk.setDefaultApp(path, app); err != nil {
				defaultErr = errors.New(locales.ChooserDefaultFailed(fsys.ExtOf(fsys.Base(path)), err.Error()))
			}
		}
		err := w.app.desk.openIn(owner, path, app)
		if err == shell.ErrChoose {
			// Gone since it was listed.
			err = errors.New(locales.ChooserGone(app.Name))
		}
		w.report(errors.Join(defaultErr, err))
	}()
}

// appsFresh is how long the programs listed for a type are taken for the
// ones there are: one installed meanwhile shows the next time.
const appsFresh = 30 * time.Second

// typeApps are the programs that open the files of a type, and when
// they were asked for; known is set once the system answered.
type typeApps struct {
	apps []shell.App
	// icons are the programs' icons as menus show them, parallel to apps:
	// nil for a program without one.
	icons []ui.MenuIcon
	asked time.Time
	known bool
}

// appsFor lists the programs that open a file, by its type, and whether
// the system has said yet. Asking takes the system a moment, on another
// thread: the menu that shows them is built again when they come.
func (w *Window) appsFor(path string) (*typeApps, bool) {
	a := w.app
	ext := fsys.ExtOf(fsys.Base(path))
	if ext == "" {
		return &typeApps{known: true}, true
	}
	if a.apps == nil {
		a.apps = map[string]*typeApps{}
	}
	ta := a.apps[ext]
	if ta == nil {
		ta = &typeApps{}
		a.apps[ext] = ta
	}
	if time.Since(ta.asked) > appsFresh {
		ta.asked = time.Now()
		go func() {
			apps := a.desk.apps(path, false)
			pictures := make([][]byte, len(apps))
			for i, app := range apps {
				var buf bytes.Buffer
				if app.Icon != nil && png.Encode(&buf, app.Icon) == nil {
					pictures[i] = buf.Bytes()
				}
			}
			w.update(func() {
				icons := make([]ui.MenuIcon, len(apps))
				for i, data := range pictures {
					if data == nil {
						continue
					}
					if icon, err := a.desk.menuIcon(data); err == nil {
						icons[i] = icon
					}
				}
				ta.apps, ta.icons, ta.known = apps, icons, true
				w.invalidate()
			})
		}()
	}
	return ta, ta.known
}

// systemMenu shows the system's own context menu at the pointer, for
// what is chosen or for the folder itself: what other programs added to
// it is there.
func (w *Window) systemMenu(t *Tab, background bool) {
	if t.Path == "" || t.Path == fsys.Computer && background || t.bin() {
		return
	}
	if t.searching() && (background || len(t.chosenPaths()) == 0) {
		// What a search found is of no one folder, and a program found is
		// no file: the system has no menu for either.
		return
	}
	paths := t.chosenPaths()
	dir := ""
	if background || len(paths) == 0 {
		paths, dir = nil, t.Path
	}
	owner := w.handle()
	go func() {
		action, err := w.app.desk.systemMenu(owner, paths, dir)
		w.report(err)
		w.update(func() {
			if w.tabs[t.ID] != t {
				return
			}
			if action == shell.MenuRename && len(paths) == 1 {
				t.rename = t.nameOfPath(paths[0])
				t.wantFocus = true
				return
			}
			switch {
			case action == shell.MenuOpen:
				// As a double click opens: by the program found here.
				w.openChosen(t)
				return
			case action == shell.MenuOpenWith && len(paths) == 1:
				w.choose(paths[0], false)
				return
			}
			w.refresh(t)
		})
	}()
}

// reload reads the folder shown again, and the icons of its entries.
func (w *Window) reload(t *Tab) {
	if real(t.Path) {
		w.app.icons.forget(t.Path)
	}
	w.refresh(t)
	if t.Path == fsys.Computer {
		w.loadSidebar()
	}
}

// setHidden shows the files the system hides, or leaves them out, in
// every tab.
func (w *Window) setHidden(show bool) {
	a := w.app
	a.set.ShowHidden = show
	a.saveSoon()
	for _, win := range a.windows {
		for _, t := range win.tabs {
			if t.Path != "" {
				t.review(show, a.set.DirsFirst)
			}
		}
		win.invalidate()
	}
}
