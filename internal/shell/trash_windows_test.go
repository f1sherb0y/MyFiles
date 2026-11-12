package shell

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"
)

// binFor makes a Recycle Bin of a test in a folder, and returns what
// puts something in it as the system does: its record, and the thing.
func binFor(t *testing.T) (dir string, put func(id, original string, folder bool, version int) string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), `$Recycle.Bin`, "S-1-5-21-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, func(id, original string, folder bool, version int) string {
		ext := filepath.Ext(original)
		kept := filepath.Join(dir, "$R"+id+ext)
		if folder {
			if err := os.MkdirAll(filepath.Join(kept, "inside"), 0o755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(kept, []byte("content of "+original), 0o644); err != nil {
			t.Fatal(err)
		}
		le := binary.LittleEndian
		rec := le.AppendUint64(nil, uint64(version))
		rec = le.AppendUint64(rec, 1234)
		rec = le.AppendUint64(rec, uint64(time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC).UnixNano()/100+116444736000000000))
		units := append(utf16.Encode([]rune(original)), 0)
		if version == 2 {
			rec = le.AppendUint32(rec, uint32(len(units)))
		}
		for _, u := range units {
			rec = le.AppendUint16(rec, u)
		}
		for version == 1 && len(rec) < 24+520 {
			rec = append(rec, 0)
		}
		if err := os.WriteFile(filepath.Join(dir, "$I"+id+ext), rec, 0o644); err != nil {
			t.Fatal(err)
		}
		return kept
	}
}

// TestTrash checks reading, restoring and deleting what a Recycle Bin
// holds, in one made for the test: the user's is only listed.
func TestTrash(t *testing.T) {
	home := t.TempDir()
	dir, put := binFor(t)
	a := put("AAAAAA", filepath.Join(home, "docs", "报告.txt"), false, 2)
	b := put("BBBBBB", filepath.Join(home, "old one.txt"), false, 1)
	c := put("CCCCCC", filepath.Join(home, "project"), true, 2)
	// A record without what it is of, and a file that is no record.
	if err := os.WriteFile(filepath.Join(dir, "$IZZZZZZ.txt"), []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "desktop.ini"), []byte("x"), 0o644)

	items, err := trashIn([]string{dir})
	if err != nil || len(items) != 3 {
		t.Fatalf("listed %d items, %v: %+v", len(items), err, items)
	}
	byPath := map[string]TrashItem{}
	for _, it := range items {
		byPath[it.Path] = it
	}
	if it := byPath[a]; it.Name != "报告.txt" || it.From != filepath.Join(home, "docs") || it.Size != 1234 || it.Dir ||
		it.Deleted != time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC).UnixNano() {
		t.Errorf("the first item reads %+v", it)
	}
	if it := byPath[b]; it.Name != "old one.txt" || it.From != home {
		t.Errorf("the record of version 1 reads %+v", it)
	}
	if it := byPath[c]; !it.Dir || it.Name != "project" {
		t.Errorf("the folder reads %+v", it)
	}

	// Restored, a file is where it was, in a folder made again for it.
	restored, err := restoreTrash([]string{a})
	want := filepath.Join(home, "docs", "报告.txt")
	if err != nil || len(restored) != 1 || restored[0] != want {
		t.Fatalf("restored to %v, %v", restored, err)
	}
	if data, err := os.ReadFile(want); err != nil || string(data) != "content of "+want {
		t.Errorf("the file restored holds %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "$IAAAAAA.txt")); err == nil {
		t.Error("the record of a file restored is still there")
	}
	// Where something took its place, it goes beside, and replaces nothing.
	if err := os.WriteFile(filepath.Join(home, "old one.txt"), []byte("newer"), 0o644); err != nil {
		t.Fatal(err)
	}
	restored, err = restoreTrash([]string{b})
	if err != nil || len(restored) != 1 || restored[0] != filepath.Join(home, "old one (2).txt") {
		t.Fatalf("restored beside as %v, %v", restored, err)
	}
	if data, _ := os.ReadFile(filepath.Join(home, "old one.txt")); string(data) != "newer" {
		t.Error("restoring replaced the file that took the place")
	}
	// Deleted for good, a folder is gone with what it held.
	if err := deleteTrash([]string{c}); err != nil {
		t.Fatal(err)
	}
	if left, _ := trashIn([]string{dir}); len(left) != 0 {
		t.Errorf("after restoring and deleting, the bin holds %+v", left)
	}
	// A path that is not of the Recycle Bin is neither restored nor deleted.
	outside := filepath.Join(home, "$Rkeep.txt")
	_ = os.WriteFile(outside, []byte("x"), 0o644)
	if _, err := restoreTrash([]string{outside}); err == nil {
		t.Error("a file outside the Recycle Bin was restored")
	}
	if err := deleteTrash([]string{outside}); err == nil {
		t.Error("a file outside the Recycle Bin was deleted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("a file outside the Recycle Bin is gone")
	}

	// The user's own, read alone.
	start := time.Now()
	real, err := Trash()
	t.Logf("the Recycle Bin holds %d items (%v), read in %v", len(real), err, time.Since(start).Round(time.Millisecond))
	for i, it := range real {
		if it.Name == "" || it.From == "" || it.Path == "" {
			t.Errorf("an item of the Recycle Bin reads %+v", it)
		}
		if i < 3 {
			t.Logf("  %s, from %s, %s", it.Name, it.From, time.Unix(0, it.Deleted).Format("2006-01-02 15:04"))
		}
	}
}
