package app

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"myfiles/internal/cli"
	"myfiles/internal/clock"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
	"myfiles/internal/watch"
)

// navMode is how a tab comes to a folder, which says what its history
// does.
type navMode uint8

const (
	// navPush goes to a folder from the one shown, which Back returns to.
	navPush navMode = iota
	// navBack and navForward go along the history.
	navBack
	navForward
	// navReplace shows a folder in place of the one shown: a new tab's
	// first, or the same one read again.
	navReplace
)

// syncWait is how long a tab waits, on the main thread, for a folder the
// user asked for: one read within it shows in the very frame of the click
// or the key that asked, where a frame drawn meanwhile would put it a
// refresh later. A folder of a local disk is read in a fraction of it;
// one that takes longer, as on a network or a disk asleep, is waited for
// off the main thread, and the window stays live.
const syncWait = 40 * time.Millisecond

// navigate shows a folder in a tab. focus names the entry to choose
// there. Called while the view builds, it runs once the view is built,
// and the view builds again with the folder: what builds the rows never
// sees them change under it.
func (w *Window) navigate(t *Tab, path string, mode navMode, focus string) {
	if path == "" {
		return
	}
	if w.building {
		w.defer_(func() {
			if w.tabs[t.ID] == t {
				w.navigate(t, path, mode, focus)
			}
		})
		return
	}
	t.gen++
	gen := t.gen
	if t.cancel != nil {
		t.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.loading, t.reload = true, false
	t.asked = clock.Now()
	same := path == t.Path
	if !same {
		t.filter = ""
	}
	t.editing = false
	set := &w.app.set
	showHidden, filter, order := set.ShowHidden, t.filter, t.order(set.DirsFirst)
	from := visit{path: t.Path, focus: t.focusName()}
	asked := t.asked

	done := make(chan *loaded, 1)
	go func() {
		if path == shell.RecycleBin {
			done <- w.app.loadTrash(filter, order)
			return
		}
		done <- load(ctx, path, showHidden, filter, order)
	}()
	arrive := func(l *loaded) {
		if t.gen != gen || w.tabs[t.ID] != t {
			return
		}
		cancel()
		t.cancel = nil
		t.trace.loaded, t.trace.arrived = clock.Now(), clock.Now()
		w.arrive(t, l, mode, from, focus, asked)
	}
	wait := syncWait
	if same {
		// Read again behind the user's back, as after a change: never
		// worth holding the window for.
		wait = 0
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case l := <-done:
			timer.Stop()
			arrive(l)
			return
		case <-timer.C:
		}
	}
	go func() {
		l := <-done
		w.update(func() { arrive(l) })
	}()
}

// arrive shows a folder that was read, on the main thread.
func (w *Window) arrive(t *Tab, l *loaded, mode navMode, from visit, focus string, asked clock.Time) {
	t.loading = false
	same := l.path == t.Path
	if l.err != nil && same {
		// The folder shown is gone, as one deleted or on a drive taken
		// out: the nearest folder above it that is still there.
		w.retreat(t)
		return
	}
	switch mode {
	case navPush:
		if from.path != "" && !same {
			t.back = append(t.back, from)
			t.forward = nil
		}
	case navBack:
		if n := len(t.back); n > 0 {
			t.back = t.back[:n-1]
		}
		t.forward = append(t.forward, from)
	case navForward:
		if n := len(t.forward); n > 0 {
			t.forward = t.forward[:n-1]
		}
		t.back = append(t.back, from)
	}
	const historyMax = 200
	if len(t.back) > historyMax {
		t.back = t.back[len(t.back)-historyMax:]
	}

	t.Path = l.path
	if l.err == nil {
		// The sidebar follows: its tree shows the folders read, and its
		// recent folders count the visit.
		w.app.listed(l.path, l)
		if !same && mode != navReplace {
			w.app.visited(l.path)
		}
	}
	t.entries, t.view, t.volumes, t.trash = l.entries, l.view, l.volumes, l.trash
	t.text = make([]rowText, len(l.entries))
	t.err, t.hidden, t.listed = l.err, l.hidden, l.took
	t.sorted, t.filtered = t.sort, t.filter
	t.selStale = true
	if same {
		// Read again: the choice stays with the entries still there,
		// and the rows stay where they are scrolled to.
		t.prune()
	} else {
		columns := t.list.Columns
		t.list = ui.ListState{Columns: columns}
		t.grid = ui.GridState{}
		t.renaming = ""
		t.sel.Clear()
		t.cursor = -1
		t.wantFocus = true
		t.shownAt = asked
		if l.err != nil {
			// Nothing of it will show to be timed.
			t.shownAt = 0
		}
	}
	if focus != "" {
		t.choose(focus)
	}
	if !t.reload && (len(t.chooseAfter) > 0 || t.renameAfter != "") {
		// What an operation made here: chosen, and the one to name.
		if len(t.chooseAfter) > 0 {
			t.choose(t.chooseAfter...)
		}
		t.rename = t.renameAfter
		t.chooseAfter, t.renameAfter = nil, ""
	}
	if !same || t.unwatch == nil {
		w.watch(t)
	}
	if p := w.layout.PaneOf(t.ID); !same && l.err == nil && p != nil && w.activeTab(p) == t {
		// The tab shows: its first frame of the folder has the icons and
		// the type names, where they come soon enough.
		w.app.icons.await(iconWait, func() { w.foresee(t) })
	}
	if t.reload {
		t.reload = false
		w.refresh(t)
	}
	w.changed()
}

