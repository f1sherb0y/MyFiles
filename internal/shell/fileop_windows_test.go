package shell

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// within fails the test if an operation is not over soon: it would be
// waiting for an answer to a window, which no test may show. The window
// goes with the test's process.
func within(t *testing.T, op func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- op() }()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("the operation waits, probably for an answer")
		return nil
	}
}

// write makes a file, and the directories it is in.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// read is the content of a file, or "(missing)".
func read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(missing)"
	}
	return string(b)
}

// names are the names in a directory, sorted.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestCopy(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	write(t, filepath.Join(src, "a.txt"), "a")
	write(t, filepath.Join(src, "tree", "b.txt"), "b")
	write(t, filepath.Join(src, "tree", "deep", "c.txt"), "c")
	if err := os.MkdirAll(filepath.Join(src, "tree", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	err := within(t, func() error {
		return Copy(0, []string{filepath.Join(src, "a.txt"), filepath.Join(src, "tree")}, dst)
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(dst, "a.txt"):                 "a",
		filepath.Join(dst, "tree", "b.txt"):         "b",
		filepath.Join(dst, "tree", "deep", "c.txt"): "c",
		filepath.Join(src, "a.txt"):                 "a",
		filepath.Join(src, "tree", "deep", "c.txt"): "c",
	} {
		if got := read(path); got != want {
			t.Errorf("%s holds %q, want %q", path, got, want)
		}
	}
	if !exists(filepath.Join(dst, "tree", "empty")) {
		t.Error("the empty directory was not copied")
	}
}

func TestCopyBeside(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "report.txt"), "report")
	write(t, filepath.Join(dir, "folder", "inner.txt"), "inner")
	// The destination differs in case from the sources' directory, and
	// is the same one.
	err := within(t, func() error {
		return Copy(0, []string{filepath.Join(dir, "report.txt"), filepath.Join(dir, "folder")}, strings.ToUpper(dir))
	})
	if err != nil {
		t.Fatal(err)
	}
	got := names(t, dir)
	t.Logf("after copying beside: %q", got)
	if len(got) != 4 {
		t.Fatalf("%d entries, want the two and their copies", len(got))
	}
	var copies int
	for _, name := range got {
		switch {
		case name == "report.txt" || name == "folder":
		case strings.HasPrefix(name, "report") && strings.HasSuffix(name, ".txt"):
			copies++
			if c := read(filepath.Join(dir, name)); c != "report" {
				t.Errorf("%s holds %q", name, c)
			}
		case strings.HasPrefix(name, "folder"):
			copies++
			if c := read(filepath.Join(dir, name, "inner.txt")); c != "inner" {
				t.Errorf("%s\\inner.txt holds %q", name, c)
			}
		default:
			t.Errorf("unexpected %s", name)
		}
	}
	if copies != 2 {
		t.Errorf("%d copies", copies)
	}
}

