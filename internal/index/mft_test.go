package index

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"

	"myfiles/internal/search"
)

// The tests make a small NTFS volume in memory, as much of one as reading
// its file table takes: the first sector, and the table, whose records
// are written the way NTFS writes them.

const (
	imgSector  = 512
	imgCluster = 4096
	imgRecord  = 1024
)

// attr makes an attribute kept in the record.
func attr(typ uint32, name string, value []byte) []byte {
	n16 := utf16.Encode([]rune(name))
	nameOff := 0x18
	valueOff := (nameOff + 2*len(n16) + 7) &^ 7
	length := (valueOff + len(value) + 7) &^ 7
	a := make([]byte, length)
	le.PutUint32(a[0:], typ)
	le.PutUint32(a[4:], uint32(length))
	a[9] = byte(len(n16))
	le.PutUint16(a[0x0A:], uint16(nameOff))
	le.PutUint32(a[0x10:], uint32(len(value)))
	le.PutUint16(a[0x14:], uint16(valueOff))
	for i, u := range n16 {
		le.PutUint16(a[nameOff+2*i:], u)
	}
	copy(a[valueOff:], value)
	return a
}

// outside makes an attribute whose content is elsewhere on the volume:
// size bytes of it, in runs of (clusters, cluster to start at).
func outside(typ uint32, size uint64, firstVCN uint64, runs ...[2]int64) []byte {
	var list []byte
	prev := int64(0)
	clusters := int64(0)
	for _, r := range runs {
		delta := r[1] - prev
		prev = r[1]
		clusters += r[0]
		list = append(list, 0x42, byte(r[0]), byte(r[0]>>8), byte(delta), byte(delta>>8), byte(delta>>16), byte(delta>>24))
	}
	list = append(list, 0)
	length := (0x40 + len(list) + 7) &^ 7
	a := make([]byte, length)
	le.PutUint32(a[0:], typ)
	le.PutUint32(a[4:], uint32(length))
	a[8] = 1
	le.PutUint64(a[0x10:], firstVCN)
	le.PutUint64(a[0x18:], firstVCN+uint64(clusters)-1)
	le.PutUint16(a[0x20:], 0x40)
	le.PutUint64(a[0x28:], uint64(clusters)*imgCluster)
	le.PutUint64(a[0x30:], size)
	le.PutUint64(a[0x38:], size)
	copy(a[0x40:], list)
	return a
}

func stdInfo(modified int64, attrs uint32) []byte {
	v := make([]byte, 0x30)
	ft := uint64(modified*10_000_000 + epoch)
	le.PutUint64(v[0:], ft-10_000_000)
	le.PutUint64(v[8:], ft)
	le.PutUint64(v[0x18:], ft+10_000_000)
	le.PutUint32(v[0x20:], attrs)
	return attr(attrStandardInformation, "", v)
}

func fileName(parent uint64, name string, space byte, size uint64) []byte {
	n16 := utf16.Encode([]rune(name))
	v := make([]byte, 0x42+2*len(n16))
	le.PutUint64(v[0:], parent)
	le.PutUint64(v[0x30:], size)
	v[0x40], v[0x41] = byte(len(n16)), space
	for i, u := range n16 {
		le.PutUint16(v[0x42+2*i:], u)
	}
	return attr(attrFileName, "", v)
}

// rec makes a record of the table: flags 1 for one in use, 3 for a
// folder; base names the record this one keeps more attributes for.
func rec(seq uint16, flags uint16, base uint64, attrs ...[]byte) []byte {
	r := make([]byte, imgRecord)
	copy(r, "FILE")
	le.PutUint16(r[4:], 0x30) // where the saved ends of the sectors are
	le.PutUint16(r[6:], 3)    // the count, and one for each sector
	le.PutUint16(r[0x10:], seq)
	le.PutUint16(r[0x14:], 0x38)
	le.PutUint16(r[0x16:], flags)
	le.PutUint64(r[0x20:], base)
	off := 0x38
	for _, a := range attrs {
		copy(r[off:], a)
		off += len(a)
	}
	le.PutUint32(r[off:], attrEnd)
	le.PutUint32(r[0x18:], uint32(off+8))
	le.PutUint32(r[0x1C:], imgRecord)
	// As NTFS writes a record: the last two bytes of each sector go to
	// the header, and a count takes their place.
	le.PutUint16(r[0x30:], 0x0007)
	for i := 1; i <= 2; i++ {
		end := i * imgSector
		copy(r[0x30+2*i:], r[end-2:end])
		le.PutUint16(r[end-2:], 0x0007)
	}
	return r
}

