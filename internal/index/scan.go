package index

import (
	"context"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"myfiles/internal/search"
)

// Request is a search.
type Request struct {
	// Background is set for a search the user did not ask for just now:
	// one run again because the files changed.
	Background bool
	Query      *search.Query
	// Scope is the folder to search in, with all it holds; "" for
	// everything.
	Scope string
	// Filter narrows results beside what the query's tokens ask, as the
	// buttons that show one kind of file do.
	Filter search.Filter
	// ShowHidden lets hidden files, and those of hidden folders, show.
	ShowHidden bool
	// Limit is the most results to return; the rest are counted.
	Limit int
	// Favorites are the paths the user keeps at hand, and History the
	// paths opened from results before, each with how much: a score of
	// 300 and of 500 put a file that matches well before the others.
	Favorites []string
	History   map[string]int
}

// Result is a file a search found.
type Result struct {
	// Path is the file's path, and Name its last part.
	Path string
	Name string
	// Flags are the file's, as the index's.
	Flags uint16
	Size  uint64
	// The times are in seconds since 1970.
	Modified, Created, Accessed uint32
	// Key orders results, the smallest first.
	Key uint64
	// ByFolder is set for a result some of whose query is in the names of
	// the folders above it, not in its own.
	ByFolder bool
}

// IsDir reports whether the result is a folder.
func (r *Result) IsDir() bool { return r.Flags&FlagDir != 0 }

// hit is a match, as a scan keeps it.
type hit struct {
	key uint64
	ref Ref
}

func hitLess(a, b hit) int {
	switch {
	case a.key < b.key:
		return -1
	case a.key > b.key:
		return 1
	case a.ref < b.ref:
		return -1
	case a.ref > b.ref:
		return 1
	}
	return 0
}

// The bits of a hit's key, from the top: whether it is deeper than the
// folder searched in, the section it is in, what orders it within the
// section, and how well it matches.
const (
	keyDeep    = 1 << 63
	secPinned  = 0
	secBoosted = 1
	secLow     = 2
)

type boost struct {
	favorite bool
	score    int
}

// scan is a search of one view under way.
type scan struct {
	ctx    context.Context
	v      *View
	req    *Request
	q      *search.Query
	p      *search.Pattern
	filter search.Filter
	skip   uint8
	keep   int
	// scope is the folder searched in, 0 for the root, and scoped whether
	// its direct entries come first.
	scope  Ref
	scoped bool
	// needs are runs of rows of which a row must be in one each, unless
	// its own name matches the pattern beside them.
	needs []need
	// dirs are the patterns the folders above a result must match, the
	// outermost first, for a query that names folders.
	dirs  []*search.Pattern
	boost map[Ref]boost

	total atomic.Int64
	mu    sync.Mutex
	hits  []hit
	// byFolder are the refs found by the names of the folders above.
	byFolder map[Ref]bool
}

// need is a pattern a folder above a row must match, or the row's own
// name: spans are the rows under the folders that do.
type need struct {
	spans []span
	own   *search.Pattern
}

// worker is what one goroutine of a scan keeps.
type worker struct {
	s       search.Scratch
	hits    []hit
	worst   uint64
	full    bool
	total   int64
	cursors []int
}

func (w *worker) add(sc *scan, h hit) {
	w.total++
	if w.full && h.key > w.worst {
		return
	}
	w.hits = append(w.hits, h)
	if len(w.hits) >= 2*sc.keep+64 {
		w.trim(sc.keep)
	}
}

func (w *worker) trim(keep int) {
	if len(w.hits) <= keep {
		return
	}
	selectHits(w.hits, keep)
	w.hits = w.hits[:keep]
	w.worst, w.full = 0, true
	for _, h := range w.hits {
		w.worst = max(w.worst, h.key)
	}
}

// selectHits puts the k best hits first, in no order among themselves.
func selectHits(hits []hit, k int) {
	lo, hi := 0, len(hits)
	for hi-lo > 16 {
		// The middle of three, moved to the middle of the hits.
		a, b, c := hits[lo], hits[lo+(hi-lo)/2], hits[hi-1]
		pivot := b
		if hitLess(a, b) > 0 {
			a, b = b, a
		}
		if hitLess(b, c) > 0 {
			b = c
			if hitLess(a, b) > 0 {
				b = a
			}
		}
		pivot = b
		i, j := lo, hi-1
		for i <= j {
			for hitLess(hits[i], pivot) < 0 {
				i++
			}
			for hitLess(hits[j], pivot) > 0 {
				j--
			}
			if i <= j {
				hits[i], hits[j] = hits[j], hits[i]
				i++
				j--
			}
		}
		switch {
		case k <= j:
			hi = j + 1
		case k >= i:
			lo = i
		default:
			return
		}
	}
	slices.SortFunc(hits[lo:hi], hitLess)
}