func TestMove(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	write(t, filepath.Join(src, "a.txt"), "a")
	write(t, filepath.Join(src, "tree", "deep", "c.txt"), "c")
	write(t, filepath.Join(src, "stays.txt"), "stays")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	err := within(t, func() error {
		return Move(0, []string{filepath.Join(src, "a.txt"), filepath.Join(src, "tree")}, dst)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, src); !slices.Equal(got, []string{"stays.txt"}) {
		t.Errorf("left behind: %q", got)
	}
	if read(filepath.Join(dst, "a.txt")) != "a" || read(filepath.Join(dst, "tree", "deep", "c.txt")) != "c" {
		t.Errorf("arrived: %q", names(t, dst))
	}

	// Moving something to where it is does nothing, and asks nothing.
	err = within(t, func() error { return Move(0, []string{filepath.Join(src, "stays.txt")}, src) })
	if err != nil {
		t.Errorf("moving in place: %v", err)
	}
	if got := names(t, src); !slices.Equal(got, []string{"stays.txt"}) {
		t.Errorf("after moving in place: %q", got)
	}
}

func TestTransferIntoItself(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	write(t, filepath.Join(dir, "sub", "f.txt"), "f")
	for _, dest := range []string{dir, filepath.Join(dir, "sub"), strings.ToUpper(filepath.Join(dir, "sub"))} {
		for _, move := range []bool{false, true} {
			err := within(t, func() error { return transfer(0, []string{dir}, dest, move) })
			if !errors.Is(err, errIntoItself) {
				t.Errorf("into %s (move %v): %v", dest, move, err)
			}
		}
	}
	// A directory whose name another's begins with is not inside it.
	sibling := filepath.Join(root, "dir2")
	if err := os.Mkdir(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := within(t, func() error { return Copy(0, []string{dir}, sibling) }); err != nil {
		t.Errorf("into a sibling: %v", err)
	}
	if read(filepath.Join(sibling, "dir", "sub", "f.txt")) != "f" {
		t.Error("not copied into the sibling")
	}
}

func TestTransferErrors(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing.txt")
	err := within(t, func() error { return Copy(0, []string{missing}, root) })
	t.Logf("copying what is not there: %v", err)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("want an error that is fs.ErrNotExist, got %v", err)
	}
	write(t, filepath.Join(root, "a.txt"), "a")
	err = within(t, func() error {
		return Copy(0, []string{filepath.Join(root, "a.txt")}, filepath.Join(root, "nowhere", "deeper"))
	})
	t.Logf("copying to a directory that is not there: %v", err)
	if err := within(t, func() error { return Copy(0, nil, root) }); err != nil {
		t.Errorf("copying nothing: %v", err)
	}
}

func TestRename(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	write(t, a, "a")
	write(t, filepath.Join(dir, "taken.txt"), "taken")
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := within(t, func() error { return Rename(0, a, "b.txt") }); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"b.txt", "folder", "taken.txt"}) {
		t.Fatalf("after renaming: %q", got)
	}
	b := filepath.Join(dir, "b.txt")

	// A name that is taken, by a file or a directory, in any case.
	for _, name := range []string{"taken.txt", "TAKEN.TXT", "folder"} {
		err := within(t, func() error { return Rename(0, b, name) })
		if !errors.Is(err, fs.ErrExist) {
			t.Errorf("renaming to %s: %v", name, err)
		}
	}
	if read(b) != "a" || read(filepath.Join(dir, "taken.txt")) != "taken" {
		t.Error("a refused rename changed files")
	}

	// The case of a name alone.
	if err := within(t, func() error { return Rename(0, b, "B.TXT") }); err != nil {
		t.Fatalf("changing the case: %v", err)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"B.TXT", "folder", "taken.txt"}) {
		t.Errorf("after changing the case: %q", got)
	}
	// The same name is nothing to do.
	if err := within(t, func() error { return Rename(0, filepath.Join(dir, "B.TXT"), "B.TXT") }); err != nil {
		t.Errorf("renaming to the same name: %v", err)
	}
	// A directory.
	if err := within(t, func() error { return Rename(0, filepath.Join(dir, "folder"), "renamed") }); err != nil {
		t.Errorf("renaming a directory: %v", err)
	}
	if !exists(filepath.Join(dir, "renamed")) {
		t.Error("the directory was not renamed")
	}

	// What cannot be done comes back as an error, in no window.
	for _, name := range []string{"", `x\y`, "x/y", `a<b>.txt`, "what?.txt"} {
		err := within(t, func() error { return Rename(0, filepath.Join(dir, "B.TXT"), name) })
		t.Logf("renaming to %q: %v", name, err)
		if err == nil || errors.Is(err, ErrCanceled) {
			t.Errorf("renaming to %q: %v, want what went wrong", name, err)
		}
	}
	err := within(t, func() error { return Rename(0, filepath.Join(dir, "missing.txt"), "x.txt") })
	t.Logf("renaming what is not there: %v", err)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("want an error that is fs.ErrNotExist, got %v", err)
	}

	// A file another program holds open.
	held := filepath.Join(dir, "held.txt")
	write(t, held, "held")
	f, err := os.Open(held)
	if err != nil {
		t.Fatal(err)
	}
	err = within(t, func() error { return Rename(0, held, "held2.txt") })
	f.Close()
	t.Logf("renaming a file held open: %v", err)
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Errorf("want a sharing violation, got %v", err)
	}
	if !exists(held) {
		t.Error("the file held open was renamed")
	}
}

