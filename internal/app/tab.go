package app

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"cairn/internal/clock"
	"cairn/internal/fsys"
	"cairn/internal/workspace"
)

// The IDs of the columns of files.
const (
	colName     = "name"
	colModified = "modified"
	colType     = "type"
	colSize     = "size"
)

// visit is a place in a tab's history: a folder, and the entry the cursor
// was on when the tab left it, which is chosen when the tab comes back.
type visit struct {
	path, focus string
}

// rowText is what a row shows of an entry besides its name, made the
// first time the row comes into view and kept while the listing lasts.
type rowText struct {
	icon *ui.Bitmap
	// iconSize is the size icon is of, in DIPs: another view asks anew.
	iconSize int
	modified string
	size     string
	made     bool
}

// Tab is a folder shown in a pane, with the way there and back.
type Tab struct {
	ID workspace.TabID
	// Path is the folder shown, fsys.Computer for the list of drives.
	Path string

	// entries are the folder's, in the order read, and view the rows:
	// indices into entries, filtered and sorted. text is parallel to
	// entries.
	entries []fsys.Entry
	view    []int32
	text    []rowText
	// volumes are the drives entries stand for when Path is
	// fsys.Computer, parallel to entries.
	volumes []fsys.Volume
	// err is why the folder could not be read.
	err error
	// hidden counts the entries the view leaves out as hidden.
	hidden int
	// listed is how long reading and sorting the folder took, and shown
	// how long it took from asking for the folder to its rows being on
	// screen.
	listed, shown time.Duration

	// mode is the view the folder shows in: list is the table's state,
	// and grid that of the other views.
	mode viewMode
	list ui.ListState
	grid ui.GridState
	// scrollGrid brings the cursor into view in the grid's next frame.
	scrollGrid bool
	// renaming is the name of the entry being renamed in a grid, draft
	// what is typed for it, and renameFresh set until its text took the
	// focus.
	renaming, draft string
	renameFresh     bool
	sel             ui.Selection[string]
	cursor          int
	sort            ui.SortOrder
	// sorted is the order view is in, to tell when the header changed
	// sort.
	sorted ui.SortOrder
	filter string
	// filtered is the filter view was made with, in lower case.
	filtered string
	// selBytes and selDirs are the size of the files chosen and the
	// number of directories among them, for the status bar; selStale
	// asks for them anew.
	selBytes int64
	selDirs  int
	selStale bool

	back, forward []visit

	// gen counts the folders asked for: what a goroutine brings back for
	// another count is dropped.
	gen     uint64
	cancel  context.CancelFunc
	loading bool
	// asked is when the folder being loaded was asked for.
	asked clock.Time
	// trace are the moments of the last folder's way to the screen,
	// printed when CAIRN_TRACE is set.
	trace struct{ loaded, arrived, built clock.Time }
	// unwatch stops watching the folder shown.
	unwatch context.CancelFunc
	// reload asks for the folder again once the load under way ends:
	// something changed in it meanwhile.
	reload bool

	// editing shows the address as a text input holding addr; fresh is
	// set until the input took the focus and selected its text.
	editing bool
	fresh   bool
	addr    string
	// addressRef and filterRef are the address and the filter field, to
	// measure the one and focus the other.
	addressRef, filterRef ui.Handle
	// wantFocus gives the rows the keyboard once they are built.
	wantFocus bool
	// rename is the name of the entry to start renaming once it shows.
	rename string
	// chooseAfter are the names to choose once the folder is read
	// again, as files just pasted, and renameAfter the one to rename.
	chooseAfter []string
	renameAfter string
	// shownAt is when a folder was asked for whose rows have not
	// reached the screen yet.
	shownAt clock.Time
}

// title is what the tab's header shows.
func (t *Tab) title() string {
	if t.Path == fsys.Computer {
		return "This PC"
	}
	return fsys.Base(t.Path)
}

// entry is the entry of a row.
func (t *Tab) entry(row int) *fsys.Entry { return &t.entries[t.view[row]] }

// pathOf is the path of an entry of the folder shown.
func (t *Tab) pathOf(e *fsys.Entry) string {
	if t.Path == fsys.Computer {
		return e.Name
	}
	return fsys.Join(t.Path, e.Name)
}

// rowOf is the row showing the entry of a name, -1 when none does.
func (t *Tab) rowOf(name string) int {
	for row, i := range t.view {
		if t.entries[i].Name == name {
			return row
		}
	}
	return -1
}

// chosen lists the entries chosen, in the order shown.
func (t *Tab) chosen() []*fsys.Entry {
	if t.sel.Len() == 0 {
		return nil
	}
	out := make([]*fsys.Entry, 0, t.sel.Len())
	for _, i := range t.view {
		if e := &t.entries[i]; t.sel.Has(e.Name) {
			out = append(out, e)
		}
	}
	return out
}

