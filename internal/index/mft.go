package index

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// NTFS keeps a record of every file of a volume in one file, the master
// file table: its name, the folder it is in, its size and its times.
// Reading that file from the volume gives the names of all the volume's
// files without opening a folder, at the speed the disk reads. It takes
// an administrator's rights: the volume is read under the file system.

var le = binary.LittleEndian

// The types of the attributes of a record that are read.
const (
	attrStandardInformation = 0x10
	attrAttributeList       = 0x20
	attrFileName            = 0x30
	attrData                = 0x80
	attrReparsePoint        = 0xC0
	attrEnd                 = 0xFFFFFFFF
)

// run is a stretch of a file on the volume: length clusters from lcn,
// which are the file's clusters from vcn.
type run struct {
	vcn, lcn, length int64
}

// table is the master file table of a volume, as it is read.
type table struct {
	r       io.ReaderAt
	cluster int64
	record  int
	runs    []run
	// valid is how many bytes of the table hold records.
	valid int64
}

// errNotNTFS is returned for a volume of another file system.
var errNotNTFS = errors.New("index: the volume is not NTFS")

// openTable reads where the table is from the volume's first sector and
// the table's own record.
func openTable(r io.ReaderAt) (*table, error) {
	boot := make([]byte, 512)
	if _, err := r.ReadAt(boot, 0); err != nil {
		return nil, err
	}
	if string(boot[3:11]) != "NTFS    " {
		return nil, errNotNTFS
	}
	sector := int64(le.Uint16(boot[0x0B:]))
	perCluster := int64(boot[0x0D])
	if perCluster > 0x80 {
		perCluster = 1 << (256 - perCluster)
	}
	t := &table{r: r, cluster: sector * perCluster}
	if size := int8(boot[0x40]); size > 0 {
		t.record = int(size) * int(t.cluster)
	} else {
		t.record = 1 << -size
	}
	if t.cluster <= 0 || t.record < 512 || t.record > 1<<16 || t.record%512 != 0 {
		return nil, errors.New("index: the volume's first sector does not read as NTFS's")
	}
	// The table's first record is the table's own: where its parts are.
	first := make([]byte, t.record)
	if _, err := r.ReadAt(first, int64(le.Uint64(boot[0x30:]))*t.cluster); err != nil {
		return nil, err
	}
	if string(first[:4]) != "FILE" || !fixup(first) {
		return nil, errors.New("index: the file table's own record does not read")
	}
	var list []byte
	eachAttr(first, func(typ uint32, a []byte) bool {
		switch typ {
		case attrData:
			if a[8] != 0 && a[9] == 0 && len(a) >= 0x40 {
				t.valid = int64(le.Uint64(a[0x38:]))
				t.runs = append(t.runs, parseRuns(a, int64(le.Uint64(a[0x10:])))...)
			}
		case attrAttributeList:
			list = t.value(a, 16<<20)
		}
		return true
	})
	if len(t.runs) == 0 {
		return nil, errors.New("index: the file table has no place on the volume")
	}
	// A table in many pieces lists the rest of them in other records of
	// its own, which are in the pieces known so far.
	seen := map[uint64]bool{0: true}
	for off := 0; off+0x1A <= len(list); {
		length := int(le.Uint16(list[off+4:]))
		if length < 0x1A || off+length > len(list) {
			break
		}
		ref := le.Uint64(list[off+0x10:]) & recMask
		if le.Uint32(list[off:]) == attrData && list[off+6] == 0 && !seen[ref] {
			seen[ref] = true
			rec := make([]byte, t.record)
			if t.readAt(rec, int64(ref)*int64(t.record)) == nil && string(rec[:4]) == "FILE" && fixup(rec) {
				eachAttr(rec, func(typ uint32, a []byte) bool {
					if typ == attrData && a[8] != 0 && a[9] == 0 && len(a) >= 0x40 {
						t.runs = append(t.runs, parseRuns(a, int64(le.Uint64(a[0x10:])))...)
						sort.Slice(t.runs, func(i, j int) bool { return t.runs[i].vcn < t.runs[j].vcn })
					}
					return true
				})
			}
		}
		off += length
	}
	if t.valid <= 0 {
		return nil, errors.New("index: the file table is empty")
	}
	return t, nil
}

