// Package index keeps the names of the files of whole drives, and finds
// among them what a query asks for.
//
// An Index is the files of one drive, or of one folder, as columns: a row
// a file, the rows in the order a walk of the tree meets them, every
// folder before what it holds. What a folder holds, however deep, is then
// one run of rows, so a search of one folder reads one stretch of memory
// and nothing else. A drive's index is a file, made in the file and mapped
// from it: it is there the moment the program starts, the system keeps of
// it in memory what searches touch, and the program's own memory holds a
// byte a row.
//
// What changes on the drive afterwards is kept beside the columns (delta)
// until the index is built again with it, into a new file.
package index

import (
	"bytes"
	"strings"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"

	"myfiles/internal/search"
)

// The flags of a row: what the file is.
const (
	FlagDir uint16 = 1 << iota
	FlagHidden
	FlagSystem
	FlagReadOnly
	// FlagLink is a junction or a symbolic link.
	FlagLink
	// FlagOffline is a file whose content is not on the disk.
	FlagOffline
)

// The marks of a row: what a search skips, or orders by. They are worked
// out of the rows and the user's rules, and kept in memory only.
const (
	// markGone is a row whose file is no longer there, or whose entry is
	// kept elsewhere since it changed.
	markGone uint8 = 1 << iota
	// markHidden is a row that is hidden, or in a hidden folder.
	markHidden
	// markExcluded is a row a rule leaves out.
	markExcluded
	// markLow and markHigh are rows a rule orders after, or before, the
	// others; markPinned puts a high one before everything.
	markLow
	markHigh
	markPinned
	// markOver is a folder renamed or moved since the index was built:
	// its name and the folder it is in are in the delta.
	markOver
)

// Row is a row of an index.
type Row = uint32

// Index is the files under a root, as columns.
type Index struct {
	// Root is the path the rows are under, with its trailing separator:
	// "C:\" for a drive.
	Root string
	Meta Meta

	n int
	// names are the names one after the other, in UTF-8, and nameOff
	// where each starts: one more than there are rows.
	names   []byte
	nameOff []uint32
	// mask tells which characters each name can answer to.
	mask []uint64
	// parent is the row of the folder each row is in, and end the row
	// after the last of what a folder holds: the row after itself for a
	// file.
	parent []uint32
	end    []uint32
	flags  []uint16
	size   []uint64
	// The times are in seconds since 1970.
	mtime, ctime, atime []uint32
	// id is what the file system calls the file, which the journal of
	// changes names it by; byRec, for a drive with such a journal, the
	// row of the file of each record of the file table.
	id    []uint64
	byRec []uint32

	// marks are worked out after the index is built or read. For an
	// index in a file they are in a file too, beside it for as long as the
	// index is open (side, in dir): a byte a row of every drive is more
	// than the program's own memory need hold.
	marks []uint8
	side  *Scratch
	dir   string

	// mapped is the file the columns are in, and release unmaps it. cold
	// is set while the system has its memory back.
	mapped  []byte
	release func()
	cold    atomic.Bool
}

// Meta is what an index knows of where it came from.
type Meta struct {
	// Serial is the serial number of the volume.
	Serial uint32
	// FileSystem is the volume's, as "NTFS".
	FileSystem string
	// JournalID and NextUSN are the journal of changes the index is up
	// to date with, and where in it: 0 for an index nothing keeps up to
	// date.
	JournalID uint64
	NextUSN   int64
	// Built is when the drive was read, in seconds since 1970, and Took
	// how long that took, in milliseconds.
	Built int64
	Took  int64
	// Table is set for an index read from the drive's file table rather
	// than by walking its folders.
	Table bool
	// Files and Dirs count the rows.
	Files, Dirs int
}

// Len is the number of rows.
func (ix *Index) Len() int { return ix.n }

// Name is a row's name.
func (ix *Index) Name(r Row) string {
	b := ix.nameBytes(r)
	return string(b)
}