// inSpans reports whether a row is in one of the runs, which are in
// order; cursor remembers where the last row asked for was.
func inSpans(spans []span, r Row, cursor *int) bool {
	i := *cursor
	if i >= len(spans) || i > 0 && spans[i-1].to > r {
		i = sort.Search(len(spans), func(k int) bool { return spans[k].to > r })
	} else {
		for i < len(spans) && spans[i].to <= r {
			i++
		}
	}
	*cursor = i
	return i < len(spans) && spans[i].from <= r
}

// Search finds what a request asks for in a view. It returns the best
// matches, in order, and how many there are in all. The results are the
// view's to hand out again while nothing changes: not to be changed.
func (v *View) Search(ctx context.Context, req *Request) ([]Result, int) {
	q := req.Query
	if q.Unknown {
		return nil, 0
	}
	key := req.key()
	if res, total, ok := v.recall(key); ok {
		return res, total
	}
	sc := &scan{ctx: ctx, v: v, req: req, q: q, p: q.Pattern, keep: max(req.Limit, 1)}
	sc.filter = q.Filter.And(req.Filter)
	sc.skip = markGone | markOver | markHidden | markExcluded
	if req.ShowHidden {
		sc.skip &^= markHidden
	}
	if q.Bypass {
		sc.skip &^= markExcluded
	}
	drive := q.Pattern.Drive
	if q.Path != nil {
		drive = q.Path.Drive
	}
	if drive != 0 && !v.onDrive(drive) {
		return nil, 0
	}
	if req.Scope != "" {
		ref, ok := v.Resolve(req.Scope)
		if !ok || !v.isDir(ref) {
			return nil, 0
		}
		sc.scope, sc.scoped = v.canon(ref), true
		// What is searched in shows, whatever hides it from elsewhere.
		sc.skip &^= v.marksOf(ref) & (markHidden | markExcluded)
	}
	sc.boosts()

	switch {
	case q.Path != nil:
		sc.path()
	default:
		if q.Pattern.Empty() && drive == 0 && !sc.scoped && !sc.filter.Any() && len(q.Segments) == 0 {
			return nil, 0
		}
		for _, seg := range q.Segments {
			sc.needs = append(sc.needs, need{spans: v.foldersMatching(ctx, seg, nil), own: seg})
		}
		spans := sc.all()
		t0 := time.Now()
		sc.run(spans, sc.p)
		t1 := time.Now()
		sc.fallback(spans)
		if Trace != nil {
			Trace("run", t1.Sub(t0))
			Trace("fallback", time.Since(t1))
		}
	}
	if ctx.Err() != nil {
		return nil, 0
	}
	t2 := time.Now()
	res := sc.results()
	if Trace != nil {
		Trace("results", time.Since(t2))
	}
	total := int(sc.total.Load())
	v.keep(key, res, total)
	return res, total
}

// KeyOf is the key a search orders a name by that is no file of an
// index, as a program's: as a file's the rules say nothing of, or, with
// low, one they put after the others. score is what the name's having
// been opened before counts, as Request.History has it. It reports false
// for a name the query does not match.
func KeyOf(q *search.Query, name string, low bool, score int) (uint64, bool) {
	if q == nil || q.Path != nil || q.Pattern == nil || q.Pattern.Empty() {
		return 0, false
	}
	var s search.Scratch
	rank, ok := q.Pattern.Eval([]byte(name), &s)
	if !ok {
		return 0, false
	}
	grade := q.Pattern.Grade([]byte(name), false, rank)
	tier, tie := uint64(0), uint64(0)
	if grade <= search.GradeStrong {
		switch {
		case score >= 500:
			tier = 2
		case score >= 300:
			tier = 1
		}
		if tier > 0 {
			tie = uint64(min(score, 4000))
		}
	}
	hist := (3-tier)<<12 | (4095 - tie)
	if low {
		return secLow<<61 | (uint64(grade)<<24|hist)<<32 | uint64(rank.Key()), true
	}
	return secBoosted<<61 | (uint64(grade)<<24|1<<23|1<<21|hist)<<32 | uint64(rank.Key()), true
}

// Sureness is the part of a result's key that the kinds of results are
// ordered after: where the rules put it, and how sure the match is. Of
// two results as sure as each other a program comes before a file.
func Sureness(key uint64) uint64 { return key >> 56 }

