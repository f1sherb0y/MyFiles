package index

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unsafe"
)

// An index's file is its columns as they are in memory, after a page that
// says where they are:
//
//	0    "MYFIDX" and the version of the format
//	8    rows, bytes of names, entries of the table of records
//	32   the length of what follows, then the root and what is known of
//	     where the index came from, as JSON
//	3072 where each column starts in the file, in the order of sections
//	4096 the columns, each starting at a multiple of 8
//
// The file is the index: it is mapped, not read, so that the system
// brings in the parts a search touches and lets go of them when memory is
// short, and the program itself holds none of it. A drive's index is
// built in its file, through the same mapping. The file is made under
// another name and renamed once whole: one that is there is complete.
const (
	fileMagic   = "MYFIDX\x00\x05"
	fileHeader  = 4096
	fileOffsets = 3072
	sections    = 12
)

type fileInfo struct {
	Root string `json:"root"`
	Meta Meta   `json:"meta"`
}

// bytesOf is the memory of a column.
func bytesOf[T any](s []T) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(s))), len(s)*int(unsafe.Sizeof(s[0])))
}

// columnOf is a column in mapped memory.
func columnOf[T any](b []byte, n int) []T {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*T)(unsafe.Pointer(unsafe.SliceData(b))), n)
}

func align8(n int64) int64 { return (n + 7) &^ 7 }

// sectionSizes are the bytes each column takes for as many rows, bytes
// of names and entries of the table of records, in the order they are in
// the file.
func sectionSizes(rows, names, recs int) [sections]int {
	return [sections]int{
		4 * (rows + 1), 8 * rows, 4 * rows, 4 * rows, 2 * rows,
		8 * rows, 4 * rows, 4 * rows, 4 * rows, 8 * rows,
		4 * recs, names,
	}
}

// carve makes the columns of the memory of each, as sectionSizes sizes
// them.
func (ix *Index) carve(mem [][]byte, rows, names, recs int) {
	ix.n = rows
	ix.nameOff = columnOf[uint32](mem[0], rows+1)
	ix.mask = columnOf[uint64](mem[1], rows)
	ix.parent = columnOf[uint32](mem[2], rows)
	ix.end = columnOf[uint32](mem[3], rows)
	ix.flags = columnOf[uint16](mem[4], rows)
	ix.size = columnOf[uint64](mem[5], rows)
	ix.mtime = columnOf[uint32](mem[6], rows)
	ix.ctime = columnOf[uint32](mem[7], rows)
	ix.atime = columnOf[uint32](mem[8], rows)
	ix.id = columnOf[uint64](mem[9], rows)
	ix.byRec = columnOf[uint32](mem[10], recs)
	ix.names = mem[11][:names:names]
}

// header is the first page of an index's file, given where its columns
// start.
func (ix *Index) header(offsets [sections]int64) ([]byte, error) {
	info, err := json.Marshal(fileInfo{ix.Root, ix.Meta})
	if err != nil {
		return nil, err
	}
	if len(info) > fileOffsets-36 {
		return nil, errors.New("index: what is known of the index does not fit its file's first page")
	}
	head := make([]byte, fileHeader)
	copy(head, fileMagic)
	binary.LittleEndian.PutUint64(head[8:], uint64(ix.n))
	binary.LittleEndian.PutUint64(head[16:], uint64(len(ix.names)))
	binary.LittleEndian.PutUint64(head[24:], uint64(len(ix.byRec)))
	binary.LittleEndian.PutUint32(head[32:], uint32(len(info)))
	copy(head[36:], info)
	for i, off := range offsets {
		binary.LittleEndian.PutUint64(head[fileOffsets+8*i:], uint64(off))
	}
	return head, nil
}

