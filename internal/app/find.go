package app

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/clock"
	"myfiles/internal/config"
	"myfiles/internal/fsys"
	"myfiles/internal/index"
	"myfiles/internal/search"
	"myfiles/internal/shell"
)

// A search shows in the tab it was typed in, in place of the folder: its
// rows are what was found, each named by its path, which tells two files
// of one name apart, and shown by its name with the folder it is in
// beside it. The folder is still the tab's: Escape, or an empty query,
// shows it again.

// finding is a tab's search.
type finding struct {
	// origin is the folder the tab showed, which it shows again when the
	// search ends, and everywhere searches the whole computer, not it.
	origin     string
	everywhere bool
	// kind is which kind of file shows, of kinds; 0 for all.
	kind int
	// asked is the query the rows are for, with its scope and kind; "" at
	// the start.
	asked string
	// quiet is set for the next search when it is run again as the files
	// changed, not asked for.
	quiet bool
	// content is set for a search inside files: read counts the files
	// read so far, of toRead, and snippets are the text around what was
	// found, by the files' paths.
	content  bool
	read     *atomic.Int64
	toRead   int
	snippets map[string]string
	// learned is the path of the result that shows because it was opened
	// for this query before, not because it matches; "" for none.
	learned string
	// renaming is the path of the result whose name is being edited, and
	// renameBuilt how often its row was built since.
	renaming    string
	renameBuilt int
	query       *search.Query
	// found are what entries stand for, parallel to them.
	found []index.Result
	// total counts all that matches, of which found are the best, and
	// limit is how many are asked for.
	total, limit int
	// reading is set while drives are still read for the first time, and
	// slow while a folder no index covers is read for this search.
	reading, slow bool
	// took is how long the last search took.
	took time.Duration
	note string
	// mode is the view the tab showed its folder in, which it shows it
	// in again: what is found shows as a table, with the folders beside
	// the names, unless another view is asked for.
	mode viewMode
	gen  uint64
	stop context.CancelFunc
	// sort is the column the results are ordered by, none for how well
	// they match; sorted the order they are in.
	sort, sorted ui.SortOrder
	scratch      search.Scratch
}

// kinds are the kinds of file a search can be narrowed to: what the
// buttons under the toolbar show.
var kinds = [...]struct {
	title func() string
	rule  string
}{
	{locales.KindAll, ""},
	{locales.KindFolders, ":f"},
	{locales.KindDocuments, "@doc"},
	{locales.KindImages, "@img"},
	{locales.KindVideo, "@video"},
	{locales.KindAudio, "@audio"},
	{locales.KindArchives, "@zip"},
	{locales.KindPrograms, "@app"},
}

// firstResults is how many results a search asks for at first, and
// moreResults how many times more each asking for more.
const (
	firstResults = 2000
	moreResults  = 8
)

// searchPath is the Path of a tab while it shows what a search found: no
// folder, so that what is done to folders is not done to it.
const searchPath = "::search"

// resultDir is the folder a result is in.
func resultDir(r *index.Result) string {
	if isProgram(r.Path) {
		// No file: of no folder.
		return ""
	}
	dir := r.Path[:len(r.Path)-len(r.Name)]
	if len(dir) > 3 {
		// All but a drive's root are named without the separator.
		dir = dir[:len(dir)-1]
	}
	return dir
}

// where is the folder a result is in as its row tells it: from the folder
// searched in, for a search of one, which says the more in the less room.
func (t *Tab) where(r *index.Result) string {
	dir := resultDir(r)
	f := t.find
	if f.everywhere || !real(f.origin) {
		return dir
	}
	if len(dir) >= len(f.origin) && strings.EqualFold(dir[:len(f.origin)], f.origin) {
		return strings.TrimLeft(dir[len(f.origin):], string(filepath.Separator))
	}
	return dir
}

// searching tells whether the tab shows what a search found.
func (t *Tab) searching() bool { return t.find != nil }

// searchOptions are how queries are read, as the settings have it.
func (a *App) searchOptions() search.Options {
	s := &a.set.Search
	return search.Options{Fuzzy: s.Fuzzy, OrFirst: s.OrFirst, Pinyin: s.Pinyin}
}

// startIndex brings the indexes of the drives up, where the settings
// have them kept.
func (a *App) startIndex() {
	if a.finder != nil {
		return
	}
	cfg := index.Config{Dir: index.DefaultDir(), Rules: a.rules(), Expand: fsys.ExpandEnv,
		Off: a.set.Search.Off, Extra: a.set.Search.Extra, Changed: a.indexChanged}
	a.finder = index.NewManager(cfg)
	// An instance that leaves no trace keeps no index either, unless
	// MYFILES_INDEX asks for one, as for a look at searching.
	if a.set.Search.Index && cfg.Dir != "" && (!a.ephemeral || os.Getenv("MYFILES_INDEX") != "") {
		a.finder.Start()
	}
}