// keptSearch is a search with what it found.
type keptSearch struct {
	key   string
	gen   uint64
	res   []Result
	total int
}

// keptAtMost is the most results a search is kept with.
const keptAtMost = 4096

// key tells requests apart: "" for one that is not to be kept.
func (req *Request) key() string {
	if req.Query.Key == "" {
		return ""
	}
	var h uint64
	for _, f := range req.Favorites {
		h = h*31 + hashString(f)
	}
	for path, n := range req.History {
		h += hashString(path) * uint64(n+1)
	}
	filter := req.Filter.Key()
	b := make([]byte, 0, len(req.Query.Key)+len(req.Scope)+len(filter)+48)
	b = append(append(b, req.Query.Key...), 0)
	b = append(append(b, req.Scope...), 0)
	b = append(append(b, filter...), 0)
	b = strconv.AppendInt(b, int64(req.Limit), 10)
	if req.ShowHidden {
		b = append(b, 'h')
	}
	b = strconv.AppendUint(append(b, ' '), h, 16)
	return string(b)
}

func hashString(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * 1099511628211
	}
	return h
}

// recall is what the same search found, if nothing changed since. What
// it returns is shared with the searches to come: not to be changed.
func (v *View) recall(key string) ([]Result, int, bool) {
	if key == "" {
		return nil, 0, false
	}
	v.keptM.Lock()
	defer v.keptM.Unlock()
	for i := range v.kept {
		if k := &v.kept[i]; k.key == key && k.gen == v.gen {
			return k.res, k.total, true
		}
	}
	return nil, 0, false
}

func (v *View) keep(key string, res []Result, total int) {
	if key == "" || len(res) > keptAtMost {
		return
	}
	v.keptM.Lock()
	defer v.keptM.Unlock()
	v.kept[v.keptN%len(v.kept)] = keptSearch{key, v.gen, res, total}
	v.keptN++
}

// Trace, when set, is told how long the parts of a search took.
var Trace func(part string, took time.Duration)

// onDrive reports whether the view is of the drive of a letter.
func (v *View) onDrive(letter rune) bool {
	root := v.ix.Root
	return len(root) >= 2 && root[1] == ':' && rune(root[0]|0x20) == letter
}

// all are the runs of rows the scope covers.
func (sc *scan) all() []span {
	if !sc.scoped || sc.scope == 0 {
		if sc.v.ix.n <= 1 {
			return nil
		}
		return []span{{1, Row(sc.v.ix.n)}}
	}
	return sc.v.spans(sc.scope)
}

// boosts finds the rows of the favorites and of the files opened before.
func (sc *scan) boosts() {
	if len(sc.req.Favorites) == 0 && len(sc.req.History) == 0 {
		return
	}
	sc.boost = map[Ref]boost{}
	for _, path := range sc.req.Favorites {
		if ref, ok := sc.v.Resolve(path); ok {
			sc.boost[ref] = boost{favorite: true}
		}
	}
	for path, score := range sc.req.History {
		if ref, ok := sc.v.Resolve(path); ok {
			b := sc.boost[ref]
			b.score = score
			sc.boost[ref] = b
		}
	}
}

// key makes the key of a match: where among results it goes.
func (sc *scan) key(ref Ref, name []byte, dir bool, marks uint8, rank search.Rank, direct bool) uint64 {
	if sc.q.Sort != search.SortRelevance {
		return sc.sortKey(ref)
	}
	grade := sc.p.Grade(name, dir, rank)
	strong := grade <= search.GradeStrong
	var b boost
	if sc.boost != nil {
		b = sc.boost[ref]
	}
	tier, tie := uint64(0), uint64(0)
	if strong {
		switch {
		case b.score >= 500:
			tier = 2
		case b.score >= 300:
			tier = 1
		}
		if tier > 0 {
			tie = uint64(min(b.score, 4000))
		}
	}
	prio := uint64(1)
	switch {
	case marks&markHigh != 0:
		prio = 0
	case marks&markLow != 0:
		prio = 2
	}
	top := marks&markPinned != 0
	favPin := !top && b.favorite && strong
	hist := (3-tier)<<12 | (4095 - tie)
	var section, a uint64
	switch {
	case top || favPin:
		section = secPinned
		a = b2u(!top)<<28 | b2u(!favPin)<<27 | uint64(grade)<<24 | prio<<22 | hist
	case marks&markLow != 0:
		section = secLow
		a = uint64(grade)<<24 | hist
	default:
		section = secBoosted
		a = uint64(grade)<<24 | b2u(!b.favorite)<<23 | prio<<21 | hist
	}
	key := section<<61 | a<<32 | uint64(rank.Key())
	if sc.scoped && !direct {
		key |= keyDeep
	}
	return key
}

