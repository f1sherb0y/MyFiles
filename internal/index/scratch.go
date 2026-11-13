package index

import (
	"sync"
	"unsafe"
)

// Scratch is memory to build an index in that is not the program's own:
// it is a temporary file, mapped, which the system keeps in memory while
// there is room and writes out when there is not. Reading a drive of
// millions of files takes several hundred megabytes of it, of which the
// program itself holds next to nothing.
//
// A nil Scratch gives ordinary memory, as tests and small folders use.
type Scratch struct {
	mu  sync.Mutex
	dir string
	// cur is what is left of the extent last mapped, and full the extents
	// before it.
	cur  []byte
	full [][]byte
	last []byte
	file scratchFile
	// failed is set once the file could not be made or grown: ordinary
	// memory from then on.
	failed bool
	// near is set when what was written together is read together, as
	// the files of a folder a walk found: memory let go of while reading
	// is then not wanted back at once.
	near bool
	// scanned counts the extents full when the reading was done: what
	// the making of an index needs beside comes after them, and is wanted
	// throughout.
	scanned int
}

// scratchExtent is how much the file grows by at a time.
const scratchExtent = 8 << 20

// NewScratch is a scratch whose file is made in dir, when first asked
// for memory. Close lets go of it all.
func NewScratch(dir string) *Scratch {
	if dir == "" {
		return nil
	}
	return &Scratch{dir: dir}
}

// Bytes is n bytes of zeroes, starting at a multiple of 8.
func (s *Scratch) Bytes(n int) []byte {
	if n == 0 {
		return nil
	}
	if s == nil {
		return make([]byte, n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n = (n + 7) &^ 7
	if len(s.cur) < n {
		if s.failed {
			return make([]byte, n)
		}
		// The first as large as what is asked: a scratch may be for one
		// small thing.
		size := (n + 0xFFFF) &^ 0xFFFF
		if s.last != nil {
			size = max(scratchExtent, size)
		}
		ext, err := s.file.grow(s.dir, size)
		if err != nil {
			s.failed = true
			return make([]byte, n)
		}
		// What was written to the extent before is the file's to keep
		// from here: the system writes it out as it sees fit, and brings
		// back what is touched again.
		// A piece given out of an extent is written on for a while after
		// the extent is full, so those before are let go of again.
		if s.last != nil {
			s.full = append(s.full, s.last)
		}
		for _, old := range s.full {
			rest(old)
		}
		s.cur, s.last = ext, ext
	}
	b := s.cur[:n:n]
	s.cur = s.cur[n:]
	return b
}

// read notes that what was found is all in the scratch: what is asked of
// it from here on is what making the index of it takes.
func (s *Scratch) read() {
	if s != nil {
		s.mu.Lock()
		s.scanned = len(s.full)
		s.mu.Unlock()
	}
}

// settle lets the system have the memory back that was read so far of
// what was found, where reading goes on elsewhere.
func (s *Scratch) settle() {
	if s == nil || !s.near {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.full[:s.scanned] {
		rest(old)
	}
}

// Near tells the scratch that what is written to it together is read
// together.
func (s *Scratch) Near() {
	if s != nil {
		s.near = true
	}
}

// Close lets go of the memory, and of the file: nothing got from the
// scratch may be used after.
func (s *Scratch) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur, s.last, s.full = nil, nil, nil
	s.file.close()
}

// scratchOf is n values of scratch memory, zeroed.
func scratchOf[T any](s *Scratch, n int) []T {
	if n == 0 {
		return nil
	}
	var zero T
	b := s.Bytes(n * int(unsafe.Sizeof(zero)))
	return unsafe.Slice((*T)(unsafe.Pointer(unsafe.SliceData(b))), n)
}

// idTable finds a number by an identifier, in scratch memory: the folders
// of a drive by what the file system calls them.
type idTable struct {
	keys []uint64
	// vals are the numbers, each one more than it is: 0 is an empty place.
	vals  []uint32
	shift uint
}

func newIDTable(s *Scratch, n int) *idTable {
	bits := uint(4)
	for 1<<bits < 2*n+2 {
		bits++
	}
	return &idTable{keys: scratchOf[uint64](s, 1<<bits), vals: scratchOf[uint32](s, 1<<bits), shift: 64 - bits}
}

func (t *idTable) slot(id uint64) uint64 { return id * 0x9E3779B97F4A7C15 >> t.shift }

// get is the number put for an identifier.
func (t *idTable) get(id uint64) (uint32, bool) {
	mask := uint64(len(t.keys) - 1)
	for i := t.slot(id); ; i = (i + 1) & mask {
		switch v := t.vals[i]; {
		case v == 0:
			return 0, false
		case t.keys[i] == id:
			return v - 1, true
		}
	}
}

// put notes a number for an identifier, unless one is noted, which it
// reports.
func (t *idTable) put(id uint64, n uint32) (dup bool) {
	mask := uint64(len(t.keys) - 1)
	for i := t.slot(id); ; i = (i + 1) & mask {
		switch {
		case t.vals[i] == 0:
			t.keys[i], t.vals[i] = id, n+1
			return false
		case t.keys[i] == id:
			return true
		}
	}
}
