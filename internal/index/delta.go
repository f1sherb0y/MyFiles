package index

import (
	"sort"
	"strings"
	"sync"

	"myfiles/internal/search"
)

// Ref names a file of a drive: a row of its index, or, with refAdded set,
// one of the delta's.
type Ref uint32

const refAdded Ref = 1 << 31

func (r Ref) added() bool { return r&refAdded != 0 }
func (r Ref) at() int     { return int(r &^ refAdded) }

// delta is what changed on a drive since its index was built: the files
// made, and those whose name, folder, size or time changed, as rows of
// its own. The index's rows they replace are marked gone. A folder of the
// index that was renamed or moved keeps its row, for what it holds to
// stay where it is, and has a row here that says what it is called and
// where it is now.
type delta struct {
	rows []drow
	// byID finds the delta's row of a file by what the file system calls
	// it.
	byID map[uint64]Ref
	// over finds the delta's row of an index's folder renamed or moved,
	// and moved lists those of them that are in another folder now.
	over  map[Row]int
	moved []Row
	// changes counts what was applied, to tell when building the index
	// again is worth it.
	changes int
}

type drow struct {
	name   string
	mask   uint64
	parent Ref
	flags  uint16
	marks  uint8
	size   uint64
	mtime  uint32
	ctime  uint32
	atime  uint32
	id     uint64
	// base is the row of the index's folder this one stands for, 0 for a
	// file or a folder of the delta's own.
	base Row
}

// View is an index with what changed since: what a search reads.
type View struct {
	ix *Index
	d  *delta
	// gen counts the changes of the view, and kept are the last searches
	// with what they found, to answer the same search again while nothing
	// changed.
	gen   uint64
	keptM sync.Mutex
	kept  [4]keptSearch
	keptN int
}

// Change is a file as it is now, for the view to follow.
type Change struct {
	Entry
	Name string
}

func newDelta() *delta {
	return &delta{byID: map[uint64]Ref{}, over: map[Row]int{}}
}

// find is the file the file system calls id, as the view has it now.
func (v *View) find(id uint64) (Ref, bool) {
	if r, ok := v.d.byID[id]; ok {
		if v.d.rows[r.at()].marks&markGone != 0 {
			return 0, false
		}
		return r, true
	}
	if r := v.ix.rowOf(id); r != 0 {
		if m := v.ix.marks[r]; m&markOver != 0 {
			return refAdded | Ref(v.d.over[r]), true
		} else if m&markGone == 0 {
			return Ref(r), true
		}
	}
	return 0, false
}

// isDir, nameOf and parentOf read a file's row, wherever it is.
func (v *View) isDir(r Ref) bool {
	if r.added() {
		return v.d.rows[r.at()].flags&FlagDir != 0
	}
	return v.ix.flags[r]&FlagDir != 0
}

func (v *View) nameOf(r Ref) string {
	if r.added() {
		return v.d.rows[r.at()].name
	}
	if v.ix.marks[r]&markOver != 0 {
		return v.d.rows[v.d.over[Row(r)]].name
	}
	return v.ix.Name(Row(r))
}

func (v *View) parentOf(r Ref) Ref {
	if r.added() {
		return v.d.rows[r.at()].parent
	}
	if v.ix.marks[r]&markOver != 0 {
		return v.d.rows[v.d.over[Row(r)]].parent
	}
	return Ref(v.ix.parent[r])
}

// folder is the row of the index a folder's files are under, for a
// folder that has one: its own, or the one it stands for.
func (v *View) folder(r Ref) (Row, bool) {
	if !r.added() {
		return Row(r), true
	}
	if base := v.d.rows[r.at()].base; base != 0 {
		return base, true
	}
	return 0, false
}

// canon is how a folder is named as a parent: an index's folder by its
// row there, also when the delta has a row for it.
func (v *View) canon(r Ref) Ref {
	if r.added() {
		if base := v.d.rows[r.at()].base; base != 0 {
			return Ref(base)
		}
	}
	return r
}