func b2u(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// sortKey orders by what a token of the query asks for.
func (sc *scan) sortKey(ref Ref) uint64 {
	var val uint64
	v := sc.v
	if ref.added() {
		d := &v.d.rows[ref.at()]
		switch sc.q.Sort {
		case search.SortSize:
			val = d.size
		case search.SortCreated:
			val = uint64(d.ctime)
		case search.SortModified:
			val = uint64(d.mtime)
		case search.SortAccessed:
			val = uint64(d.atime)
		}
	} else {
		switch sc.q.Sort {
		case search.SortSize:
			val = v.ix.size[ref]
		case search.SortCreated:
			val = uint64(v.ix.ctime[ref])
		case search.SortModified:
			val = uint64(v.ix.mtime[ref])
		case search.SortAccessed:
			val = uint64(v.ix.atime[ref])
		}
	}
	if sc.q.Descending {
		return ^val
	}
	return val
}

// chunkRows is how many rows a goroutine takes at a time.
const chunkRows = 1 << 15

// run scans runs of rows, and the delta's, for the names a pattern
// matches.
func (sc *scan) run(spans []span, p *search.Pattern) {
	sc.v.ix.warm(spans)
	// The runs, cut into stretches the goroutines take in turn: shorter
	// ones where there are few rows, for each goroutine to have some.
	rows := 0
	for _, s := range spans {
		rows += int(s.to - s.from)
	}
	stretch := Row(min(max(rows/(4*runtime.GOMAXPROCS(0)), 1024), chunkRows))
	var chunks []span
	for _, s := range spans {
		for lo := s.from; lo < s.to; lo += stretch {
			chunks = append(chunks, span{lo, min(lo+stretch, s.to)})
		}
	}
	workers := min(runtime.GOMAXPROCS(0), len(chunks))
	if rows < 4096 {
		workers = min(workers, 1)
	}
	var next atomic.Int64
	body := func(w *worker) {
		if cap(w.cursors) < len(sc.needs) {
			w.cursors = make([]int, len(sc.needs))
		}
		w.cursors = w.cursors[:len(sc.needs)]
		for {
			i := int(next.Add(1)) - 1
			if i >= len(chunks) || sc.ctx.Err() != nil {
				return
			}
			sc.rows(w, chunks[i], p)
		}
	}
	ws := make([]*worker, max(workers, 1))
	for i := range ws {
		ws[i] = getWorker()
	}
	if workers <= 1 {
		body(ws[0])
	} else {
		var wg sync.WaitGroup
		for _, w := range ws {
			wg.Go(func() { body(w) })
		}
		wg.Wait()
	}
	// What changed since the index was built is few: one goroutine.
	sc.delta(ws[0], p, false)
	for _, w := range ws {
		sc.total.Add(w.total)
		sc.hits = append(sc.hits, w.hits...)
		putWorker(w)
	}
}

// workers are kept between searches: what they match in grows to the
// longest name once.
var workers sync.Pool

func getWorker() *worker {
	if w, ok := workers.Get().(*worker); ok {
		return w
	}
	return &worker{}
}

func putWorker(w *worker) {
	if cap(w.hits) > 1<<16 {
		w.hits = nil
	}
	w.hits, w.worst, w.full, w.total = w.hits[:0], 0, false, 0
	workers.Put(w)
}

// rows scans a stretch of the index's rows.
func (sc *scan) rows(w *worker, s span, p *search.Pattern) {
	ix := sc.v.ix
	mask, marks, flags := ix.mask, ix.marks, ix.flags
	required, only := p.Required()
	empty := p.Empty()
	skip := sc.skip
	needName := sc.filter.NeedsName()
	dirsOnly, filesOnly := sc.filter.DirsOnly, sc.filter.FilesOnly
	scopeRow, _ := sc.v.folder(sc.scope)
	for i := range w.cursors {
		w.cursors[i] = 0
	}
	for r := s.from; r < s.to; r++ {
		m := mask[r]
		if m&required != required {
			continue
		}
		if marks[r]&skip != 0 {
			continue
		}
		if !only && !empty && !p.Admits(m) {
			continue
		}
		dir := flags[r]&FlagDir != 0
		if dir && filesOnly || !dir && dirsOnly {
			continue
		}
		child := ix.parent[r] == scopeRow
		name := ix.names[ix.nameOff[r]:ix.nameOff[r+1]]
		if !p.Quick(name) {
			continue
		}
		if needName && !sc.filter.Admits(name, dir) {
			continue
		}
		if !sc.passes(w, r, name) {
			continue
		}
		var rank search.Rank
		if !empty {
			var ok bool
			if rank, ok = p.Eval(name, &w.s); !ok {
				continue
			}
		}
		w.add(sc, hit{sc.key(Ref(r), name, dir, marks[r], rank, child), Ref(r)})
	}
}

// passes reports whether a row is in the runs the query needs it in, or
// matches by its own name where that will do.
func (sc *scan) passes(w *worker, r Row, name []byte) bool {
	for i := range sc.needs {
		n := &sc.needs[i]
		if inSpans(n.spans, r, &w.cursors[i]) {
			continue
		}
		if n.own != nil {
			if _, ok := n.own.Match(name, &w.s); ok {
				continue
			}
		}
		return false
	}
	return true
}

// delta scans the rows of what changed since the index was built.
func (sc *scan) delta(w *worker, p *search.Pattern, direct bool) {
	v := sc.v
	empty := p.Empty()
	for i := range v.d.rows {
		d := &v.d.rows[i]
		if d.marks&(sc.skip&^markOver) != 0 {
			continue
		}
		if !empty && !p.Admits(d.mask) {
			continue
		}
		ref := refAdded | Ref(i)
		child := d.parent == sc.scope
		if direct && !child || sc.scoped && sc.scope != 0 && !child && !v.under(ref, sc.scope) {
			continue
		}
		dir := d.flags&FlagDir != 0
		name := []byte(d.name)
		if !sc.filter.Admits(name, dir) {
			continue
		}
		if !sc.passesSlow(ref, name, &w.s) {
			continue
		}
		var rank search.Rank
		if !empty {
			var ok bool
			if rank, ok = p.Eval(name, &w.s); !ok {
				continue
			}
		}
		w.add(sc, hit{sc.key(ref, name, dir, d.marks, rank, child), ref})
	}
}

// passesSlow is passes for a row of the delta: the folders above it are
// asked one by one.
func (sc *scan) passesSlow(ref Ref, name []byte, s *search.Scratch) bool {
	for i := range sc.needs {
		n := &sc.needs[i]
		if _, ok := n.own.Match(name, s); ok {
			continue
		}
		if !sc.v.aboveMatches(ref, n.own, s) {
			return false
		}
	}
	// The folders a query names, from the nearest up.
	at := len(sc.dirs) - 1
	for up, n := ref, 0; at >= 0 && n < 4096; n++ {
		up = sc.v.canon(sc.v.parentOf(up))
		if up == 0 {
			break
		}
		if _, ok := sc.dirs[at].MatchString(sc.v.nameOf(up), s); ok {
			at--
		}
	}
	return at < 0
}

// aboveMatches reports whether a folder above a file matches a pattern.
func (v *View) aboveMatches(ref Ref, p *search.Pattern, s *search.Scratch) bool {
	for n := 0; n < 4096; n++ {
		ref = v.canon(v.parentOf(ref))
		if ref == 0 {
			return false
		}
		if _, ok := p.MatchString(v.nameOf(ref), s); ok {
			return true
		}
	}
	return false
}

// foldersMatching are the runs of rows under the folders whose names
// match a pattern, in order and apart. within, when given, are the runs
// the folders themselves must be in.
func (v *View) foldersMatching(ctx context.Context, p *search.Pattern, within []span) []span {
	ix := v.ix
	if within == nil {
		if ix.n <= 1 {
			return nil
		}
		within = []span{{1, Row(ix.n)}}
	}
	var chunks []span
	for _, s := range within {
		for lo := s.from; lo < s.to; lo += chunkRows {
			chunks = append(chunks, span{lo, min(lo+chunkRows, s.to)})
		}
	}
	workers := max(min(runtime.GOMAXPROCS(0), len(chunks)), 1)
	found := make([][]span, workers)
	var next atomic.Int64
	var wg sync.WaitGroup
	for wi := range workers {
		wg.Go(func() {
			var s search.Scratch
			for {
				i := int(next.Add(1)) - 1
				if i >= len(chunks) || ctx.Err() != nil {
					return
				}
				c := chunks[i]
				for r := c.from; r < c.to; r++ {
					if ix.flags[r]&FlagDir == 0 || ix.marks[r]&(markGone|markOver) != 0 || !p.Admits(ix.mask[r]) {
						continue
					}
					if _, ok := p.Match(ix.nameBytes(r), &s); !ok {
						continue
					}
					if ix.end[r] > r+1 {
						found[wi] = append(found[wi], span{r + 1, ix.end[r]})
					}
					// What it holds is covered already.
					r = ix.end[r] - 1
				}
			}
		})
	}
	wg.Wait()
	var all []span
	for _, f := range found {
		all = append(all, f...)
	}
	// The index's folders renamed since: under their names of now.
	var s search.Scratch
	for i := range v.d.rows {
		d := &v.d.rows[i]
		if d.base == 0 || d.marks&markGone != 0 || ix.end[d.base] <= d.base+1 {
			continue
		}
		if _, ok := p.MatchString(d.name, &s); ok {
			all = append(all, span{d.base + 1, ix.end[d.base]})
		}
	}
	return mergeSpans(all)
}

// mergeSpans puts runs in order, those inside others taken out.
func mergeSpans(all []span) []span {
	if len(all) < 2 {
		return all
	}
	sort.Slice(all, func(i, j int) bool { return all[i].from < all[j].from })
	out := all[:1]
	for _, s := range all[1:] {
		last := &out[len(out)-1]
		if s.from < last.to {
			last.to = max(last.to, s.to)
			continue
		}
		out = append(out, s)
	}
	return out
}

// intersect are the parts of runs that are in others too.
func intersect(a, b []span) []span {
	var out []span
	for i, j := 0, 0; i < len(a) && j < len(b); {
		lo, hi := max(a[i].from, b[j].from), min(a[i].to, b[j].to)
		if lo < hi {
			out = append(out, span{lo, hi})
		}
		if a[i].to < b[j].to {
			i++
		} else {
			j++
		}
	}
	return out
}

// fallback finds the files that match a query of several words by the
// names of the folders above them: "d01j dcj" finds d01j.txt in a folder
// called dcj. A word of the query at least must be in the file's own
// name. Such results come after those that match by name alone.
func (sc *scan) fallback(spans []span) {
	sets := sc.p.Split()
	if sets == nil || sc.q.Sort != search.SortRelevance || sc.ctx.Err() != nil {
		return
	}
	// Results by name come first: when they fill what is asked for,
	// those by folder would not show.
	if int(sc.total.Load()) >= sc.keep {
		return
	}
	v := sc.v
	// For each word, the rows under a folder that matches it.
	under := make([][]span, len(sets))
	var reach []span
	for i, set := range sets {
		if !set.Positive() {
			continue
		}
		under[i] = v.foldersMatching(sc.ctx, set, nil)
		reach = append(reach, under[i]...)
	}
	reach = intersect(mergeSpans(reach), spans)
	if len(reach) == 0 {
		return
	}
	termLen := sc.p.TermLength()
	scopeRow, _ := v.folder(sc.scope)
	ix := v.ix
	var chunks []span
	for _, s := range reach {
		for lo := s.from; lo < s.to; lo += chunkRows {
			chunks = append(chunks, span{lo, min(lo+chunkRows, s.to)})
		}
	}
	workers := max(min(runtime.GOMAXPROCS(0), len(chunks)), 1)
	ws := make([]*worker, workers)
	var next atomic.Int64
	var wg sync.WaitGroup
	for wi := range ws {
		w := &worker{cursors: make([]int, len(sc.needs))}
		ws[wi] = w
		wg.Go(func() {
			at := make([]int, len(sets))
			for {
				ci := int(next.Add(1)) - 1
				if ci >= len(chunks) || sc.ctx.Err() != nil {
					return
				}
				clear(w.cursors)
				clear(at)
				c := chunks[ci]
			rows:
				for r := c.from; r < c.to; r++ {
					if ix.marks[r]&sc.skip != 0 {
						continue
					}
					dir := ix.flags[r]&FlagDir != 0
					name := ix.nameBytes(r)
					if !sc.filter.Admits(name, dir) {
						continue
					}
					byName, byFolder := 0, 0
					for i, set := range sets {
						if set.Admits(ix.mask[r]) {
							if _, ok := set.Match(name, &w.s); ok {
								byName++
								continue
							}
						}
						if under[i] == nil || !inSpans(under[i], r, &at[i]) {
							continue rows
						}
						byFolder++
					}
					if byName == 0 || byFolder == 0 {
						continue
					}
					if !sc.passes(w, r, name) {
						continue
					}
					// How much of the path the query is: the longer the
					// path, the less.
					length := len(name)
					for up := ix.parent[r]; up != 0; up = ix.parent[up] {
						length += int(ix.nameOff[up+1]-ix.nameOff[up]) + 1
					}
					rank := search.NoNameRank(min(1, float64(termLen)/float64(max(length, 1))))
					child := ix.parent[r] == scopeRow
					w.add(sc, hit{sc.key(Ref(r), name, dir, ix.marks[r], rank, child), Ref(r)})
				}
			}
		})
	}
	wg.Wait()
	if sc.byFolder == nil {
		sc.byFolder = map[Ref]bool{}
	}
	for _, w := range ws {
		sc.total.Add(w.total)
		w.trim(sc.keep)
		for _, h := range w.hits {
			sc.byFolder[h.ref] = true
		}
		sc.hits = append(sc.hits, w.hits...)
	}
}

// path searches as a query that names folders asks.
func (sc *scan) path() {
	v, pq := sc.v, sc.q.Path
	empty := search.ParseText("", search.Options{})
	if pq.Exact != "" {
		// A folder of that very path lists what it holds; the path of an
		// entry of a folder, the entries that match its last part.
		if pq.Listing {
			if ref, ok := v.Resolve(pq.Exact); ok && v.isDir(ref) {
				sc.list(ref, empty)
				return
			}
		} else if cut := strings.LastIndexByte(pq.Exact, '\\'); cut >= 2 {
			dir, last := pq.Exact[:cut], pq.Exact[cut+1:]
			if cut == 2 {
				dir = pq.Exact[:3]
			}
			if ref, ok := v.Resolve(dir); ok && v.isDir(ref) && last != "" {
				sc.list(ref, search.ParseText(last, sc.pOpts()))
				return
			}
		}
	}
	// Else each part but the last must match a folder above the file, in
	// their order, and the last its name.
	spans := sc.all()
	for _, d := range pq.Dirs {
		spans = v.foldersMatching(sc.ctx, d, spans)
	}
	p := pq.File
	if p == nil {
		p = empty
	}
	sc.p, sc.dirs = p, pq.Dirs
	sc.run(spans, p)
}

func (sc *scan) pOpts() search.Options { return sc.p.Options() }

// list finds the entries of a folder that match a pattern.
func (sc *scan) list(dir Ref, p *search.Pattern) {
	v := sc.v
	sc.scope, sc.scoped = v.canon(dir), true
	sc.skip &^= v.marksOf(dir) & (markHidden | markExcluded)
	sc.p = p
	w := &worker{}
	empty := p.Empty()
	if row, ok := v.folder(sc.scope); ok {
		ix := v.ix
		ix.children(row, func(r Row) bool {
			dir := ix.flags[r]&FlagDir != 0
			name := ix.nameBytes(r)
			if ix.marks[r]&sc.skip != 0 || !sc.filter.Admits(name, dir) {
				return true
			}
			var rank search.Rank
			if !empty {
				var ok bool
				if rank, ok = p.Eval(name, &w.s); !ok {
					return true
				}
			}
			w.add(sc, hit{sc.key(Ref(r), name, dir, ix.marks[r], rank, true), Ref(r)})
			return sc.ctx.Err() == nil
		})
	}
	sc.delta(w, p, true)
	sc.total.Add(w.total)
	sc.hits = append(sc.hits, w.hits...)
}

// results makes results of the best hits, in order.
func (sc *scan) results() []Result {
	if len(sc.hits) > sc.keep {
		selectHits(sc.hits, sc.keep)
		sc.hits = sc.hits[:sc.keep]
	}
	sortMany(sc.hits, hitLess)
	out := make([]Result, len(sc.hits))
	// Many results are made by several goroutines, a stretch each.
	const stretch = 4096
	if parts := min(runtime.GOMAXPROCS(0), len(out)/stretch); parts > 1 {
		var wg sync.WaitGroup
		for p := range parts {
			lo, hi := p*len(out)/parts, (p+1)*len(out)/parts
			wg.Go(func() { sc.fill(out, lo, hi) })
		}
		wg.Wait()
	} else {
		sc.fill(out, 0, len(out))
	}
	SortTies(out)
	return out
}

// fill makes the results of a stretch of the hits.
func (sc *scan) fill(out []Result, lo, hi int) {
	v := sc.v
	// The folders' paths are made once each: results often share them.
	dirs := map[Ref]string{}
	var buf []byte
	for i := lo; i < hi; i++ {
		h := sc.hits[i]
		r := &out[i]
		parent := v.canon(v.parentOf(h.ref))
		dir, ok := dirs[parent]
		if !ok {
			buf = v.appendPath(buf[:0], parent)
			if parent != 0 {
				buf = append(buf, '\\')
			}
			dir = string(buf)
			dirs[parent] = dir
		}
		buf = append(buf[:0], dir...)
		if h.ref.added() || v.ix.marks[h.ref]&markOver != 0 {
			buf = append(buf, v.nameOf(h.ref)...)
		} else {
			buf = append(buf, v.ix.nameBytes(Row(h.ref))...)
		}
		r.Path = string(buf)
		r.Name = r.Path[len(dir):]
		r.Key = h.key
		r.ByFolder = sc.byFolder[h.ref]
		if h.ref.added() {
			d := &v.d.rows[h.ref.at()]
			r.Flags, r.Size = d.flags, d.size
			r.Modified, r.Created, r.Accessed = d.mtime, d.ctime, d.atime
		} else {
			ix := v.ix
			r.Flags, r.Size = ix.flags[h.ref], ix.size[h.ref]
			r.Modified, r.Created, r.Accessed = ix.mtime[h.ref], ix.ctime[h.ref], ix.atime[h.ref]
		}
	}
}

// sortMany sorts as slices.SortFunc does, the many by several goroutines:
// each a part, and the parts merged two and two.
func sortMany[T any](s []T, cmp func(a, b T) int) {
	const part = 4096
	parts := min(runtime.GOMAXPROCS(0), len(s)/part)
	if parts < 2 {
		slices.SortFunc(s, cmp)
		return
	}
	bounds := make([]int, parts+1)
	for p := range bounds {
		bounds[p] = p * len(s) / parts
	}
	var wg sync.WaitGroup
	for p := range parts {
		wg.Go(func() { slices.SortFunc(s[bounds[p]:bounds[p+1]], cmp) })
	}
	wg.Wait()
	src, dst := s, make([]T, len(s))
	for width := 1; width < parts; width *= 2 {
		for p := 0; p < parts; p += 2 * width {
			lo, mid, hi := bounds[p], bounds[min(p+width, parts)], bounds[min(p+2*width, parts)]
			wg.Go(func() {
				a, b, at := src[lo:mid], src[mid:hi], lo
				for len(a) > 0 && len(b) > 0 {
					if cmp(b[0], a[0]) < 0 {
						dst[at], b = b[0], b[1:]
					} else {
						dst[at], a = a[0], a[1:]
					}
					at++
				}
				at += copy(dst[at:], a)
				copy(dst[at:], b)
			})
		}
		wg.Wait()
		src, dst = dst, src
	}
	if &src[0] != &s[0] {
		copy(s, src)
	}
}

// SortTies puts results that are in the order of their keys in their
// last order: those of one key by Less.
func SortTies(out []Result) {
	for i := 0; i < len(out); {
		j := i + 1
		for j < len(out) && out[j].Key == out[i].Key {
			j++
		}
		if j-i > 1 {
			sortTied(out[i:j])
		}
		i = j
	}
}

// sortTied orders results of one key: by their places, which are moved
// once the order is known, as a result is several times a place.
func sortTied(out []Result) {
	if len(out) < 12 {
		slices.SortFunc(out, func(a, b Result) int { return Compare(&a, &b) })
		return
	}
	at := make([]int32, len(out))
	for i := range at {
		at[i] = int32(i)
	}
	sortMany(at, func(a, b int32) int { return Compare(&out[a], &out[b]) })
	sorted := make([]Result, len(out))
	for i, from := range at {
		sorted[i] = out[from]
	}
	copy(out, sorted)
}

// Less orders results: by their keys, and those that match as well as
// each other files first, then by name as people read names, the shorter
// path first.
func Less(a, b *Result) bool { return Compare(a, b) < 0 }

// Compare is the order of Less, as a number below, at or above 0.
func Compare(a, b *Result) int {
	switch {
	case a.Key < b.Key:
		return -1
	case a.Key > b.Key:
		return 1
	}
	if a.IsDir() != b.IsDir() {
		if b.IsDir() {
			return -1
		}
		return 1
	}
	if c := search.CompareNames(a.Name, b.Name); c != 0 {
		return c
	}
	if len(a.Path) != len(b.Path) {
		return len(a.Path) - len(b.Path)
	}
	if c := compareFold(a.Path, b.Path); c != 0 {
		return c
	}
	return strings.Compare(a.Path, b.Path)
}

// compareFold orders paths without the case of their ASCII letters.
func compareFold(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := a[i], b[i]
		if x == y {
			continue
		}
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}