// Save writes an index that is in memory to a file, in place of the one
// there.
func (ix *Index) Save(path string) error {
	cols := [sections][]byte{
		bytesOf(ix.nameOff), bytesOf(ix.mask), bytesOf(ix.parent), bytesOf(ix.end), bytesOf(ix.flags),
		bytesOf(ix.size), bytesOf(ix.mtime), bytesOf(ix.ctime), bytesOf(ix.atime), bytesOf(ix.id),
		bytesOf(ix.byRec), ix.names,
	}
	var offsets [sections]int64
	at := int64(fileHeader)
	for i, col := range cols {
		offsets[i] = at
		at = align8(at + int64(len(col)))
	}
	head, err := ix.header(offsets)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	w.Write(head)
	var pad [8]byte
	for _, col := range cols {
		w.Write(col)
		w.Write(pad[:align8(int64(len(col)))-int64(len(col))])
	}
	err = w.Flush()
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// BuildFile makes an index of what scans found under a root, in a file,
// and maps it: the index is never in the program's own memory. What
// building takes beside is the scratch's. See Build for the rest.
func BuildFile(path, root string, rootID uint64, scans []*Scan, tracked bool, meta Meta, sc *Scratch) (*Index, error) {
	tmp := path + ".tmp"
	var (
		data    []byte
		done    func(keep bool) error
		offsets [sections]int64
		failed  error
	)
	ix := build(root, rootID, scans, tracked, sc, func(rows, names, recs int) ([][]byte, error) {
		sizes := sectionSizes(rows, names, recs)
		at := int64(fileHeader)
		for i, size := range sizes {
			offsets[i] = at
			at = align8(at + int64(size))
		}
		data, done, failed = createMapped(tmp, at)
		if failed != nil {
			return nil, failed
		}
		mem := make([][]byte, sections)
		for i, size := range sizes {
			mem[i] = data[offsets[i] : offsets[i]+int64(size) : offsets[i]+int64(size)]
		}
		return mem, nil
	})
	if ix == nil {
		return nil, failed
	}
	meta.Files, meta.Dirs = ix.Meta.Files, ix.Meta.Dirs
	ix.Meta = meta
	head, err := ix.header(offsets)
	if err == nil {
		copy(data, head)
	}
	// The columns are the mapping's, which goes now: the file is mapped
	// again to read once it has its name.
	ix = nil
	if derr := done(err == nil); err == nil {
		err = derr
	}
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	return Open(path)
}

// Open maps an index from its file.
func Open(path string) (*Index, error) {
	data, release, err := mapFile(path)
	if err != nil {
		return nil, err
	}
	ix, err := decode(data)
	if err != nil {
		release()
		return nil, fmt.Errorf("index: %s: %w", filepath.Base(path), err)
	}
	ix.mapped, ix.release = data, release
	ix.dir = filepath.Dir(path)
	ix.side = NewScratch(ix.dir)
	ix.marks = ix.side.Bytes(ix.n)[:ix.n]
	return ix, nil
}

func decode(data []byte) (*Index, error) {
	if len(data) < fileHeader || string(data[:8]) != fileMagic {
		return nil, errors.New("not an index of this version")
	}
	n := int(binary.LittleEndian.Uint64(data[8:]))
	names := int(binary.LittleEndian.Uint64(data[16:]))
	recs := int(binary.LittleEndian.Uint64(data[24:]))
	infoLen := int(binary.LittleEndian.Uint32(data[32:]))
	if n < 1 || n > 1<<31-2 || names < 0 || recs < 0 || infoLen > fileOffsets-36 {
		return nil, errors.New("counts out of range")
	}
	var info fileInfo
	if err := json.Unmarshal(data[36:36+infoLen], &info); err != nil {
		return nil, err
	}
	sizes := sectionSizes(n, names, recs)
	mem := make([][]byte, sections)
	for i, size := range sizes {
		off := int64(binary.LittleEndian.Uint64(data[fileOffsets+8*i:]))
		if off < fileHeader || off%8 != 0 || off+int64(size) > int64(len(data)) {
			return nil, errors.New("the file is shorter than its rows")
		}
		mem[i] = data[off : off+int64(size) : off+int64(size)]
	}
	ix := &Index{Root: info.Root, Meta: info.Meta}
	ix.carve(mem, n, names, recs)
	// What a search trusts without looking: the last name ends where the
	// names do, and no row is in a folder after it.
	if ix.nameOff[n] != uint32(names) || ix.end[0] != uint32(n) {
		return nil, errors.New("the columns do not fit each other")
	}
	return ix, nil
}

// Store keeps the files of indexes in a folder, one a drive: named by a
// key and a count, the highest count the newest. A file that is mapped
// cannot be written over, so each index is a new file, and the older ones
// go once nothing maps them.
type Store struct {
	Dir string
}

func (s Store) files(key string) []string {
	matches, _ := filepath.Glob(filepath.Join(s.Dir, key+".*.idx"))
	sort.Slice(matches, func(i, j int) bool { return genOf(matches[i]) > genOf(matches[j]) })
	return matches
}

func genOf(path string) int {
	parts := strings.Split(filepath.Base(path), ".")
	if len(parts) < 3 {
		return 0
	}
	n, _ := strconv.Atoi(parts[len(parts)-2])
	return n
}

// Load maps the newest index kept under a key that reads.
func (s Store) Load(key string) (*Index, error) {
	var last error = os.ErrNotExist
	for _, path := range s.files(key) {
		ix, err := Open(path)
		if err == nil {
			return ix, nil
		}
		last = err
	}
	return nil, last
}

// next is the file the next index kept under a key goes in, and those
// there now.
func (s Store) next(key string) (path string, old []string, err error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", nil, err
	}
	old = s.files(key)
	gen := 1
	if len(old) > 0 {
		gen = genOf(old[0]) + 1
	}
	return filepath.Join(s.Dir, fmt.Sprintf("%s.%d.idx", key, gen)), old, nil
}

// Save keeps an index that is in memory under a key, and removes the
// older ones that are no longer in use.
func (s Store) Save(key string, ix *Index) error {
	path, old, err := s.next(key)
	if err != nil {
		return err
	}
	if err := ix.Save(path); err != nil {
		return err
	}
	for _, path := range old {
		// One still mapped stays until a later one is kept.
		os.Remove(path)
	}
	return nil
}

// Build makes the index of what scans found in a file kept under a key,
// maps it, and removes the older ones that are no longer in use.
func (s Store) Build(key, root string, rootID uint64, scans []*Scan, tracked bool, meta Meta, sc *Scratch) (*Index, error) {
	path, old, err := s.next(key)
	if err != nil {
		return nil, err
	}
	ix, err := BuildFile(path, root, rootID, scans, tracked, meta, sc)
	if err != nil {
		return nil, err
	}
	for _, path := range old {
		os.Remove(path)
	}
	return ix, nil
}

// Remove forgets the indexes kept under a key.
func (s Store) Remove(key string) {
	for _, path := range s.files(key) {
		os.Remove(path)
	}
}

// Sweep removes what a run that ended badly left in the folder: files
// half written, and the scratch files of programs no longer running.
func (s Store) Sweep() {
	for _, pattern := range []string{"*.tmp", "tmp-*.idx"} {
		matches, _ := filepath.Glob(filepath.Join(s.Dir, pattern))
		for _, path := range matches {
			// One in use by a program that runs cannot be removed.
			os.Remove(path)
		}
	}
}