func TestDeletePermanently(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "a")
	write(t, filepath.Join(dir, "tree", "deep", "c.txt"), "c")
	write(t, filepath.Join(dir, "stays.txt"), "stays")
	readOnly := filepath.Join(dir, "readonly.txt")
	write(t, readOnly, "r")
	if err := os.Chmod(readOnly, 0o444); err != nil {
		t.Fatal(err)
	}
	err := within(t, func() error {
		return Delete(0, []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "tree"), readOnly}, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"stays.txt"}) {
		t.Errorf("left: %q", got)
	}
	err = within(t, func() error { return Delete(0, []string{filepath.Join(dir, "missing.txt")}, true) })
	t.Logf("deleting what is not there: %v", err)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("want an error that is fs.ErrNotExist, got %v", err)
	}
	if err := within(t, func() error { return Delete(0, nil, true) }); err != nil {
		t.Errorf("deleting nothing: %v", err)
	}
}

// TestDeleteToTrash leaves a file in the user's Recycle Bin, so it runs
// only when asked to: CAIRN_TEST_TRASH=1.
func TestDeleteToTrash(t *testing.T) {
	if os.Getenv("CAIRN_TEST_TRASH") != "1" {
		t.Skip("set CAIRN_TEST_TRASH=1 to put a file in the Recycle Bin")
	}
	path := filepath.Join(t.TempDir(), "cairn-test-delete-me.txt")
	write(t, path, "delete me")
	if err := within(t, func() error { return Delete(0, []string{path}, false) }); err != nil {
		t.Fatal(err)
	}
	if exists(path) {
		t.Error("the file is still there")
	}
}

func TestNewFolderAndFile(t *testing.T) {
	dir := t.TempDir()
	for _, want := range []string{"New folder", "New folder (2)", "New folder (3)"} {
		got, err := NewFolder(dir, "New folder")
		if err != nil || got != filepath.Join(dir, want) {
			t.Fatalf("NewFolder = %q, %v; want %s", got, err, want)
		}
		if info, err := os.Stat(got); err != nil || !info.IsDir() {
			t.Fatalf("%s is no directory", got)
		}
	}
	for _, want := range []string{"New Text Document.txt", "New Text Document (2).txt", "New Text Document (3).txt"} {
		got, err := NewFile(dir, "New Text Document.txt")
		if err != nil || got != filepath.Join(dir, want) {
			t.Fatalf("NewFile = %q, %v; want %s", got, err, want)
		}
		if info, err := os.Stat(got); err != nil || info.IsDir() || info.Size() != 0 {
			t.Fatalf("%s is no empty file", got)
		}
	}
	// Names without an extension, and with a dot in front alone.
	for name, want := range map[string]string{"notes": "notes (2)", ".config": ".config (2)", "a.tar.gz": "a.tar (2).gz"} {
		if _, err := NewFile(dir, name); err != nil {
			t.Fatal(err)
		}
		got, err := NewFile(dir, name)
		if err != nil || got != filepath.Join(dir, want) {
			t.Errorf("the second %s = %q, %v; want %s", name, got, err, want)
		}
	}
	// A directory in the way of a file, and a file in the way of a
	// directory, are taken names too.
	if got, err := NewFile(dir, "New folder"); err != nil || got != filepath.Join(dir, "New folder (4)") {
		t.Errorf("a file named as a directory = %q, %v", got, err)
	}
	if got, err := NewFolder(dir, "notes"); err != nil || got != filepath.Join(dir, "notes (3)") {
		t.Errorf("a directory named as a file = %q, %v", got, err)
	}
	// A dot in a directory's name is no extension.
	if _, err := NewFolder(dir, "v1.2"); err != nil {
		t.Fatal(err)
	}
	if got, err := NewFolder(dir, "v1.2"); err != nil || got != filepath.Join(dir, "v1.2 (2)") {
		t.Errorf("the second v1.2 = %q, %v", got, err)
	}
	// Where nothing can be made.
	if _, err := NewFolder(filepath.Join(dir, "missing"), "x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("in a directory that is not there: %v", err)
	}
	if _, err := NewFile(dir, "what?.txt"); err == nil {
		t.Error("a name the system refuses was made")
	}
	// A path longer than the system takes without the long form.
	long := dir
	for len(long) < 300 {
		long = filepath.Join(long, strings.Repeat("d", 50))
	}
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := NewFile(long, "f.txt"); err != nil || !exists(got) {
		t.Errorf("in a long path: %q, %v", got, err)
	}
}