// rules are the user's rules, the defaults until they were changed.
func (a *App) rules() []index.Rule {
	if !a.set.Search.RulesSet {
		return index.DefaultRules()
	}
	return parseRules(a.set.Search.Rules)
}

// parseRules reads rules as the settings show them: a line a rule, with
// what it does before it.
func parseRules(lines []string) []index.Rule {
	var out []index.Rule
	for _, line := range lines {
		line = strings.TrimSpace(line)
		r := index.Rule{Effect: index.Normal}
		switch {
		case strings.HasPrefix(line, `\`) && len(line) > 1 && strings.ContainsRune(`!-+\`, rune(line[1])) && !strings.HasPrefix(line, `\\`):
			line = line[1:]
		case strings.HasPrefix(line, "!"):
			r.Effect, line = index.Exclude, line[1:]
		case strings.HasPrefix(line, "++"):
			r.Effect, r.Top, line = index.High, true, line[2:]
		case strings.HasPrefix(line, "+"):
			r.Effect, line = index.High, line[1:]
		case strings.HasPrefix(line, "-"):
			r.Effect, line = index.Low, line[1:]
		}
		if r.Pattern = strings.TrimSpace(line); r.Pattern != "" {
			out = append(out, r)
		}
	}
	return out
}

// formatRules writes rules as parseRules reads them.
func formatRules(rules []index.Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		prefix := ""
		switch {
		case r.Effect == index.Exclude:
			prefix = "!"
		case r.Effect == index.Low:
			prefix = "-"
		case r.Effect == index.High && r.Top:
			prefix = "++"
		case r.Effect == index.High:
			prefix = "+"
		case strings.ContainsRune(`!-+`, rune(r.Pattern[0])):
			prefix = `\`
		}
		out[i] = prefix + r.Pattern
	}
	return out
}

// indexChanged is told, from any goroutine, that an index changed: the
// searches showing are run again, a moment later, once for many changes.
func (a *App) indexChanged() {
	a.saveMu.Lock()
	pending := a.refinding
	a.refinding = true
	a.saveMu.Unlock()
	if pending {
		return
	}
	// A search that takes long is not run again for every file that
	// changes on a busy drive: the longer it took, the longer until the
	// next.
	a.saveMu.Lock()
	wait := max(refindAfter, time.Until(a.refound.Add(min(50*a.refindTook, refindAtMost))))
	a.saveMu.Unlock()
	time.AfterFunc(wait, func() {
		a.desk.post(func() {
			var took time.Duration
			for _, w := range a.windows {
				for _, t := range w.tabs {
					if p := w.layout.PaneOf(t.ID); t.searching() && !t.find.content && p != nil && w.activeTab(p) == t {
						took = max(took, t.find.took)
						t.find.quiet = true
						w.search(t, true)
					}
				}
				// The settings show the drives' states.
				w.invalidate()
			}
			a.saveMu.Lock()
			a.refinding = false
			a.refound, a.refindTook = time.Now(), took
			a.saveMu.Unlock()
		})
	})
}

// refindAfter is how long after a change of the files the searches
// showing are run again, and refindAtMost the longest a costly one waits
// for its next run.
const (
	refindAfter  = 350 * time.Millisecond
	refindAtMost = 15 * time.Second
)

// history are the files opened from results, as searches take them: each
// with what it counts, which halves in 90 days.
func (a *App) history() map[string]int {
	if !a.set.Search.History || len(a.opened) == 0 {
		return nil
	}
	now := time.Now().Unix()
	out := make(map[string]int, len(a.opened))
	for path, o := range a.opened {
		days := float64(max(now-o.Last, 0)) / 86400
		if score := int(math.Round(o.Score * math.Pow(0.5, days/90) * 100)); score >= 100 {
			out[path] = score
		}
	}
	return out
}

// noteOpened counts a file opened from the results of a search, for the
// search it was opened from.
func (a *App) noteOpened(f *finding, path string) {
	if !a.set.Search.History || a.ephemeral {
		return
	}
	// One that showed because it was opened for this query before says
	// nothing new of the query.
	if f != nil && f.query != nil && f.learned != path {
		a.learn(f.query.Text, path)
	}
	if a.opened == nil {
		a.opened = config.LoadOpened()
	}
	now := time.Now().Unix()
	o := a.opened[path]
	days := float64(max(now-o.Last, 0)) / 86400
	o.Score = min(o.Score*math.Pow(0.5, days/90)+1, 10)
	o.Last = now
	a.opened[path] = o
	if len(a.opened) > 2000 {
		// The least of them go.
		for p, old := range a.opened {
			if old.Score < 1.5 && now-old.Last > 30*86400 {
				delete(a.opened, p)
			}
		}
	}
	opened := make(map[string]config.Opened, len(a.opened))
	for p, o := range a.opened {
		opened[p] = o
	}
	go func() { _ = config.SaveOpened(opened) }()
}

// setQuery takes what the search field holds: a search of it, or the
// folder again when it holds nothing.
func (w *Window) setQuery(t *Tab) {
	if strings.TrimSpace(t.filter) == "" && (t.find == nil || t.find.kind == 0) {
		w.endSearch(t)
		return
	}
	if t.find == nil {
		t.find = &finding{origin: t.Path, everywhere: t.everywhere, limit: firstResults, mode: t.mode}
		t.Path = searchPath
		t.mode = viewDetails
		// The folder's rows give way to what is found, which most often
		// is there within this frame.
		t.entries, t.view, t.text, t.volumes = nil, nil, nil, nil
		t.sel.Clear()
		t.cursor = -1
		if t.unwatch != nil {
			t.unwatch()
			t.unwatch = nil
		}
		// What is being read of the folder is of no use, and what changes
		// in it shows when the search ends.
		t.gen++
		if t.cancel != nil {
			t.cancel()
			t.cancel = nil
		}
		t.loading = false
	}
	w.search(t, false)
}

// endSearch shows the tab's folder again.
func (w *Window) endSearch(t *Tab) {
	f := t.find
	if f == nil {
		return
	}
	if f.stop != nil {
		f.stop()
	}
	t.find, t.filter = nil, ""
	// The next search is of the folder again, unless asked otherwise.
	t.everywhere, t.mode = false, f.mode
	t.entries, t.view, t.text = nil, nil, nil
	t.sel.Clear()
	t.cursor = -1
	// Read as a folder come to anew: the rows start at the top.
	t.Path = ""
	w.navigate(t, f.origin, navReplace, "")
	w.changed()
}

// searchWait is how long the window waits for a search: one that is done
// within it shows in the frame of the key that asked for it.
const searchWait = 12 * time.Millisecond

// search runs the tab's search and shows what it finds. again is set for
// a search run again as the files changed: the choice and the place stay.
func (w *Window) search(t *Tab, again bool) {
	f := t.find
	if f == nil {
		return
	}
	if w.building {
		w.defer_(func() {
			if w.tabs[t.ID] == t {
				w.search(t, again)
			}
		})
		return
	}
	a := w.app
	text := strings.TrimSpace(t.filter)
	asked := text + "\x00" + f.origin + "\x00" + string(rune('0'+f.kind))
	if f.everywhere {
		asked = text + "\x00\x00" + string(rune('0'+f.kind))
	}
	if !again && asked == f.asked {
		return
	}
	if asked != f.asked {
		f.limit = firstResults
	}
	f.asked = asked
	if f.stop != nil {
		f.stop()
	}
	f.gen++
	gen := f.gen
	ctx, cancel := context.WithCancel(context.Background())
	f.stop = cancel

	if rest, ok := a.contentQuery(text); ok {
		w.searchContent(t, gen, ctx, cancel, rest)
		return
	}
	f.content, f.snippets = false, nil
	filters := a.filters()
	q := search.ParseQuery(text, a.searchOptions(), ':', filters)
	req := &index.Request{Query: q, Limit: f.limit, ShowHidden: a.set.ShowHidden,
		Favorites: a.set.Pinned, History: a.history(), Background: f.quiet}
	f.quiet = false
	if !f.everywhere && real(f.origin) {
		req.Scope = f.origin
	}
	if list := a.kindList(); f.kind >= len(list) {
		// A kind of the user's that is no more.
		f.kind = 0
	} else if rule := list[f.kind].rule; rule != "" {
		req.Filter = search.NewFilter(rule, filters)
	}
	type answer struct {
		found index.Found
		slow  bool
		took  time.Duration
	}
	start := clock.Now()
	done := make(chan answer, 1)
	slowly := make(chan struct{})
	a.startIndex()
	go func() {
		found := a.finder.Search(ctx, req)
		slow := false
		if found.Uncovered && ctx.Err() == nil {
			// No index has the folder: it is read now, for this search
			// and those that follow.
			close(slowly)
			slow = true
			found, _ = a.finder.SearchFolder(ctx, req)
		}
		done <- answer{found, slow, clock.Since(start)}
	}()
	show := func(ans answer) {
		if w.tabs[t.ID] != t || t.find != f || f.gen != gen || ctx.Err() != nil {
			return
		}
		cancel()
		f.stop = nil
		f.query, f.slow, f.took = q, false, ans.took
		a.addPrograms(f, q, &ans.found, req)
		f.learned = a.recall(f, &ans.found, req.Filter)
		f.reading = ans.found.Reading
		f.note = ""
		if q.Unknown {
			f.note = locales.SearchUnknownToken()
		}
		w.found(t, ans.found, again)
	}
	timer := time.NewTimer(searchWait)
	select {
	case ans := <-done:
		timer.Stop()
		show(ans)
		return
	case <-timer.C:
	}
	go func() {
		select {
		case <-slowly:
			w.update(func() {
				if t.find == f && f.gen == gen {
					f.slow = true
				}
			})
		case <-ctx.Done():
			return
		}
	}()
	go func() {
		ans := <-done
		w.update(func() { show(ans) })
	}()
}

// found shows what a search found as the tab's rows.
func (w *Window) found(t *Tab, found index.Found, again bool) {
	f := t.find
	f.found, f.total = found.Results, found.Total
	entries := make([]fsys.Entry, len(f.found))
	for i := range f.found {
		r := &f.found[i]
		e := fsys.Entry{Name: r.Path, Size: int64(r.Size), ModTime: int64(r.Modified) * 1e9, Created: int64(r.Created) * 1e9}
		if r.Flags&index.FlagDir != 0 {
			e.Attr |= fsys.AttrDir
			e.Size = 0
		}
		if r.Flags&index.FlagHidden != 0 {
			e.Attr |= fsys.AttrHidden
		}
		if r.Flags&index.FlagSystem != 0 {
			e.Attr |= fsys.AttrSystem
		}
		if r.Flags&index.FlagLink != 0 {
			e.Attr |= fsys.AttrLink
		}
		if r.Flags&index.FlagOffline != 0 {
			e.Attr |= fsys.AttrOffline
		}
		entries[i] = e
	}
	t.entries = entries
	t.text = make([]rowText, len(entries))
	t.volumes, t.trash = nil, nil
	t.err, t.hidden, t.loading = nil, 0, false
	t.selStale = true
	f.sorted = ui.SortOrder{}
	t.findView()
	if again {
		t.prune()
	} else {
		columns := t.list.Columns
		t.list = ui.ListState{Columns: columns}
		t.grid = ui.GridState{}
		t.renaming = ""
		t.sel.Clear()
		t.cursor = -1
	}
	if t.unwatch != nil {
		t.unwatch()
		t.unwatch = nil
	}
	w.changed()
}

// The least times a file is opened from a query's results, of the user's
// own choosing, for the query to find it whatever it is called, and the
// most files kept for one query.
const (
	learnedAfter  = 3
	learnedAtMost = 20
)

// learnedKey is a query as it is told from others for what was opened
// from it: without case, its spaces one each, and, of letters only, as
// one word, so that "sousuo jieguo" is "sousuojieguo".
func learnedKey(query string) string {
	words := strings.Fields(strings.ToLower(query))
	key := strings.Join(words, " ")
	if len(words) < 2 {
		return key
	}
	joined := 0
	for _, w := range words {
		if len(w) < 2 || len(w) > 63 {
			return key
		}
		for i := 0; i < len(w); i++ {
			if w[i] < 'a' || w[i] > 'z' {
				return key
			}
		}
		joined += len(w)
	}
	if joined > 63 {
		return key
	}
	return strings.Join(words, "")
}

func (a *App) learnedFiles() map[string][]config.Learned {
	if a.learned == nil {
		a.learned = config.LoadLearned()
	}
	return a.learned
}

// learn notes that a file was opened from the results of a query.
func (a *App) learn(query, path string) {
	key := learnedKey(query)
	if key == "" {
		return
	}
	all := a.learnedFiles()
	list := all[key]
	entry := config.Learned{Path: path}
	for i := range list {
		if strings.EqualFold(list[i].Path, path) {
			entry = list[i]
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	entry.Opens++
	entry.Time = time.Now().Unix()
	// The newest first, and no more than so many.
	list = append([]config.Learned{entry}, list...)
	if len(list) > learnedAtMost {
		list = list[:learnedAtMost]
	}
	all[key] = list
	if a.ephemeral {
		return
	}
	saved := make(map[string][]config.Learned, len(all))
	for k, v := range all {
		saved[k] = slices.Clone(v)
	}
	go func() { _ = config.SaveLearned(saved) }()
}

// recall adds to what a search found the file the user opened for its
// query often enough, when the search did not find it: one at most, after
// all that matches. It returns the file's path, "" for none.
func (a *App) recall(f *finding, found *index.Found, filter search.Filter) string {
	if !a.set.Search.History || f.query == nil || f.query.Path != nil {
		return ""
	}
	if a.learned == nil && a.ephemeral {
		// An instance that leaves no trace reads none either.
		return ""
	}
	list := a.learnedFiles()[learnedKey(f.query.Text)]
	var able []config.Learned
	for _, l := range list {
		if l.Opens >= learnedAfter {
			able = append(able, l)
		}
	}
	// The one opened last, of those opened as often.
	slices.SortStableFunc(able, func(x, y config.Learned) int {
		if x.Time != y.Time {
			return int(y.Time - x.Time)
		}
		return y.Opens - x.Opens
	})
	for _, l := range able {
		if !f.everywhere && !within(l.Path, f.origin) {
			continue
		}
		r, ok := a.learnedResult(l.Path)
		if !ok || !filter.Admits([]byte(r.Name), r.IsDir()) {
			continue
		}
		for i := range found.Results {
			if strings.EqualFold(found.Results[i].Path, l.Path) {
				// Found as it is: nothing to add.
				return ""
			}
		}
		found.Results = append(found.Results, r)
		found.Total++
		return l.Path
	}
	return ""
}

// learnedResult is a file to show for a query it was opened from: false
// for one that is gone, hidden, or that the rules put last or leave out.
func (a *App) learnedResult(path string) (index.Result, bool) {
	if a.finder != nil {
		if r, standing, ok := a.finder.Lookup(path); ok {
			if standing.Low || standing.Excluded || standing.Hidden && !a.set.ShowHidden {
				return r, false
			}
			return r, true
		}
	}
	// Of a drive without an index: as the file system has it.
	info, err := os.Lstat(path)
	if err != nil {
		return index.Result{}, false
	}
	r := index.Result{Path: path, Name: filepath.Base(path), Modified: uint32(max(info.ModTime().Unix(), 0))}
	if info.IsDir() {
		r.Flags |= index.FlagDir
	} else {
		r.Size = uint64(info.Size())
	}
	return r, true
}

// within reports whether a path is in a folder, at any depth.
func within(path, dir string) bool {
	dir = strings.TrimRight(dir, `\`)
	return len(path) > len(dir)+1 && strings.EqualFold(path[:len(dir)], dir) && path[len(dir)] == '\\'
}

// isProgram reports a path that names a program or a setting of the
// system, not a file: what shell.Programs lists.
func isProgram(path string) bool { return strings.HasPrefix(path, "::") }

// programsFor is how long the list of programs is good for.
const programsFor = 10 * time.Minute

// loadPrograms lists the programs and settings, away from the window,
// when they were not listed lately.
func (a *App) loadPrograms() {
	a.saveMu.Lock()
	busy := a.programsBusy || !a.programsAt.IsZero() && time.Since(a.programsAt) < programsFor
	if !busy {
		a.programsBusy = true
	}
	a.saveMu.Unlock()
	if busy || a.set.Search.NoPrograms {
		return
	}
	go func() {
		// In a process of its own, which ends: listed here, they would
		// bring some twenty megabytes of the system's into the program
		// for as long as it runs.
		list, err := shell.ProgramsBy(a.exe)
		a.saveMu.Lock()
		first := a.programsAt.IsZero()
		if err == nil || first {
			a.programs = list
		}
		a.programsAt, a.programsBusy = time.Now(), false
		a.saveMu.Unlock()
		if first && len(list) > 0 && a.desk != nil {
			// The search that asked for them shows them.
			a.desk.post(func() {
				for _, w := range a.windows {
					for _, t := range w.tabs {
						if t.searching() && t.find.everywhere && !t.find.content {
							w.search(t, true)
						}
					}
				}
			})
		}
	}()
}

// addPrograms puts among what a search of the whole computer found the
// programs and the settings of the system that the query matches: each
// before the files that match as surely, a program before a setting, as
// Lertaro orders the kinds of what it finds. A search of a folder finds
// files only.
func (a *App) addPrograms(f *finding, q *search.Query, found *index.Found, req *index.Request) {
	if !f.everywhere || a.set.Search.NoPrograms || q.Sort != search.SortRelevance {
		return
	}
	a.loadPrograms()
	// What narrows the search to a kind of file leaves programs in only
	// when programs are the kind.
	filter := q.Filter.And(req.Filter)
	if filter.DirsOnly || filter.Any() && !filter.AdmitsProgram() {
		return
	}
	a.saveMu.Lock()
	list := a.programs
	a.saveMu.Unlock()
	var added []index.Result
	for i := range list {
		p := &list[i]
		// A setting comes after what the rules do not put last, as
		// Lertaro's own rules have it.
		key, ok := index.KeyOf(q, p.Name, p.Setting, req.History[p.Path])
		if !ok {
			continue
		}
		added = append(added, index.Result{Path: p.Path, Name: p.Name, Key: key})
	}
	if len(added) == 0 {
		return
	}
	kindOf := func(r *index.Result) int {
		switch {
		case !isProgram(r.Path):
			return 2
		case a.setting(r.Path):
			return 1
		}
		return 0
	}
	all := append(added, found.Results...)
	slices.SortStableFunc(all, func(x, y index.Result) int {
		if sx, sy := index.Sureness(x.Key), index.Sureness(y.Key); sx != sy {
			if sx < sy {
				return -1
			}
			return 1
		}
		if kx, ky := kindOf(&x), kindOf(&y); kx != ky {
			return kx - ky
		}
		return index.Compare(&x, &y)
	})
	found.Results = all
	found.Total += len(added)
}

// setting reports whether a program's path is a setting's.
func (a *App) setting(path string) bool {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	for i := range a.programs {
		if a.programs[i].Path == path {
			return a.programs[i].Setting
		}
	}
	return false
}

// renameResult gives what a search found another name, where it is, and
// shows it under that name.
func (w *Window) renameResult(t *Tab, path, name string) {
	owner, f := w.handle(), t.find
	go func() {
		if err := shell.Rename(owner, path, name); err != nil {
			w.report(err)
			return
		}
		w.update(func() {
			if t.find != f {
				return
			}
			renamed := fsys.Join(filepath.Dir(path), name)
			for i := range t.entries {
				if t.entries[i].Name == path && i < len(f.found) {
					t.entries[i].Name = renamed
					f.found[i].Path, f.found[i].Name = renamed, name
					t.text[i] = rowText{}
				}
			}
			t.selStale = true
			// An index that follows its drive hears of it, and the search
			// is run again; one read for a search of a folder is read
			// again.
			if w.app.finder != nil && w.app.finder.Forget(path) {
				w.search(t, true)
			}
			w.changed()
		})
	}()
}

// findView puts the results in the order asked for: as found, which is
// by how well they match, or by a column.
func (t *Tab) findView() {
	f := t.find
	t.view = fsys.All(t.entries)
	f.sorted = f.sort
	if f.sort.Column == "" {
		return
	}
	o := fsys.Order{Descending: f.sort.Descending}
	switch f.sort.Column {
	case colSize:
		o.By = fsys.BySize
	case colModified:
		o.By = fsys.ByModified
	case colType:
		// The folders they are in: the paths, which start with them.
		o.By = fsys.ByName
	default:
		// By name: the names are the last parts of the paths.
		names := make([]fsys.Entry, len(t.entries))
		for i := range t.entries {
			names[i] = t.entries[i]
			names[i].Name = t.find.found[i].Name
		}
		fsys.Sort(names, t.view, o)
		return
	}
	fsys.Sort(t.entries, t.view, o)
}

// result is what a row of results stands for.
func (t *Tab) result(i int32) *index.Result { return &t.find.found[i] }

// highlight makes of a text the spans that show which of its characters
// the query took.
func (f *finding) highlight(c *ui.Context, text string, muted bool) []ui.Span {
	th := c.Theme()
	base := ui.Span{}
	if muted {
		base.Color = th.TextMuted
	}
	if f.query == nil || text == "" {
		base.Text = text
		return []ui.Span{base}
	}
	p := f.query.Pattern
	if f.query.Path != nil {
		if p = f.query.Path.File; p == nil {
			base.Text = text
			return []ui.Span{base}
		}
	}
	marks := p.Highlight(text, &f.scratch)
	var spans []ui.Span
	start, at, on := 0, 0, false
	flush := func(end int) {
		if end == start {
			return
		}
		s := base
		s.Text = text[start:end]
		if on {
			s.Weight = 700
			s.Background = th.Accent.Alpha(0.22)
			s.Radius = 2
		}
		spans = append(spans, s)
		start = end
	}
	for i := 0; i < len(text); {
		_, size := utf8.DecodeRuneInString(text[i:])
		if at < len(marks) && marks[at] != on {
			flush(i)
			on = marks[at]
		}
		at++
		i += size
	}
	flush(len(text))
	return spans
}

// searchField is the field that searches: in the folder shown, or, with
// the button beside it on, the whole computer.
func (w *Window) searchField(c *ui.Context, t *Tab) {
	if t.bin() {
		// What was deleted is narrowed where it is, by the names it had.
		in := ui.TextInput(c.Key("filter"), &t.filter).Placeholder(locales.Filter()).Label(locales.Filter()).
			Width(190).Shrink(1).MinWidth(70).Height(controlSize).Padding(0, 6).TabStop(false).Bind(&t.filterRef)
		switch {
		case in.Shortcut(0, ui.KeyEscape):
			t.filter = ""
			t.wantFocus = true
		case in.Submitted(), in.Shortcut(0, ui.KeyDown):
			t.wantFocus = true
		}
		return
	}
	if !real(t.place()) {
		// From the list of drives, as from any place that is no folder,
		// a search is of all the drives.
		t.everywhere = true
	}
	th := c.Theme()
	hint := locales.SearchEverywhere()
	if !t.everywhere {
		hint = locales.SearchIn(placeTitle(t.place()))
	}
	// The two ways into a search, each a button: of this folder, and of
	// the whole computer, which is past the field.
	if real(t.place()) {
		sb := iconButton(c, "search", icoSearch, locales.SearchIn(placeTitle(t.place())))
		if t.searching() && !t.everywhere {
			sb.Background(th.Accent.Alpha(0.22))
		}
		if sb.Clicked() {
			w.setEverywhere(t, false)
		}
	}
	before := t.filter
	// What an input method composes among the files shows here, after
	// what was typed, until it is done.
	value, shown := &t.filter, ""
	if t.composing != "" {
		shown = t.filter + t.composing
		value = &shown
	}
	in := ui.TextInput(c.Key("filter"), value).Placeholder(hint).Label(locales.Search()).Search().
		Width(190).Shrink(1).MinWidth(70).Height(controlSize).Padding(0, 6).TabStop(false).Bind(&t.filterRef)
	t.fieldAt = in.Bounds()
	if t.caretEnd {
		// Typed among the files: what is typed next goes after it.
		t.caretEnd = false
		n := utf8.RuneCountInString(t.filter)
		in.SetTextSelection(n, n)
	}
	switch {
	case in.Shortcut(0, ui.KeyEscape):
		t.filter = ""
		w.endSearch(t)
		t.wantFocus = true
	case in.Submitted(), in.Shortcut(0, ui.KeyDown):
		t.wantFocus = true
		if t.cursor < 0 && len(t.view) > 0 {
			t.cursor = 0
			t.list.Choose(0)
		}
	}
	if t.filter != before || t.searching() && t.find.everywhere != t.everywhere {
		// Several lines pasted are as many names to find: the field took
		// them as one line, with spaces between.
		if len(t.filter)-len(before) > 2 {
			t.filter = w.pastedLines(t.filter)
			t.caretEnd = true
		}
		if t.find != nil {
			t.find.everywhere = t.everywhere
		}
		w.setQuery(t)
	}
	b := iconButton(c, "everywhere", icoComputer, locales.SearchScopeEverywhere())
	if t.everywhere {
		b.Background(th.Accent.Alpha(0.22))
	}
	if b.Clicked() && real(t.place()) {
		w.setEverywhere(t, !t.everywhere)
	}
}

// pastedLines is what the field holds with the lines just pasted into
// it, if several were, apart by bars: as many names, any of which will
// do.
func (w *Window) pastedLines(text string) string {
	data, err := w.app.desk.read(transfer.Text)
	if err != nil {
		return text
	}
	raw, err := data.Read(transfer.Text)
	if err != nil {
		return text
	}
	breaks := func(r rune) bool { return r == '\r' || r == '\n' }
	var lines []string
	for _, line := range strings.FieldsFunc(string(raw), breaks) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) < 2 {
		return text
	}
	// As the field has them: each line break a space.
	flat := strings.Map(func(r rune) rune {
		if breaks(r) {
			return ' '
		}
		return r
	}, strings.ReplaceAll(string(raw), "\r\n", "\n"))
	if at := strings.Index(text, flat); at >= 0 {
		return text[:at] + strings.Join(lines, " | ") + text[at+len(flat):]
	}
	return text
}

// setEverywhere has the tab's searches be of the whole computer, or of
// its folder, and puts the keyboard in the field.
func (w *Window) setEverywhere(t *Tab, on bool) {
	t.everywhere = on || !real(t.place())
	if t.find != nil {
		t.find.everywhere = t.everywhere
		w.search(t, false)
	}
	t.filterRef.Focus()
	w.invalidate()
}

// kindBar is the row of kinds a search can be narrowed to, under the
// toolbar while a search shows.
func (w *Window) kindBar(c *ui.Context, t *Tab) {
	f := t.find
	th := c.Theme()
	ui.Row(c.Key("kinds")).Height(barHeight-4).Shrink(0).Padding(0, 8).Gap(4).AlignItems(ui.Center).Children(func() {
		for i, k := range w.app.kindList() {
			b := ui.ButtonBase(c.Key(i)).Height(controlSize-4).Padding(0, 9).Radius(10).Shrink(0).Center().TabStop(false)
			on := f.kind == i
			b.Draw(func(p *ui.Painter, r ui.Rect) {
				switch {
				case on:
					p.Fill(r, th.Accent.Alpha(0.24), 10)
				case b.Hovered():
					p.Fill(r, th.SurfaceHover, 10)
				}
			})
			b.Children(func() { ui.Text(c, k.title()).SingleLine().FontSize(th.FontSize - 1) })
			if b.Clicked() && !on {
				f.kind = i
				w.search(t, false)
			}
		}
		ui.Spacer(c)
		if len(f.found) < f.total {
			if ui.Link(c, locales.SearchShowAll(), "").FontSize(th.FontSize - 1).Clicked() {
				f.limit *= moreResults
				w.search(t, true)
			}
		}
	})
	ui.Divider(c)
}

// typed takes a character typed among the files: it starts a search of
// it, and what follows goes on in the field.
func (w *Window) typed(t *Tab, text string) bool {
	if text == "" || t.settings || t.bin() {
		return false
	}
	for _, r := range text {
		if r < 0x20 || r == 0x7F {
			return false
		}
	}
	if strings.TrimSpace(t.filter) == "" && strings.TrimSpace(text) == "" {
		// A space alone chooses, as it always did.
		return false
	}
	t.filter += text
	t.caretEnd = true
	t.filterRef.Focus()
	w.setQuery(t)
	w.invalidate()
	return true
}

// takeTyping has the list of files take what is typed while it has the
// keyboard, as a text field does: through the system's input method.
//
// A list is no text, and an element that is none has no input method: its
// keys type the letters of the keyboard. So a letter typed among the files
// came as that letter, the search field took the keyboard for the next,
// and only there did the input method wake: the first letter was typed
// past it and the rest composed by it, and it could not be switched to
// English beforehand, there being none to switch among the files. With
// the list taking text the input method is there from the first key, in
// whatever mode the user left it; what it composes shows in the search
// field, and what it gives is searched for.
func (w *Window) takeTyping(c *ui.Context, t *Tab, list ui.Element, banded bool) {
	t.listFocus = list.Focused()
	if !t.listFocus {
		t.composing = ""
	}
	if !t.typesToSearch() {
		// Nothing typed here is searched for: the keys stay keys.
		return
	}
	// The candidates of the input method show under the search field,
	// where what is composed shows.
	at := ui.Rect{X: 8, W: 1, H: controlSize}
	if b := list.Bounds(); t.fieldAt.W > 0 && b.W > 0 {
		at = ui.Rect{X: t.fieldAt.X - b.X + 6, Y: t.fieldAt.Y - b.Y, W: 1, H: t.fieldAt.H}
	}
	if !banded {
		// A list with a rectangle to draw in it has its input taken for
		// that (bandOn), which hands on what is typed.
		list.HandleInput(func(ev ui.InputEvent) bool { return w.listText(t, ev) })
	}
	list.TextCaret(at)
}

// typesToSearch reports whether what is typed among the tab's files is
// searched for.
func (t *Tab) typesToSearch() bool {
	return !t.settings && !t.bin() && !t.editing && t.renaming == ""
}

// listText takes what is typed, or composed by an input method, while the
// list of files has the keyboard, and reports whether it did.
func (w *Window) listText(t *Tab, ev ui.InputEvent) bool {
	if !t.typesToSearch() {
		return false
	}
	switch ev.Kind {
	case ui.InputText:
		t.composing = ""
		return w.typed(t, ev.Text)
	case ui.InputCompose:
		if t.composing != ev.Text {
			t.composing = ev.Text
			w.invalidate()
		}
		return true
	}
	return false
}

// nameTyped reports whether a name is being typed in the tab, as when a
// file is renamed: the keyboard is with a text, and not with the list of
// files, which takes text too.
func (t *Tab) nameTyped(c *ui.Context) bool {
	return c.FocusTakesText() && !t.listFocus
}

// openLocation shows the folder a result is in, with the result chosen.
func (w *Window) openLocation(t *Tab, path string) {
	dir, ok := fsys.Parent(path)
	if !ok {
		return
	}
	w.endSearchQuietly(t)
	w.navigate(t, dir, navPush, fsys.Base(path))
}

// endSearchQuietly forgets the tab's search without reading its folder
// again: the tab is about to show another.
func (w *Window) endSearchQuietly(t *Tab) {
	if f := t.find; f != nil {
		if f.stop != nil {
			f.stop()
		}
		t.find, t.filter = nil, ""
		t.everywhere, t.mode = false, f.mode
		t.Path = f.origin
	}
}

// searchStatus is what the status bar tells of a search.
func (t *Tab) searchStatus() (left, right string) {
	f := t.find
	switch {
	case t.sel.Len() > 0:
		left = locales.StatusSelected(t.sel.Len())
		if files := t.sel.Len() - t.selDirs; files > 0 {
			left += "  ·  " + formatSize(t.selBytes)
		}
	case len(f.found) < f.total:
		left = locales.SearchResultsOf(len(f.found), f.total)
	default:
		left = locales.SearchResults(f.total)
	}
	switch {
	case f.note != "":
		right = f.note
	case f.content && f.stop != nil && f.toRead > 0:
		right = locales.SearchContentReading(strconv.FormatInt(f.read.Load(), 10), strconv.Itoa(f.toRead))
	case f.slow:
		right = locales.SearchReadingFolder()
	case f.reading:
		right = locales.SearchReadingDrives()
	case f.stop != nil:
		right = locales.SearchSearching()
	case f.took > 0:
		right = formatDuration(f.took)
	}
	return left, right
}