// foresee asks for the icons and the type names of the entries a folder's
// first frame is about to show: as many as the tab's last frame built,
// from the top, or around the entry chosen, which the view scrolls to.
func (w *Window) foresee(t *Tab) {
	a := w.app
	n := t.span + 8
	if t.span == 0 {
		// A tab that has not shown yet: a screenful.
		n = 80
	}
	n = min(n, 400, len(t.view))
	first := 0
	if t.cursor >= 0 {
		first = t.cursor - n/2
	}
	first = max(min(first, len(t.view)-n), 0)
	set := a.icons.at(viewSpecs[t.mode].icon)
	names := real(t.Path) && (t.mode == viewDetails || t.mode == viewTiles)
	// The last asked for is the first answered: from the bottom up, for
	// the top rows to come first.
	for row := first + n - 1; row >= first; row-- {
		e := t.entry(row)
		set.forEntry(t.Path, e)
		if names {
			a.icons.typeName(e)
		}
	}
}

// prune takes out of the choice the names no entry has any more.
func (t *Tab) prune() {
	if t.sel.Len() == 0 {
		return
	}
	names := make(map[string]bool, len(t.view))
	for _, i := range t.view {
		names[t.entries[i].Name] = true
	}
	var gone []string
	for name := range t.sel.All() {
		if !names[name] {
			gone = append(gone, name)
		}
	}
	for _, name := range gone {
		t.sel.Remove(name)
	}
	if t.cursor >= len(t.view) {
		t.cursor = len(t.view) - 1
	}
}

// retreat goes to the nearest folder above the one shown that exists.
func (w *Window) retreat(t *Tab) {
	path := t.Path
	go func() {
		for {
			parent, ok := fsys.Parent(path)
			if !ok {
				path = fsys.Computer
				break
			}
			path = parent
			if e, err := fsys.Stat(path); err == nil && e.IsDir() {
				break
			}
		}
		w.update(func() {
			if w.tabs[t.ID] == t {
				w.navigate(t, path, navReplace, "")
			}
		})
	}()
}

// refresh reads the folder shown again, keeping the choice and the place.
func (w *Window) refresh(t *Tab) {
	if t.Path == "" {
		return
	}
	if t.loading {
		// Once the read under way ends.
		t.reload = true
		return
	}
	w.navigate(t, t.Path, navReplace, "")
}

// watch follows the changes of the folder shown, reading it again as
// they come, and going where the folder goes when it is renamed.
//
// A folder of a drive is watched by the app's hub, from the root of the
// drive, which holds no folder: a watch of the folder itself keeps every
// folder above it from being renamed, moved or deleted to the Recycle
// Bin, by this program and by any other. Folders of a network, and of
// drives that can be taken out, are watched themselves, as nothing else
// tells of them.
func (w *Window) watch(t *Tab) {
	if t.unwatch != nil {
		t.unwatch()
		t.unwatch = nil
	}
	if t.err != nil || !real(t.Path) && !t.bin() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.unwatch = cancel
	path := t.Path
	// watched is the folder as the watch knows it, on the main thread: it
	// goes where the folder goes, and the watch with it.
	watched := path
	a := w.app
	// told runs fn on the main thread, while the tab shows the folder
	// still and this watch is the one it has.
	told := func(fn func()) {
		w.update(func() {
			if w.tabs[t.ID] == t && t.Path == watched && ctx.Err() == nil {
				fn()
			}
		})
	}
	go func() {
		if path == shell.RecycleBin {
			// What it holds is in a folder on each drive: something made
			// or gone there is something deleted, put back or emptied, by
			// any program.
			for _, dir := range a.desk.trashFolders() {
				if stop, ok := a.hub.Watch(dir, func(watch.Event) { told(func() { w.refresh(t) }) }); ok {
					defer stop()
				}
			}
			<-ctx.Done()
			return
		}
		stop, ok := a.hub.Watch(path, func(ev watch.Event) {
			told(func() {
				switch ev.Kind {
				case watch.Moved:
					// The watch goes on under the new path: renamed again
					// at once, the folder is followed again.
					watched = ev.To
					w.follow(t, ev.To)
				case watch.Lost:
					// Watched by no one: the folder read again is watched
					// again, some way.
					t.unwatch()
					t.unwatch = nil
					w.refresh(t)
				default:
					// Changed, or gone, which reading it tells.
					w.refresh(t)
				}
			})
		})
		if ok {
			<-ctx.Done()
			stop()
			return
		}
		files, err := mygo.WatchFiles(ctx, path, mygo.FileWatchOptions{
			HintsOnly: true,
			Debounce:  80 * time.Millisecond,
			MaxDelay:  400 * time.Millisecond,
		})
		if err != nil {
			return
		}
		defer files.Close()
		for {
			_, err := files.Next(ctx)
			if ctx.Err() != nil {
				return
			}
			told(func() { w.refresh(t) })
			if err != nil {
				return
			}
		}
	}()
}