// value is the content of an attribute, read from the volume when it is
// not in the record; nil for one longer than most.
func (t *table) value(a []byte, most int64) []byte {
	if a[8] == 0 {
		if len(a) < 0x18 {
			return nil
		}
		off, n := int(le.Uint16(a[0x14:])), int(le.Uint32(a[0x10:]))
		if off+n > len(a) {
			return nil
		}
		return a[off : off+n]
	}
	if len(a) < 0x40 {
		return nil
	}
	size := int64(le.Uint64(a[0x30:]))
	if size <= 0 || size > most {
		return nil
	}
	out := make([]byte, 0, size)
	for _, r := range parseRuns(a, 0) {
		part := make([]byte, r.length*t.cluster)
		if _, err := t.r.ReadAt(part, r.lcn*t.cluster); err != nil {
			return nil
		}
		out = append(out, part...)
	}
	if int64(len(out)) > size {
		out = out[:size]
	}
	return out
}

// parseRuns reads where on the volume an attribute's content is. Each
// run is written as its length and how far it starts from the one before.
func parseRuns(a []byte, vcn int64) []run {
	off := int(le.Uint16(a[0x20:]))
	if off >= len(a) {
		return nil
	}
	b := a[off:]
	var runs []run
	lcn := int64(0)
	for len(b) > 0 && b[0] != 0 {
		ln, on := int(b[0]&0xF), int(b[0]>>4)
		if 1+ln+on > len(b) || ln == 0 || ln > 8 || on > 8 {
			break
		}
		var length int64
		for i := ln - 1; i >= 0; i-- {
			length = length<<8 | int64(b[1+i])
		}
		if on > 0 {
			var delta int64
			if b[ln+on]&0x80 != 0 {
				delta = -1
			}
			for i := on - 1; i >= 0; i-- {
				delta = delta<<8 | int64(b[1+ln+i])
			}
			lcn += delta
			runs = append(runs, run{vcn, lcn, length})
		}
		// A run without a place is a hole: nothing of the table is there.
		vcn += length
		b = b[1+ln+on:]
	}
	return runs
}

// readAt reads a part of the table, which may lie in several places.
func (t *table) readAt(buf []byte, off int64) error {
	for len(buf) > 0 {
		vcn := off / t.cluster
		i := sort.Search(len(t.runs), func(k int) bool { return t.runs[k].vcn+t.runs[k].length > vcn })
		if i == len(t.runs) || t.runs[i].vcn > vcn {
			return io.ErrUnexpectedEOF
		}
		r := t.runs[i]
		within := off - r.vcn*t.cluster
		n := min(int64(len(buf)), r.length*t.cluster-within)
		if _, err := t.r.ReadAt(buf[:n], r.lcn*t.cluster+within); err != nil {
			return err
		}
		buf, off = buf[n:], off+n
	}
	return nil
}

// fixup puts back the last two bytes of each sector of a record, which
// the file system writes over with a count to tell a record half written,
// and reports whether the record is whole.
func fixup(rec []byte) bool {
	off, count := int(le.Uint16(rec[4:])), int(le.Uint16(rec[6:]))
	if count < 1 || off+2*count > len(rec) {
		return false
	}
	usn := le.Uint16(rec[off:])
	for i := 1; i < count; i++ {
		end := i * 512
		if end > len(rec) {
			break
		}
		if le.Uint16(rec[end-2:]) != usn {
			return false
		}
		copy(rec[end-2:end], rec[off+2*i:off+2*i+2])
	}
	return true
}

// eachAttr calls fn with each attribute of a record, until fn returns
// false.
func eachAttr(rec []byte, fn func(typ uint32, attr []byte) bool) {
	used := min(int(le.Uint32(rec[0x18:])), len(rec))
	for off := int(le.Uint16(rec[0x14:])); off+16 <= used; {
		typ := le.Uint32(rec[off:])
		if typ == attrEnd {
			return
		}
		length := int(le.Uint32(rec[off+4:]))
		if length < 16 || off+length > used {
			return
		}
		if !fn(typ, rec[off:off+length]) {
			return
		}
		off += length
	}
}