// Path is a file's path.
func (v *View) Path(r Ref) string {
	return string(v.appendPath(nil, r))
}

func (v *View) appendPath(buf []byte, r Ref) []byte {
	if !r.added() && len(v.d.over) == 0 {
		return v.ix.appendPath(buf, Row(r))
	}
	var chain [64]Ref
	refs := chain[:0]
	for r != 0 && len(refs) < 4096 {
		refs = append(refs, r)
		r = v.parentOf(r)
	}
	buf = append(buf, v.ix.Root...)
	for i := len(refs) - 1; i >= 0; i-- {
		buf = append(buf, v.nameOf(refs[i])...)
		if i > 0 {
			buf = append(buf, '\\')
		}
	}
	return buf
}

// under reports whether a file is in a folder, however deep. Both are
// named as canon names them.
func (v *View) under(r, dir Ref) bool {
	for n := 0; r != 0 && n < 4096; n++ {
		r = v.canon(v.parentOf(r))
		if r == dir {
			return true
		}
	}
	return dir == 0
}

// resultOf is a file as a search would tell of it.
func (v *View) resultOf(ref Ref) Result {
	path := v.Path(ref)
	r := Result{Path: path, Name: v.nameOf(ref)}
	if ref.added() {
		d := &v.d.rows[ref.at()]
		r.Flags, r.Size = d.flags, d.size
		r.Modified, r.Created, r.Accessed = d.mtime, d.ctime, d.atime
		return r
	}
	if v.ix.marks[ref]&markOver != 0 {
		d := &v.d.rows[v.d.over[Row(ref)]]
		r.Flags, r.Size = d.flags, d.size
		r.Modified, r.Created, r.Accessed = d.mtime, d.ctime, d.atime
		return r
	}
	ix := v.ix
	r.Flags, r.Size = ix.flags[ref], ix.size[ref]
	r.Modified, r.Created, r.Accessed = ix.mtime[ref], ix.ctime[ref], ix.atime[ref]
	return r
}