func (ix *Index) nameBytes(r Row) []byte {
	return ix.names[ix.nameOff[r]:ix.nameOff[r+1]]
}

// IsDir reports whether a row is a folder.
func (ix *Index) IsDir(r Row) bool { return ix.flags[r]&FlagDir != 0 }

// Close lets go of the file the index is mapped from.
func (ix *Index) Close() {
	if ix.release != nil {
		ix.mapped = nil
		ix.release()
		ix.release = nil
	}
	if ix.side != nil {
		ix.marks = nil
		ix.side.Close()
		ix.side = nil
	}
}

// Rest gives back the memory the searches so far brought in of the
// index's file. The next search brings in what it touches.
func (ix *Index) Rest() {
	if ix.mapped == nil {
		return
	}
	rest(ix.mapped)
	if ix.side != nil {
		rest(ix.marks)
	}
	ix.cold.Store(true)
}

// warm brings in what a scan of stretches of rows reads of every row,
// when the index's memory was given back since it was last read: all at
// once, where the scan would bring it in a page at a time.
func (ix *Index) warm(spans []span) {
	if !ix.cold.Load() || len(spans) == 0 {
		return
	}
	lo, hi := spans[0].from, spans[0].to
	for _, s := range spans[1:] {
		lo, hi = min(lo, s.from), max(hi, s.to)
	}
	if hi-lo < 1<<16 {
		// A folder's worth: as it comes.
		return
	}
	var marks []byte
	if ix.side != nil {
		marks = ix.marks[lo:hi]
	}
	warm(bytesOf(ix.mask[lo:hi]), bytesOf(ix.flags[lo:hi]), marks)
	if int(hi-lo) >= ix.n/2 {
		ix.cold.Store(false)
	}
}

// children calls fn with each row a folder holds directly, until fn
// returns false.
func (ix *Index) children(dir Row, fn func(Row) bool) {
	for c := dir + 1; c < ix.end[dir]; c = ix.end[c] {
		if !fn(c) {
			return
		}
	}
}

// child is the row of a folder's entry of a name, compared without case.
func (ix *Index) child(dir Row, name string) (Row, bool) {
	found, ok := Row(0), false
	ix.children(dir, func(c Row) bool {
		if ix.marks[c]&markGone == 0 && equalFold(ix.nameBytes(c), name) {
			found, ok = c, true
			return false
		}
		return true
	})
	return found, ok
}

// equalFold reports whether a name is another, without case.
func equalFold(a []byte, b string) bool {
	if len(a) == len(b) {
		same := true
		for i := 0; i < len(a); i++ {
			x, y := a[i], b[i]
			if x >= 0x80 || y >= 0x80 {
				same = false
				break
			}
			if x != y && (x|0x20 != y|0x20 || x|0x20 < 'a' || x|0x20 > 'z') {
				return false
			}
		}
		if same {
			return true
		}
	}
	// The string's bytes as they are, to read: a copy for each name of a
	// drive would be millions.
	return bytes.EqualFold(a, unsafe.Slice(unsafe.StringData(b), len(b)))
}