func ref(index uint64, seq uint16) uint64 { return uint64(seq)<<48 | index }

// build lays the table out on the volume in pieces, as pieces says: each
// a number of records and the cluster it starts at.
func buildVolume(records [][]byte, pieces [][2]int64) []byte {
	var runs [][2]int64
	last := int64(0)
	for _, p := range pieces {
		clusters := p[0] * imgRecord / imgCluster
		runs = append(runs, [2]int64{clusters, p[1]})
		last = max(last, p[1]+clusters)
	}
	data := make([]byte, (last+4)*imgCluster)
	copy(data[3:], "NTFS    ")
	le.PutUint16(data[0x0B:], imgSector)
	data[0x0D] = imgCluster / imgSector
	le.PutUint64(data[0x30:], uint64(pieces[0][1]))
	data[0x40] = 0xF6 // records of 2^10 bytes
	// The table's own record says where its pieces are.
	size := uint64(len(records)) * imgRecord
	records[0] = rec(1, 1, 0, stdInfo(1000, 6), fileName(ref(5, 5), "$MFT", 3, 0), outside(attrData, size, 0, runs...))
	at := 0
	for _, p := range pieces {
		for i := int64(0); i < p[0] && at < len(records); i++ {
			copy(data[p[1]*imgCluster+i*imgRecord:], records[at])
			at++
		}
	}
	return data
}