// Resolve is the file of a path, as the view has it now.
func (v *View) Resolve(path string) (Ref, bool) {
	rest, ok := v.ix.rel(path)
	if !ok {
		return 0, false
	}
	cur := Ref(0)
	for rest != "" {
		seg := rest
		if i := strings.IndexByte(rest, '\\'); i >= 0 {
			seg, rest = rest[:i], strings.TrimLeft(rest[i:], `\`)
		} else {
			rest = ""
		}
		next, ok := v.childOf(cur, seg)
		if !ok {
			return 0, false
		}
		cur = next
	}
	return cur, true
}

// childOf is the entry of a name in a folder.
func (v *View) childOf(dir Ref, name string) (Ref, bool) {
	dir = v.canon(dir)
	if row, ok := v.folder(dir); ok {
		found, ok := Row(0), false
		v.ix.children(row, func(c Row) bool {
			// A folder renamed is under its new name in the delta, and
			// one moved away is not here.
			if v.ix.marks[c]&(markGone|markOver) == 0 && equalFold(v.ix.nameBytes(c), name) {
				found, ok = c, true
				return false
			}
			return true
		})
		if ok {
			return Ref(found), true
		}
	}
	for i := range v.d.rows {
		if d := &v.d.rows[i]; d.marks&markGone == 0 && d.parent == dir && strings.EqualFold(d.name, name) {
			return refAdded | Ref(i), true
		}
	}
	return 0, false
}

// inherit is what a file takes of its folder's marks.
const inherit = markHidden | markExcluded | markLow | markHigh | markPinned

func (v *View) marksOf(r Ref) uint8 {
	if r.added() {
		return v.d.rows[r.at()].marks
	}
	if v.ix.marks[r]&markOver != 0 {
		return v.d.rows[v.d.over[Row(r)]].marks
	}
	return v.ix.marks[r]
}

// Apply makes the view follow a file as it is now: made, renamed, moved,
// or changed. rules says what the user's rules make of its name.
func (v *View) Apply(c Change, rules *Rules) bool {
	parent, ok := v.find(c.Parent)
	if !ok && c.Parent != v.ix.id[0] {
		return false
	}
	parent = v.canon(parent)
	v.d.changes++
	v.gen++
	marks := v.marksOf(parent)&inherit | rules.own(c.Name, c.Flags)
	if c.Flags&FlagHidden != 0 {
		marks |= markHidden
	}
	row := drow{
		name: c.Name, mask: search.Mask([]byte(c.Name)), parent: parent, flags: c.Flags, marks: marks,
		size: c.Size, mtime: c.Modified, ctime: c.Created, atime: c.Accessed, id: c.ID,
	}
	if cur, ok := v.find(c.ID); ok {
		if cur.added() {
			// The delta's own row follows.
			d := &v.d.rows[cur.at()]
			if d.base != 0 && d.parent != parent && !containsRow(v.d.moved, d.base) {
				v.d.moved = append(v.d.moved, d.base)
				sort.Slice(v.d.moved, func(i, j int) bool { return v.d.moved[i] < v.d.moved[j] })
			}
			row.base = d.base
			*d = row
			return true
		}
		base := Row(cur)
		if v.ix.flags[base]&FlagDir != 0 && c.Flags&FlagDir != 0 {
			if equalExact(v.ix.nameBytes(base), c.Name) && Ref(v.ix.parent[base]) == parent {
				// A folder whose times changed: nothing a search shows.
				return true
			}
			// What it holds stays where it is, under the row it has.
			row.base = base
			v.ix.marks[base] |= markOver
			v.d.over[base] = len(v.d.rows)
			if Ref(v.ix.parent[base]) != parent {
				v.d.moved = append(v.d.moved, base)
				sort.Slice(v.d.moved, func(i, j int) bool { return v.d.moved[i] < v.d.moved[j] })
			}
		} else {
			v.ix.marks[base] |= markGone
		}
	}
	v.d.byID[c.ID] = refAdded | Ref(len(v.d.rows))
	v.d.rows = append(v.d.rows, row)
	return true
}

func containsRow(rows []Row, r Row) bool {
	for _, x := range rows {
		if x == r {
			return true
		}
	}
	return false
}

func equalExact(a []byte, b string) bool { return string(a) == b }

// Remove makes the view forget a file that is gone, with what it held.
func (v *View) Remove(id uint64) bool {
	cur, ok := v.find(id)
	if !ok {
		return false
	}
	v.d.changes++
	v.gen++
	v.removeRef(cur)
	return true
}

func (v *View) removeRef(cur Ref) {
	dir := v.isDir(cur)
	canon := v.canon(cur)
	if cur.added() {
		v.d.rows[cur.at()].marks |= markGone
	}
	if row, ok := v.folder(cur); ok {
		// The index's row and, for a folder, all it holds.
		end := row + 1
		if dir {
			end = v.ix.end[row]
		}
		for r := row; r < end; r++ {
			v.ix.marks[r] |= markGone
		}
	}
	if !dir {
		return
	}
	// What the delta has under it.
	for i := range v.d.rows {
		if d := &v.d.rows[i]; d.marks&markGone == 0 && v.under(refAdded|Ref(i), canon) {
			d.marks |= markGone
			if d.base != 0 {
				for r := d.base; r < v.ix.end[d.base]; r++ {
					v.ix.marks[r] |= markGone
				}
			}
		}
	}
}

// movedIn reports whether a folder of the index that was moved is now
// under dir.
func (v *View) movedIn(row Row, dir Ref) bool {
	return v.under(refAdded|Ref(v.d.over[row]), dir)
}

// span is a run of rows.
type span struct{ from, to Row }

// spans are the runs of the index's rows that are under a folder now:
// the run it had when the index was built, without the folders moved out
// of it since, and with those moved into it.
func (v *View) spans(dir Ref) []span {
	var base span
	row, has := v.folder(dir)
	if has {
		base = span{row + 1, v.ix.end[row]}
	}
	if len(v.d.moved) == 0 {
		if !has || base.from >= base.to {
			return nil
		}
		return []span{base}
	}
	canon := v.canon(dir)
	// Each moved folder's run is in or out as a whole, but for the moved
	// folders inside it: from the outermost in.
	type cut struct {
		span
		in bool
	}
	cuts := make([]cut, 0, len(v.d.moved))
	for _, m := range v.d.moved {
		cuts = append(cuts, cut{span{m, v.ix.end[m]}, v.movedIn(m, canon)})
	}
	var out []span
	var walk func(lo, hi Row, in bool, from int) int
	walk = func(lo, hi Row, in bool, from int) int {
		at := lo
		i := from
		for i < len(cuts) && cuts[i].from < hi {
			c := cuts[i]
			if c.from < lo {
				i++
				continue
			}
			if in && c.from > at {
				out = append(out, span{at, c.from})
			}
			// The moved folder's own row is the delta's to match.
			i = walk(c.from+1, c.to, c.in, i+1)
			at = c.to
		}
		if in && hi > at {
			out = append(out, span{at, hi})
		}
		return i
	}
	// Inside the folder's own run, then the folders moved in from
	// elsewhere.
	if has {
		first := sort.Search(len(cuts), func(i int) bool { return cuts[i].from >= base.from })
		walk(base.from, base.to, true, first)
	}
	for i := 0; i < len(cuts); {
		c := cuts[i]
		if has && c.from >= base.from && c.from < base.to {
			i++
			continue
		}
		i = walk(c.from+1, c.to, c.in, i+1)
	}
	return out
}

// NewView is a view of an index as it was built, marked by the rules.
func NewView(ix *Index, rules *Rules) *View {
	v := &View{ix: ix, d: newDelta()}
	if ix.marks == nil {
		ix.marks = make([]uint8, ix.n)
	}
	v.mark(rules)
	// Marking read every name: of an index in a file, more than the
	// searches to come need in memory.
	ix.Rest()
	return v
}

// Index is the index the view is of.
func (v *View) Index() *Index { return v.ix }

// Changes counts what changed since the index was built.
func (v *View) Changes() int { return v.d.changes }

// Mark works the marks out again, as after the rules changed.
func (v *View) Mark(rules *Rules) {
	v.gen++
	v.mark(rules)
	v.ix.Rest()
}

// idOf is what the file system calls a file.
func (v *View) idOf(r Ref) uint64 {
	if r.added() {
		return v.d.rows[r.at()].id
	}
	return v.ix.id[r]
}

// rescan is the view as a scan: what the index holds that is still
// there, and what changed since, in a scratch's memory.
func (v *View) rescan(sc *Scratch) *Scan {
	ix := v.ix
	// In the order of the rows, which is the order they are wanted in.
	sc.Near()
	s := NewScan(sc)
	for r := 1; r < ix.n; r++ {
		if ix.marks[r]&(markGone|markOver) != 0 {
			continue
		}
		s.addBytes(Entry{
			ID: ix.id[r], Parent: ix.id[ix.parent[r]], Flags: ix.flags[r], Size: ix.size[r],
			Modified: ix.mtime[r], Created: ix.ctime[r], Accessed: ix.atime[r],
		}, ix.nameBytes(Row(r)))
	}
	for i := range v.d.rows {
		d := &v.d.rows[i]
		if d.marks&markGone != 0 {
			continue
		}
		s.AddString(Entry{
			ID: d.id, Parent: v.idOf(d.parent), Flags: d.flags, Size: d.size,
			Modified: d.mtime, Created: d.ctime, Accessed: d.atime,
		}, d.name)
	}
	return s
}

// Compact builds the index again with what changed since, in memory, and
// returns a view of it with nothing beside it.
func (v *View) Compact(rules *Rules) *View {
	ix := v.ix
	out := Build(ix.Root, ix.id[0], []*Scan{v.rescan(nil)}, ix.byRec != nil)
	meta := ix.Meta
	meta.Files, meta.Dirs = out.Meta.Files, out.Meta.Dirs
	out.Meta = meta
	return NewView(out, rules)
}