// rel is a path's part under the index's root, and whether it is under
// it at all.
func (ix *Index) rel(path string) (string, bool) {
	root := strings.TrimRight(ix.Root, `\`)
	if len(path) < len(root) || !strings.EqualFold(path[:len(root)], root) {
		return "", false
	}
	rest := path[len(root):]
	if rest != "" && rest[0] != '\\' {
		return "", false
	}
	return strings.Trim(rest, `\`), true
}

// appendPath appends a row's path to buf.
func (ix *Index) appendPath(buf []byte, r Row) []byte {
	var chain [64]Row
	rows := chain[:0]
	for r != 0 {
		rows = append(rows, r)
		r = ix.parent[r]
	}
	buf = append(buf, ix.Root...)
	for i := len(rows) - 1; i >= 0; i-- {
		buf = append(buf, ix.nameBytes(rows[i])...)
		if i > 0 {
			buf = append(buf, '\\')
		}
	}
	return buf
}

// depth is how many folders a row is under, below the root.
func (ix *Index) depth(r Row) int {
	n := 0
	for r = ix.parent[r]; r != 0; r = ix.parent[r] {
		n++
	}
	return n
}

// Scan is what reading a drive, or a folder, found: files in no order,
// each with what the file system calls it and the folder it is in. Several
// goroutines read a drive, each into a Scan of its own. What it holds is
// kept in pieces of a Scratch's memory: a scan of millions of files is
// not in the program's own.
type Scan struct {
	sc *Scratch
	// recs are pieces of scanPiece files each, the last partly used, and
	// n how many files there are in all.
	recs [][]scanRec
	n    int
	// names are pieces of namePiece bytes, each as far used as its
	// length says.
	names [][]byte
}

const (
	scanBits  = 14
	scanPiece = 1 << scanBits
	nameBits  = 18
	namePiece = 1 << nameBits
)

// NewScan is a scan that keeps what it finds in a scratch's memory.
func NewScan(sc *Scratch) *Scan { return &Scan{sc: sc} }

type scanRec struct {
	id, parent uint64
	size       uint64
	// nameAt is where the name is: the piece of names, and where in it.
	nameAt              uint32
	nameLen             uint16
	flags               uint16
	mtime, ctime, atime uint32
}

// Entry is a file as a scan tells of it.
type Entry struct {
	// ID is what the file system calls the file, and Parent the folder it
	// is in.
	ID, Parent uint64
	Flags      uint16
	Size       uint64
	// The times are in seconds since 1970.
	Modified, Created, Accessed uint32
}

// room is a piece of names with room for n bytes more.
func (s *Scan) room(n int) *[]byte {
	if k := len(s.names); k > 0 && cap(s.names[k-1])-len(s.names[k-1]) >= n {
		return &s.names[k-1]
	}
	s.names = append(s.names, s.sc.Bytes(max(namePiece, n))[:0])
	return &s.names[len(s.names)-1]
}

// Add adds a file, by its name in UTF-16 as the system gives it.
func (s *Scan) Add(e Entry, name []uint16) {
	p := s.room(3 * len(name))
	off := len(*p)
	*p = appendUTF16(*p, name)
	s.add(e, p, off)
}

// AddString adds a file by its name.
func (s *Scan) AddString(e Entry, name string) {
	p := s.room(len(name))
	off := len(*p)
	*p = append(*p, name...)
	s.add(e, p, off)
}

// addBytes adds a file by its name in UTF-8.
func (s *Scan) addBytes(e Entry, name []byte) {
	p := s.room(len(name))
	off := len(*p)
	*p = append(*p, name...)
	s.add(e, p, off)
}

func (s *Scan) add(e Entry, p *[]byte, off int) {
	n := len(*p) - off
	if n == 0 || n > 0xFFFF || off >= namePiece {
		*p = (*p)[:off]
		return
	}
	if s.n>>scanBits == len(s.recs) {
		s.recs = append(s.recs, scratchOf[scanRec](s.sc, scanPiece))
	}
	s.recs[s.n>>scanBits][s.n&(scanPiece-1)] = scanRec{
		id: e.ID, parent: e.Parent, size: e.Size,
		nameAt: uint32(len(s.names)-1)<<nameBits | uint32(off), nameLen: uint16(n),
		flags: e.Flags, mtime: e.Modified, ctime: e.Created, atime: e.Accessed,
	}
	s.n++
}

// rec is the scan's file of a number.
func (s *Scan) rec(i int) *scanRec { return &s.recs[i>>scanBits][i&(scanPiece-1)] }

// name is a file's name.
func (s *Scan) name(r *scanRec) []byte {
	off := r.nameAt & (namePiece - 1)
	return s.names[r.nameAt>>nameBits][off : off+uint32(r.nameLen)]
}

// Len is the number of files found.
func (s *Scan) Len() int { return s.n }

// appendUTF16 appends s to b as UTF-8.
func appendUTF16(b []byte, s []uint16) []byte {
	for i := 0; i < len(s); i++ {
		u := rune(s[i])
		switch {
		case u < 0x80:
			b = append(b, byte(u))
		case u >= 0xD800 && u < 0xDC00 && i+1 < len(s) && s[i+1] >= 0xDC00 && s[i+1] < 0xE000:
			b = utf8.AppendRune(b, 0x10000+(u-0xD800)<<10+(rune(s[i+1])-0xDC00))
			i++
		case u >= 0xD800 && u < 0xE000:
			b = utf8.AppendRune(b, utf8.RuneError)
		default:
			b = utf8.AppendRune(b, u)
		}
	}
	return b
}

// Build makes an index of what scans found under a root, in the
// program's own memory: for tests, and for what is small. rootID is what
// the file system calls the root; files whose folders do not lead to it
// are left out. tracked asks for the table that finds a file by what the
// journal of changes calls it.
func Build(root string, rootID uint64, scans []*Scan, tracked bool) *Index {
	ix := build(root, rootID, scans, tracked, nil, func(rows, names, recs int) ([][]byte, error) {
		sizes := sectionSizes(rows, names, recs)
		out := make([][]byte, len(sizes))
		for i, size := range sizes {
			// In words of 8 bytes: the columns of them start where they
			// must.
			out[i] = bytesOf(make([]uint64, (size+7)/8))[:size]
		}
		return out, nil
	})
	return ix
}

// at names a file of the scans: the scan, and the file's number in it.
type at struct {
	scan, rec uint32
}

// build makes an index of what scans found, in the memory columns gives
// for as many rows, bytes of names and entries of the table of records as
// there may be at most: nil when that memory is not to be had. What it
// needs beside is the scratch's.
func build(root string, rootID uint64, scans []*Scan, tracked bool, sc *Scratch,
	columns func(rows, names, recs int) ([][]byte, error)) *Index {
	// How much there is: the files, the folders among them, the bytes of
	// their names, and the highest record of the file table.
	total, dirCount, nameBytes := 0, 0, 0
	var maxRec uint64
	sc.read()
	for _, s := range scans {
		sc.settle()
		total += s.n
		for i := range s.n {
			rec := s.rec(i)
			nameBytes += int(rec.nameLen)
			if rec.flags&FlagDir != 0 {
				dirCount++
			}
			if r := rec.id & recMask; r > maxRec {
				maxRec = r
			}
		}
	}
	recs := 0
	if tracked {
		recs = int(maxRec) + 1
	}
	// The folders, by what the file system calls them: every file names
	// the one it is in.
	dirs := newIDTable(sc, dirCount+1)
	dirAt := scratchOf[at](sc, dirCount+1)
	nd := uint32(1)
	dirs.put(rootID, 0)
	for si, s := range scans {
		sc.settle()
		for ri := range s.n {
			if rec := s.rec(ri); rec.flags&FlagDir != 0 && rec.id != rootID {
				if !dirs.put(rec.id, nd) {
					dirAt[nd] = at{uint32(si), uint32(ri)}
					nd++
				}
			}
		}
	}
	// What each folder holds: counted, then placed.
	start := scratchOf[uint32](sc, int(nd)+1)
	for _, s := range scans {
		sc.settle()
		for ri := range s.n {
			rec := s.rec(ri)
			if rec.id == rootID {
				continue
			}
			if d, ok := dirs.get(rec.parent); ok {
				start[d+1]++
			}
		}
	}
	for i := 1; i < len(start); i++ {
		start[i] += start[i-1]
	}
	kids := scratchOf[at](sc, int(start[nd]))
	fill := scratchOf[uint32](sc, int(nd))
	copy(fill, start)
	for si, s := range scans {
		sc.settle()
		for ri := range s.n {
			rec := s.rec(ri)
			if rec.id == rootID {
				continue
			}
			if d, ok := dirs.get(rec.parent); ok {
				// A folder is held once, where it was first found.
				if rec.flags&FlagDir != 0 {
					self, _ := dirs.get(rec.id)
					if here := dirAt[self]; here.scan != uint32(si) || here.rec != uint32(ri) {
						continue
					}
				}
				kids[fill[d]] = at{uint32(si), uint32(ri)}
				fill[d]++
			}
		}
	}

	mem, err := columns(total+1, nameBytes, recs)
	if err != nil {
		return nil
	}
	ix := &Index{Root: root}
	ix.carve(mem, total+1, nameBytes, recs)
	ix.names = ix.names[:0]

	// The walk: a folder, then what it holds, the folders among it walked
	// in turn.
	type frame struct {
		dir  uint32
		row  Row
		next uint32
	}
	n := 1
	ix.flags[0] = FlagDir
	ix.id[0] = rootID
	stack := []frame{{dir: 0, row: 0, next: start[0]}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.next == fill[f.dir] {
			ix.end[f.row] = uint32(n)
			stack = stack[:len(stack)-1]
			continue
		}
		k := kids[f.next]
		f.next++
		if sc != nil && n&(1<<18-1) == 0 {
			// The columns are a file's, written from their starts on: what
			// is written need not stay in memory for the rest to be.
			for _, col := range mem {
				rest(col)
			}
			sc.settle()
		}
		s := scans[k.scan]
		rec := s.rec(int(k.rec))
		row := Row(n)
		n++
		name := s.name(rec)
		ix.nameOff[row] = uint32(len(ix.names))
		ix.names = append(ix.names, name...)
		ix.mask[row] = search.Mask(name)
		ix.parent[row] = f.row
		ix.end[row] = row + 1
		ix.flags[row] = rec.flags
		ix.size[row] = rec.size
		ix.mtime[row], ix.ctime[row], ix.atime[row] = rec.mtime, rec.ctime, rec.atime
		ix.id[row] = rec.id
		if rec.flags&FlagDir != 0 {
			ix.Meta.Dirs++
			d, _ := dirs.get(rec.id)
			// A loop in the folders, which no file system should have,
			// would never end: a folder walked once is not walked again.
			if fill[d] != start[d] && rec.flags&FlagLink == 0 && len(stack) < 4096 {
				stack = append(stack, frame{dir: d, row: row, next: start[d]})
			}
		} else {
			ix.Meta.Files++
		}
	}
	ix.nameOff[n] = uint32(len(ix.names))
	ix.truncate(n)
	if tracked {
		for r := 1; r < n; r++ {
			ix.byRec[ix.id[r]&recMask] = uint32(r)
		}
	}
	return ix
}

// recMask takes the number of a file's record in the file table out of
// what the file system calls the file: the rest counts how often the
// record was used.
const recMask = 1<<48 - 1

// truncate cuts the columns to the rows used.
func (ix *Index) truncate(n int) {
	ix.n = n
	ix.nameOff = ix.nameOff[:n+1]
	ix.mask = ix.mask[:n]
	ix.parent = ix.parent[:n]
	ix.end = ix.end[:n]
	ix.flags = ix.flags[:n]
	ix.size = ix.size[:n]
	ix.mtime = ix.mtime[:n]
	ix.ctime = ix.ctime[:n]
	ix.atime = ix.atime[:n]
	ix.id = ix.id[:n]
}

// rowOf is the row of the file the file system calls id, 0 for none.
func (ix *Index) rowOf(id uint64) Row {
	if rec := id & recMask; rec < uint64(len(ix.byRec)) {
		if r := ix.byRec[rec]; r != 0 && ix.id[r] == id {
			return r
		}
	}
	return 0
}
