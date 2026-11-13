package index

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// An index built in its file, with a scratch for what building takes, is
// the index built in memory: the same rows, found the same way, and there
// again when the file is opened later.
func TestBuildFile(t *testing.T) {
	dir := t.TempDir()
	sc := NewScratch(dir)
	scan := NewScan(sc)
	heap := NewScan(nil)
	// Enough files for several pieces of a scan, in folders several deep.
	const folders, each = 300, 150
	id := uint64(100)
	for d := range folders {
		id++
		parent := uint64(treeRoot)
		if d > 0 {
			// Each folder in one before it.
			parent = 101 + uint64(d/2)*(each+1)
		}
		dirID := id
		e := Entry{ID: dirID, Parent: parent, Flags: FlagDir}
		name := fmt.Sprintf("folder-%03d", d)
		scan.AddString(e, name)
		heap.AddString(e, name)
		for f := range each {
			id++
			e := Entry{ID: id, Parent: dirID, Size: uint64(f), Modified: uint32(id)}
			name := fmt.Sprintf("file-%03d-%03d.txt", d, f)
			if f == 7 {
				name = fmt.Sprintf("报告-%03d.docx", d)
			}
			scan.AddString(e, name)
			heap.AddString(e, name)
		}
	}
	if scan.Len() != folders*(each+1) || len(scan.recs) < 2 {
		t.Fatalf("the scan holds %d files in %d pieces", scan.Len(), len(scan.recs))
	}

	store := Store{Dir: dir}
	meta := Meta{Serial: 7, FileSystem: "NTFS", NextUSN: 42}
	ix, err := store.Build("d", `D:\`, treeRoot, []*Scan{scan}, true, meta, sc)
	if err != nil {
		t.Fatal(err)
	}
	sc.Close()
	defer func() { ix.Close() }()
	mem := Build(`D:\`, treeRoot, []*Scan{heap}, true)
	if ix.Len() != mem.Len() || ix.Len() != folders*(each+1)+1 {
		t.Fatalf("%d rows in the file, %d in memory", ix.Len(), mem.Len())
	}
	if ix.Meta.NextUSN != 42 || ix.Meta.Files != folders*each || ix.Meta.Dirs != folders {
		t.Fatalf("the file knows %+v", ix.Meta)
	}
	for r := range Row(ix.Len()) {
		if ix.Name(r) != mem.Name(r) || ix.parent[r] != mem.parent[r] || ix.end[r] != mem.end[r] ||
			ix.id[r] != mem.id[r] || ix.size[r] != mem.size[r] || ix.mask[r] != mem.mask[r] {
			t.Fatalf("row %d differs: %q and %q", r, ix.Name(r), mem.Name(r))
		}
	}
	if !slices.Equal(ix.byRec, mem.byRec) {
		t.Fatal("the tables of records differ")
	}
	fv, mv := NewView(ix, nil), NewView(mem, nil)
	for _, q := range []string{"file-123", "baogao", "folder-2", "报告-017", "txt 042"} {
		got, wanted := find(t, fv, q, ""), find(t, mv, q, "")
		if len(got) == 0 || !slices.Equal(got, wanted) {
			t.Fatalf("%q finds %d in the file, %d in memory", q, len(got), len(wanted))
		}
	}
	// Nothing is left of the scratch, nor of the file half written: only
	// the file the open index keeps its marks in.
	left, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(left) != 1 {
		t.Fatalf("left in the folder: %v", left)
	}

	// What changed since goes into the next file, built of the one mapped.
	fv.Remove(ix.id[ix.rowOf(102)])
	fv.Apply(Change{Entry: Entry{ID: 900_000, Parent: 101}, Name: "added-later.txt"}, nil)
	sc = NewScratch(dir)
	next, err := store.Build("d", `D:\`, treeRoot, []*Scan{fv.rescan(sc)}, true, ix.Meta, sc)
	sc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if next.Len() != ix.Len() {
		t.Fatalf("%d rows after one file went and one came, %d before", next.Len(), ix.Len())
	}
	nv := NewView(next, nil)
	if got := find(t, nv, "added-later", ""); len(got) != 1 {
		t.Fatalf("the file added is found %d times", len(got))
	}
	if got := find(t, nv, "file-000-000", ""); len(got) != 0 {
		t.Fatalf("the file removed is found: %v", got)
	}
	// The older file goes once nothing maps it.
	next.Close()
	ix.Close()
	again, err := store.Load("d")
	if err != nil {
		t.Fatal(err)
	}
	if again.Len() != next.n || filepath.Base(store.files("d")[0]) != "d.2.idx" {
		t.Fatalf("loaded %d rows of %v", again.Len(), store.files("d"))
	}
	store.Sweep()
	if _, err := os.Stat(store.files("d")[0]); err != nil {
		t.Fatal("a sweep took an index")
	}
	// With the last index closed, its marks' file is gone too.
	again.Close()
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Fatalf("left in the folder: %v", left)
	}
}