// chosenPaths lists the paths of the entries chosen, in the order shown.
func (t *Tab) chosenPaths() []string {
	entries := t.chosen()
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = t.pathOf(e)
	}
	return paths
}

// choose makes the entries of the names the choice, and puts the cursor
// on the first, scrolled into view.
func (t *Tab) choose(names ...string) {
	t.sel.Clear()
	t.selStale = true
	first := -1
	for _, name := range names {
		if row := t.rowOf(name); row >= 0 {
			// The name as the folder has it, which may differ in case
			// from the one asked for.
			t.sel.Add(t.entries[t.view[row]].Name)
			if first < 0 || row < first {
				first = row
			}
		} else if row := t.rowOfFold(name); row >= 0 {
			t.sel.Add(t.entries[t.view[row]].Name)
			if first < 0 || row < first {
				first = row
			}
		}
	}
	if first >= 0 {
		t.cursor = first
		t.list.Choose(first)
		t.list.ScrollTo(first, ui.Center)
		t.scrollGrid = true
	}
}

func (t *Tab) rowOfFold(name string) int {
	for row, i := range t.view {
		if fsys.SameName(t.entries[i].Name, name) {
			return row
		}
	}
	return -1
}

// focusName is the name of the entry under the cursor, for the history.
func (t *Tab) focusName() string {
	if t.cursor >= 0 && t.cursor < len(t.view) && t.sel.Len() > 0 {
		return t.entry(t.cursor).Name
	}
	return ""
}

// order is how the tab sorts, as fsys takes it.
func (t *Tab) order(dirsFirst bool) fsys.Order {
	o := fsys.Order{Descending: t.sort.Descending, DirsFirst: dirsFirst}
	switch t.sort.Column {
	case colSize:
		o.By = fsys.BySize
	case colModified:
		o.By = fsys.ByModified
	case colType:
		o.By = fsys.ByType
	}
	return o
}

// buildView makes the rows of entries: those the filter and the hidden
// setting let through, sorted. It runs off the main thread for a folder
// just read, and on it when the filter or the order changes.
func buildView(entries []fsys.Entry, showHidden bool, filter string, o fsys.Order) (view []int32, hidden int) {
	filter = strings.ToLower(filter)
	view = make([]int32, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if !showHidden && e.Hidden() {
			hidden++
			continue
		}
		if filter != "" && !containsFold(e.Name, filter) {
			continue
		}
		view = append(view, int32(i))
	}
	fsys.Sort(entries, view, o)
	return view, hidden
}

// review makes the rows again from the entries the tab has, as after the
// filter or the order changed, keeping the choice.
func (t *Tab) review(showHidden, dirsFirst bool) {
	if t.Path == fsys.Computer {
		t.view = fsys.All(t.entries)
		t.hidden = 0
	} else {
		t.view, t.hidden = buildView(t.entries, showHidden, t.filter, t.order(dirsFirst))
	}
	t.sorted, t.filtered = t.sort, t.filter
	t.selStale = true
	if t.cursor >= len(t.view) {
		t.cursor = len(t.view) - 1
	}
}

// measure counts what is chosen, for the status bar.
func (t *Tab) measure() {
	t.selStale = false
	t.selBytes, t.selDirs = 0, 0
	if t.sel.Len() == 0 {
		return
	}
	for _, i := range t.view {
		e := &t.entries[i]
		if !t.sel.Has(e.Name) {
			continue
		}
		if e.IsDir() {
			t.selDirs++
		} else {
			t.selBytes += e.Size
		}
	}
}

// loaded is a folder read off the main thread, ready to show.
type loaded struct {
	path    string
	entries []fsys.Entry
	view    []int32
	volumes []fsys.Volume
	hidden  int
	err     error
	took    time.Duration
}

// load reads a folder and makes its rows.
func load(ctx context.Context, path string, showHidden bool, filter string, o fsys.Order) *loaded {
	start := clock.Now()
	l := &loaded{path: path}
	if path == fsys.Computer {
		l.volumes = fsys.Volumes()
		l.entries = make([]fsys.Entry, len(l.volumes))
		for i, v := range l.volumes {
			l.entries[i] = fsys.Entry{Name: v.Root, Attr: fsys.AttrDir, Size: v.Total}
		}
		l.view = fsys.All(l.entries)
	} else {
		listing, err := fsys.List(ctx, path)
		if err != nil {
			l.err = err
		} else {
			l.entries = listing.Entries
			l.view, l.hidden = buildView(l.entries, showHidden, filter, o)
		}
	}
	l.took = clock.Since(start)
	return l
}

func containsFoldSlow(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), sub)
}