// follow takes a tab where its folder went: renamed, or in a folder that
// was, it is the same folder under another path, with the same entries
// chosen and the rows where they were.
func (w *Window) follow(t *Tab, to string) {
	from := t.Path
	if to == "" || to == from {
		return
	}
	w.app.renamed(from, to)
	t.Path = to
	// What is being read of the old path is of no use.
	t.loading = false
	w.refresh(t)
	w.changed()
}

// renamed puts the new path of a folder in the history of every tab, for
// the ways back and forward to lead where the folders are now. What was
// renamed is the folder at from or one above it: the part that from and
// to do not share.
func (a *App) renamed(from, to string) {
	// D:\a\old\x\y and D:\a\new\x\y: D:\a\old became D:\a\new.
	for {
		pf, okf := fsys.Parent(from)
		pt, okt := fsys.Parent(to)
		if !okf || !okt || !strings.EqualFold(fsys.Base(from), fsys.Base(to)) {
			break
		}
		from, to = pf, pt
	}
	under := strings.ToLower(strings.TrimRight(from, `\`)) + `\`
	move := func(path string) string {
		switch lower := strings.ToLower(path); {
		case lower+`\` == under:
			return to
		case strings.HasPrefix(lower, under):
			return strings.TrimRight(to, `\`) + `\` + path[len(under):]
		}
		return path
	}
	for _, w := range a.windows {
		for _, t := range w.tabs {
			for i := range t.back {
				t.back[i].path = move(t.back[i].path)
			}
			for i := range t.forward {
				t.forward[i].path = move(t.forward[i].path)
			}
		}
	}
}

// up goes to the folder holding the one shown, with it chosen there.
func (w *Window) up(t *Tab) {
	if t.Path == fsys.Computer || t.Path == "" {
		return
	}
	parent, ok := fsys.Parent(t.Path)
	if !ok || !real(t.Path) {
		w.navigate(t, fsys.Computer, navPush, t.Path)
		return
	}
	w.navigate(t, parent, navPush, fsys.Base(t.Path))
}

func (w *Window) goBack(t *Tab) {
	if n := len(t.back); n > 0 {
		w.navigate(t, t.back[n-1].path, navBack, t.back[n-1].focus)
	}
}

func (w *Window) goForward(t *Tab) {
	if n := len(t.forward); n > 0 {
		w.navigate(t, t.forward[n-1].path, navForward, t.forward[n-1].focus)
	}
}

// openEntry opens an entry: a directory in the tab, a file with its
// program.
func (w *Window) openEntry(t *Tab, e *fsys.Entry) {
	path := t.pathOf(e)
	if e.IsDir() {
		w.navigate(t, path, navPush, "")
		return
	}
	w.openFile(path, t.Path)
}

// goTo shows what the user typed in the address bar: a folder, or a
// file, which is chosen in its folder.
func (w *Window) goTo(t *Tab, text string) {
	cwd := t.Path
	if !real(cwd) {
		cwd = ""
	}
	type found struct {
		target cli.Target
		err    error
	}
	done := make(chan found, 1)
	go func() {
		target, err := cli.Find(text, cwd)
		done <- found{target, err}
	}()
	show := func(f found) {
		if w.tabs[t.ID] != t {
			return
		}
		if f.err != nil {
			w.say(f.err.Error())
			return
		}
		w.navigate(t, f.target.Path, navPush, f.target.Select)
	}
	// Looking at a path of a local disk takes no time worth a frame;
	// one that takes long, as on a network, is waited for elsewhere.
	timer := time.NewTimer(syncWait)
	select {
	case f := <-done:
		timer.Stop()
		show(f)
	case <-timer.C:
		go func() {
			f := <-done
			w.update(func() { show(f) })
		}()
	}
}