// tableScan is what one goroutine keeps of the records it reads.
type tableScan struct {
	scan *Scan
	// u16 is where a record's names are read into, for the time the
	// record is looked at.
	u16 []uint16
	// lists are the scan's entries of files that keep attributes in
	// other records: their sizes may be there.
	lists []int
	// metas are those files' flags and times, links the names other
	// records hold for them, and sizes the sizes found there.
	metas map[uint64]Entry
	links []link
	sizes map[uint64]uint64
	root  uint64
}

type link struct {
	id, parent uint64
	name       []uint16
}

// record reads one record of the table into the scan.
func (ts *tableScan) record(rec []byte, index uint64) {
	if string(rec[:4]) != "FILE" || !fixup(rec) {
		return
	}
	header := le.Uint16(rec[0x16:])
	if header&1 == 0 {
		// A record no file uses.
		return
	}
	base := le.Uint64(rec[0x20:])
	extension := base&recMask != 0
	id := uint64(le.Uint16(rec[0x10:]))<<48 | index
	if extension {
		id = base
	} else if index < 24 {
		// The file system's own files, of which only the root is one a
		// user has.
		if index == 5 {
			ts.root = id
		}
		return
	}
	e := Entry{ID: id}
	var attrs, tag uint32
	var names [8]link
	nameSpaces := [8]byte{}
	n := 0
	ts.u16 = ts.u16[:0]
	hasList, hasData, hasSize := false, false, false
	var fnSize uint64
	eachAttr(rec, func(typ uint32, a []byte) bool {
		resident := a[8] == 0
		var val []byte
		if resident && len(a) >= 0x18 {
			off, size := int(le.Uint16(a[0x14:])), int(le.Uint32(a[0x10:]))
			if off+size <= len(a) {
				val = a[off : off+size]
			}
		}
		switch typ {
		case attrStandardInformation:
			if len(val) >= 0x24 {
				e.Created = unixSeconds(int64(le.Uint64(val[0:])))
				e.Modified = unixSeconds(int64(le.Uint64(val[8:])))
				e.Accessed = unixSeconds(int64(le.Uint64(val[0x18:])))
				attrs = le.Uint32(val[0x20:])
			}
		case attrAttributeList:
			hasList = true
		case attrFileName:
			if len(val) < 0x42 {
				break
			}
			length := int(val[0x40])
			if 0x42+2*length > len(val) || length == 0 {
				break
			}
			space := val[0x41]
			if n < len(names) {
				// Room for a record's names is there from the start: what
				// was read stays where it is.
				if ts.u16 == nil {
					ts.u16 = make([]uint16, 0, 8*256)
				}
				from := len(ts.u16)
				for i := range length {
					ts.u16 = append(ts.u16, le.Uint16(val[0x42+2*i:]))
				}
				name := ts.u16[from:]
				names[n] = link{id, le.Uint64(val[0:]), name}
				nameSpaces[n] = space
				n++
			}
			fnSize = le.Uint64(val[0x30:])
		case attrData:
			if a[9] != 0 {
				// A stream with a name of its own, not the file's content.
				break
			}
			hasData = true
			switch {
			case resident:
				e.Size, hasSize = uint64(le.Uint32(a[0x10:])), true
			case len(a) >= 0x38 && le.Uint64(a[0x10:]) == 0:
				e.Size, hasSize = le.Uint64(a[0x30:]), true
			}
		case attrReparsePoint:
			if len(val) >= 4 {
				tag = le.Uint32(val)
			}
		}
		return true
	})
	if extension {
		// What another record keeps for a file: more of its names, or
		// its size.
		for i := range n {
			if nameSpaces[i] != 2 {
				kept := names[i]
				kept.name = append([]uint16(nil), kept.name...)
				ts.links = append(ts.links, kept)
			}
		}
		if hasSize {
			if ts.sizes == nil {
				ts.sizes = map[uint64]uint64{}
			}
			ts.sizes[id] = e.Size
		}
		return
	}
	// attrs lacks the folder's bit, which the record's header has.
	if header&2 != 0 {
		attrs |= 0x10
	}
	e.Flags = flagsOf(attrs, tag)
	if e.Flags&FlagDir != 0 {
		e.Size = 0
	} else if !hasData {
		e.Size = fnSize
	}
	if hasList {
		if ts.metas == nil {
			ts.metas = map[uint64]Entry{}
		}
		ts.metas[id] = e
	}
	// Each name the file has is an entry: a file with several is in
	// several folders, or in one under several names. The short name of
	// the days of 8.3 counts only when it is the only one.
	onlyDOS := true
	for i := range n {
		if nameSpaces[i] != 2 {
			onlyDOS = false
		}
	}
	for i := range n {
		if nameSpaces[i] == 2 && !onlyDOS {
			continue
		}
		e.Parent = names[i].parent
		if hasList && !hasSize && e.Flags&FlagDir == 0 {
			ts.lists = append(ts.lists, ts.scan.n)
		}
		ts.scan.Add(e, names[i].name)
	}
}