func TestScanTable(t *testing.T) {
	const dirAttr, hiddenAttr = 0x10, 0x2
	records := make([][]byte, 48)
	for i := range records {
		records[i] = make([]byte, imgRecord)
	}
	root := ref(5, 5)
	records[5] = rec(5, 3, 0, stdInfo(1000, 6), fileName(root, ".", 3, 0))
	// The system's own files are left out.
	records[9] = rec(9, 1, 0, stdInfo(1000, 6), fileName(root, "$Secure", 3, 0))
	docs := ref(24, 1)
	records[24] = rec(1, 3, 0, stdInfo(2000, 0), fileName(root, "Documents", 3, 0))
	// A small file's content is in its record; its size is the content's.
	records[25] = rec(2, 1, 0, stdInfo(3000, 0x20), fileName(docs, "notes.txt", 3, 0),
		attr(attrData, "", []byte("hello world")),
		attr(attrData, "Zone.Identifier", []byte("a stream with a name of its own")))
	// A larger one's is elsewhere, and its size told where it is.
	records[26] = rec(1, 1, 0, stdInfo(4000, 0x20), fileName(docs, "年度报告.pdf", 1, 0),
		fileName(docs, "年度~1.PDF", 2, 0), outside(attrData, 123456, 0, [2]int64{31, 500}))
	// A hidden folder, and a file in it under two names: two entries.
	git := ref(27, 3)
	records[27] = rec(3, 3, 0, stdInfo(5000, hiddenAttr), fileName(docs, ".git", 3, 0))
	records[28] = rec(1, 1, 0, stdInfo(6000, 0x20), fileName(git, "HEAD", 3, 0), fileName(docs, "head-link", 3, 0),
		attr(attrData, "", []byte("ref")))
	// A record no file uses, which the one before it was once.
	records[29] = rec(4, 0, 0, stdInfo(7000, 0x20), fileName(docs, "deleted.txt", 3, 0))
	// A file of so many attributes that some are in another record: a
	// name there, and its size.
	big := ref(30, 1)
	records[30] = rec(1, 1, 0, stdInfo(8000, 0x20), attr(attrAttributeList, "", make([]byte, 32)), fileName(docs, "big.bin", 3, 0))
	records[40] = rec(1, 1, big, fileName(root, "big-link.bin", 3, 0), outside(attrData, 9_000_000_000, 0, [2]int64{16, 600}))
	// A folder that leads nowhere: what it holds is left out.
	records[31] = rec(1, 1, 0, stdInfo(9000, 0x20), fileName(ref(47, 9), "orphan.txt", 3, 0))
	// A record half written: the ends of its sectors do not agree.
	torn := rec(1, 1, 0, stdInfo(9000, 0x20), fileName(docs, "torn.txt", 3, 0))
	le.PutUint16(torn[imgSector-2:], 0x0006)
	records[32] = torn
	// A junction.
	records[33] = rec(1, 3, 0, stdInfo(9500, 0x410), fileName(root, "Link", 3, 0),
		attr(attrReparsePoint, "", []byte{0x03, 0, 0, 0xA0, 0, 0, 0, 0}))

	// The table in two pieces, the second before the first on the volume.
	data := buildVolume(records, [][2]int64{{32, 40}, {16, 20}})
	// Read as the program reads a drive: into a scratch file, and built
	// in the index's own.
	dir := t.TempDir()
	sc := NewScratch(dir)
	scans, rootID, err := ScanTable(context.Background(), bytes.NewReader(data), nil, sc)
	if err != nil {
		t.Fatal(err)
	}
	if rootID != root {
		t.Fatalf("the root is %x, want %x", rootID, root)
	}
	ix, err := Store{Dir: dir}.Build("x", `X:\`, rootID, scans, true, Meta{}, sc)
	sc.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	v := NewView(ix, nil)
	var got []string
	for r := 1; r < ix.Len(); r++ {
		got = append(got, fmt.Sprintf("%s size=%d mtime=%d flags=%x", v.Path(Ref(r)), ix.size[r], ix.mtime[r], ix.flags[r]))
	}
	sort.Strings(got)
	want := []string{
		`X:\Documents size=0 mtime=2000 flags=1`,
		`X:\Documents\.git size=0 mtime=5000 flags=3`,
		`X:\Documents\.git\HEAD size=3 mtime=6000 flags=0`,
		`X:\Documents\big.bin size=9000000000 mtime=8000 flags=0`,
		`X:\Documents\head-link size=3 mtime=6000 flags=0`,
		`X:\Documents\notes.txt size=11 mtime=3000 flags=0`,
		`X:\Documents\年度报告.pdf size=123456 mtime=4000 flags=0`,
		`X:\Link size=0 mtime=9500 flags=11`,
		`X:\big-link.bin size=9000000000 mtime=8000 flags=0`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the table reads as\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	// The files are found by what the journal calls them.
	if r := ix.rowOf(ref(25, 2)); r == 0 || ix.Name(r) != "notes.txt" {
		t.Errorf("the file of record 25 is row %d", r)
	}
	req := &Request{Query: search.ParseQuery("ndbg", search.DefaultOptions(), ':', nil), Limit: 10}
	if res, _ := v.Search(context.Background(), req); len(res) != 1 || res[0].Path != `X:\Documents\年度报告.pdf` {
		t.Errorf("ndbg finds %+v", res)
	}

	// What is no NTFS volume is told so.
	if _, _, err := ScanTable(context.Background(), bytes.NewReader(make([]byte, 1<<16)), nil, nil); err != errNotNTFS {
		t.Errorf("a blank volume: %v", err)
	}
}

// corpus is as many files in as many folders, named as files are.
func corpus(files, dirs int) *View {
	s := NewScan(nil)
	words := strings.Fields("report summary invoice photo draft final notes budget meeting archive backup config index main readme")
	exts := strings.Fields("txt pdf docx jpg png go md json xlsx zip")
	for d := range dirs {
		s.AddString(Entry{ID: uint64(100 + d), Parent: treeRoot, Flags: FlagDir}, fmt.Sprintf("folder-%03d", d))
	}
	for f := range files {
		name := fmt.Sprintf("%s-%s-%05d.%s", words[f%len(words)], words[(f/7)%len(words)], f, exts[f%len(exts)])
		s.AddString(Entry{ID: uint64(10_000 + f), Parent: uint64(100 + f%dirs), Size: uint64(f)}, name)
	}
	return NewView(Build(`D:\`, treeRoot, []*Scan{s}, true), nil)
}

// BenchmarkSearch searches as many files as Lertaro's own measurements
// do (60,000 in 100 folders, dev-docs/search-performance.md there), and
// a hundred times as many.
func BenchmarkSearch(b *testing.B) {
	for _, n := range []int{60_000, 6_000_000} {
		v := corpus(n, 100)
		for _, c := range []struct {
			query string
			limit int
		}{{"report", 204}, {"report", 1 << 30}, {"rprt", 204}, {`folder\report`, 204}} {
			name := fmt.Sprintf("%d/%s/%d", n, c.query, c.limit)
			if c.limit > 1000 {
				name = fmt.Sprintf("%d/%s/all", n, c.query)
			}
			b.Run(name, func(b *testing.B) {
				req := &Request{Query: search.ParseQuery(c.query, search.DefaultOptions(), ':', nil), Limit: c.limit}
				var total int
				for b.Loop() {
					// As the first time: not what the search before left.
					v.gen++
					_, total = v.Search(context.Background(), req)
				}
				b.ReportMetric(float64(total), "matches")
			})
		}
	}
}
