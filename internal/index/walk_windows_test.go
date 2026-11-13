package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// walked is the paths a walk of a folder finds, under the folder, sorted.
func walked(t *testing.T, root string, ids bool) []string {
	t.Helper()
	dir := t.TempDir()
	sc := NewScratch(dir)
	defer sc.Close()
	scans, rootID, err := Walk(context.Background(), root, WalkOptions{IDs: ids, Scratch: sc})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range scans {
		n += s.Len()
	}
	ix := Build(root+`\`, rootID, scans, ids)
	if ix.Len() != n+1 {
		t.Errorf("%d files met, %d rows: a folder was read twice", n, ix.Len()-1)
	}
	var out []string
	for r := 1; r < ix.Len(); r++ {
		p := strings.TrimPrefix(string(ix.appendPath(nil, Row(r))), root+`\`)
		if ix.flags[r]&FlagLink != 0 {
			p += " (link)"
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// A walk reads each folder once, whether it opens folders by what the
// file system calls them or by their paths, and does not go through a
// link to another folder.
func TestWalk(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{`real\a.txt`, `real\deep\b.txt`, `other\c.txt`, `top.txt`} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644)
	}
	wanted := []string{`other`, `other\c.txt`, `real`, `real\a.txt`, `real\deep`, `real\deep\b.txt`, `top.txt`}
	// A junction, which needs no rights to make.
	if err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "link"), filepath.Join(root, "real")).Run(); err == nil {
		wanted = append(wanted, `link (link)`)
		sort.Strings(wanted)
	} else {
		t.Log("no junction:", err)
	}
	for _, ids := range []bool{true, false} {
		if got := walked(t, root, ids); !slices.Equal(got, wanted) {
			t.Errorf("with ids %v the walk finds\n%s\nwant\n%s", ids, strings.Join(got, "\n"), strings.Join(wanted, "\n"))
		}
	}
	// A folder that is itself a link is read: it was asked for.
	if _, err := os.Stat(filepath.Join(root, "link")); err == nil {
		got := walked(t, filepath.Join(root, "link"), true)
		if !slices.Equal(got, []string{`a.txt`, `deep`, `deep\b.txt`}) {
			t.Errorf("a walk of the link finds %v", got)
		}
	}

	// Two folders whose names differ in case only, where the system lets
	// them be: each is read as itself, by what it is called.
	twins := filepath.Join(root, "twins")
	os.Mkdir(twins, 0o755)
	if exec.Command("fsutil", "file", "setCaseSensitiveInfo", twins, "enable").Run() != nil {
		t.Log("no folders that tell case apart here")
		return
	}
	for _, f := range []string{`Docs\upper.txt`, `docs\lower.txt`} {
		os.MkdirAll(filepath.Join(twins, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(twins, f), nil, 0o644)
	}
	got := walked(t, twins, true)
	if !slices.Equal(got, []string{`Docs`, `Docs\upper.txt`, `docs`, `docs\lower.txt`}) {
		t.Errorf("the twins are read as %v", got)
	}
}