// ScanTable reads the master file table of an NTFS volume through r, and
// returns the files it lists with what the file system calls the root.
// progress, when set, counts the records read, and sc is where what is
// found is kept, nil for the program's own memory.
func ScanTable(ctx context.Context, r io.ReaderAt, progress *atomic.Int64, sc *Scratch) ([]*Scan, uint64, error) {
	t, err := openTable(r)
	if err != nil {
		return nil, 0, err
	}
	// A stretch of records a goroutine: read, then looked at where it
	// was read.
	const perChunk = 1024
	chunk := int64(perChunk * t.record)
	chunks := (t.valid + chunk - 1) / chunk
	workers := int(min(int64(min(max(runtime.GOMAXPROCS(0), 2), 12)), chunks))
	parts := make([]*tableScan, workers)
	var next atomic.Int64
	var failed atomic.Pointer[error]
	var wg sync.WaitGroup
	for wi := range workers {
		ts := &tableScan{scan: NewScan(sc)}
		parts[wi] = ts
		wg.Go(func() {
			buf := make([]byte, chunk)
			for {
				i := next.Add(1) - 1
				if i >= chunks || ctx.Err() != nil || failed.Load() != nil {
					return
				}
				off := i * chunk
				b := buf[:min(chunk, t.valid-off)]
				if err := t.readAt(b, off); err != nil {
					failed.Store(&err)
					return
				}
				for at := 0; at+t.record <= len(b); at += t.record {
					ts.record(b[at:at+t.record], uint64(off+int64(at))/uint64(t.record))
				}
				if progress != nil {
					progress.Add(int64(len(b) / t.record))
				}
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if perr := failed.Load(); perr != nil {
		return nil, 0, *perr
	}
	// What records keep for files of other records: the names, with the
	// files' flags and times, and the sizes.
	metas, sizes := map[uint64]Entry{}, map[uint64]uint64{}
	var root uint64
	for _, ts := range parts {
		for id, e := range ts.metas {
			metas[id] = e
		}
		for id, size := range ts.sizes {
			sizes[id] = size
		}
		if ts.root != 0 {
			root = ts.root
		}
	}
	if root == 0 {
		return nil, 0, errors.New("index: the file table has no root")
	}
	scans := make([]*Scan, 0, workers)
	for _, ts := range parts {
		for _, i := range ts.lists {
			if size, ok := sizes[ts.scan.rec(i).id]; ok {
				ts.scan.rec(i).size = size
			}
		}
		for _, l := range ts.links {
			if e, ok := metas[l.id]; ok {
				e.Parent = l.parent
				if size, ok := sizes[l.id]; ok && e.Flags&FlagDir == 0 {
					e.Size = size
				}
				ts.scan.Add(e, l.name)
			}
		}
		scans = append(scans, ts.scan)
	}
	return scans, root, nil
}
