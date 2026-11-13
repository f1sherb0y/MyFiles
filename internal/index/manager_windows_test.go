package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"myfiles/internal/search"
)

// liveDrive is a manager of the drive the tests' folder is on, for the
// tests that follow a real drive: MYFILES_INDEX_LIVE_TEST=1 asks for
// them, as they read the whole drive.
func liveDrive(t *testing.T, store string) (*Manager, string, chan struct{}) {
	t.Helper()
	if os.Getenv("MYFILES_INDEX_LIVE_TEST") == "" {
		t.Skip("set MYFILES_INDEX_LIVE_TEST=1 to read and follow a real drive")
	}
	dir := t.TempDir()
	root := filepath.VolumeName(dir) + `\`
	info, ok := driveInfo(root)
	if !ok || !info.Journal {
		t.Skipf("%s keeps no journal of changes", root)
	}
	changed := make(chan struct{}, 1)
	m := NewManager(Config{Dir: store, Changed: func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}})
	// Only the tests' drive.
	for _, d := range localDrives() {
		if rootKey(d.Root) != rootKey(root) {
			m.cfg.Off = append(m.cfg.Off, d.Root)
		}
	}
	m.Start()
	t.Cleanup(m.Close)
	return m, dir, changed
}

// eventually waits for what a search of a folder finds to be what is
// wanted.
func eventually(t *testing.T, m *Manager, changed chan struct{}, query, scope string, wantPaths ...string) {
	t.Helper()
	deadline := time.After(60 * time.Second)
	var got []string
	for {
		req := &Request{Query: search.ParseQuery(query, search.DefaultOptions(), ':', nil), Scope: scope, Limit: 100}
		f := m.Search(context.Background(), req)
		got = got[:0]
		for _, r := range f.Results {
			got = append(got, r.Path)
		}
		if !f.Reading && !f.Uncovered && len(got) == len(wantPaths) {
			same := true
			for i := range got {
				same = same && got[i] == wantPaths[i]
			}
			if same {
				return
			}
		}
		select {
		case <-changed:
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatalf("%q in %s: found %v, want %v", query, scope, got, wantPaths)
		}
	}
}

func TestFollowDrive(t *testing.T) {
	store := t.TempDir()
	m, dir, changed := liveDrive(t, store)
	write := func(rel string) string {
		path := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// There when the drive is read, or made just after: found either way.
	a := write(`alpha\zebra-unicorn-one.txt`)
	eventually(t, m, changed, "zebra-unicorn", dir, a)
	t.Logf("drives: %+v", m.Drives())

	b := write(`alpha\zebra-unicorn-two.txt`)
	eventually(t, m, changed, "zebra-unicorn", dir, a, b)

	// Renamed.
	c := filepath.Join(dir, `alpha\zebra-unicorn-2.txt`)
	if err := os.Rename(b, c); err != nil {
		t.Fatal(err)
	}
	eventually(t, m, changed, "zebra-unicorn", dir, c, a)

	// The folder renamed: its files follow.
	if err := os.Rename(filepath.Join(dir, "alpha"), filepath.Join(dir, "beta")); err != nil {
		t.Fatal(err)
	}
	a, c = filepath.Join(dir, `beta\zebra-unicorn-one.txt`), filepath.Join(dir, `beta\zebra-unicorn-2.txt`)
	eventually(t, m, changed, "zebra-unicorn", dir, c, a)
	eventually(t, m, changed, "zebra-unicorn", filepath.Join(dir, "beta"), c, a)

	// A folder made with a file in it at once, and the folder moved.
	d := write(`gamma\deep\zebra-unicorn-three.txt`)
	eventually(t, m, changed, "zebra-unicorn-three", dir, d)
	if err := os.Rename(filepath.Join(dir, "gamma"), filepath.Join(dir, `beta\gamma`)); err != nil {
		t.Fatal(err)
	}
	d = filepath.Join(dir, `beta\gamma\deep\zebra-unicorn-three.txt`)
	eventually(t, m, changed, "zebra-unicorn-three", filepath.Join(dir, "beta"), d)

	// Deleted: a file, then a folder with all it holds.
	os.Remove(a)
	eventually(t, m, changed, "zebra-unicorn", dir, c, d)
	os.RemoveAll(filepath.Join(dir, `beta\gamma`))
	eventually(t, m, changed, "zebra-unicorn", dir, c)

	// Closed, changed behind its back, and started again: the index kept
	// catches up from the journal.
	m.Close()
	e := write(`beta\zebra-unicorn-late.txt`)
	os.Remove(c)
	changed2 := make(chan struct{}, 1)
	m2 := NewManager(Config{Dir: store, Off: m.cfg.Off, Changed: func() {
		select {
		case changed2 <- struct{}{}:
		default:
		}
	}})
	start := time.Now()
	m2.Start()
	defer m2.Close()
	eventually(t, m2, changed2, "zebra-unicorn", dir, e)
	for _, s := range m2.Drives() {
		if s.State == StateReady {
			t.Logf("%s: caught up %v after starting, read at %v by the table: %v, live: %v", s.Root,
				time.Since(start).Round(time.Millisecond), s.Built.Format(time.TimeOnly), s.Table, s.Live)
			if s.Built.After(start) {
				t.Errorf("%s was read again, where its index only had to catch up", s.Root)
			}
		}
	}
}

func TestSearchFolderUncovered(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "needle-file.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "other.txt"), nil, 0o644)
	m := NewManager(Config{Dir: t.TempDir()})
	defer m.Close()
	req := &Request{Query: search.ParseQuery("needle", search.DefaultOptions(), ':', nil), Scope: dir, Limit: 10}
	if f := m.Search(context.Background(), req); !f.Uncovered {
		t.Fatalf("a manager without drives covers %s: %+v", dir, f)
	}
	f, err := m.SearchFolder(context.Background(), req)
	if err != nil || len(f.Results) != 1 || f.Results[0].Path != filepath.Join(dir, "sub", "needle-file.txt") {
		t.Fatalf("found %+v, %v", f.Results, err)
	}
}

// eventuallyAll waits for a search of a folder to find the paths wanted,
// in whatever order.
func eventuallyAll(t *testing.T, m *Manager, changed chan struct{}, query, scope string, wantPaths ...string) {
	t.Helper()
	sort.Strings(wantPaths)
	deadline := time.After(30 * time.Second)
	var got []string
	for {
		req := &Request{Query: search.ParseQuery(query, search.DefaultOptions(), ':', nil), Scope: scope, Limit: 100}
		f := m.Search(context.Background(), req)
		got = got[:0]
		for _, r := range f.Results {
			got = append(got, r.Path)
		}
		sort.Strings(got)
		if !f.Reading && !f.Uncovered && slices.Equal(got, wantPaths) {
			return
		}
		select {
		case <-changed:
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatalf("%q in %s: found %v, want %v", query, scope, got, wantPaths)
		}
	}
}

// A folder taken into the index, which no journal tells of, is followed
// by what the system tells of its paths: files made, renamed, moved in
// with what they hold, and gone.
func TestFollowFolder(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "quokka-one.txt"), nil, 0o644)
	changed := make(chan struct{}, 1)
	m := NewManager(Config{Dir: t.TempDir(), Extra: []string{dir}, Changed: func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}})
	for _, d := range localDrives() {
		m.cfg.Off = append(m.cfg.Off, d.Root)
	}
	m.Start()
	t.Cleanup(m.Close)
	p := func(rel string) string { return filepath.Join(dir, rel) }
	eventuallyAll(t, m, changed, "quokka", dir, p(`sub\quokka-one.txt`))
	live := func() bool {
		for _, d := range m.Drives() {
			if rootKey(d.Root) == rootKey(dir+`\`) {
				return d.Live
			}
		}
		return false
	}
	for deadline := time.Now().Add(10 * time.Second); !live(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the folder is not followed: %+v", m.Drives())
		}
	}

	// A file made, and one made in a folder made with it.
	os.WriteFile(p("quokka-two.txt"), []byte("12345"), 0o644)
	os.MkdirAll(p(`new\deeper`), 0o755)
	os.WriteFile(p(`new\deeper\quokka-three.txt`), nil, 0o644)
	eventuallyAll(t, m, changed, "quokka", dir, p("quokka-two.txt"), p(`new\deeper\quokka-three.txt`), p(`sub\quokka-one.txt`))

	// Renamed, and its folder renamed: what the folder holds goes with it.
	os.Rename(p("quokka-two.txt"), p("quokka-2.txt"))
	os.Rename(p("sub"), p("renamed"))
	eventuallyAll(t, m, changed, "quokka", dir, p("quokka-2.txt"), p(`new\deeper\quokka-three.txt`), p(`renamed\quokka-one.txt`))

	// A folder moved in from elsewhere is told of alone: what it holds is
	// read.
	os.MkdirAll(filepath.Join(outside, "arrived", "inner"), 0o755)
	os.WriteFile(filepath.Join(outside, "arrived", "inner", "quokka-four.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(outside, "arrived", "quokka-five.txt"), nil, 0o644)
	if err := os.Rename(filepath.Join(outside, "arrived"), p("arrived")); err != nil {
		t.Fatal(err)
	}
	eventuallyAll(t, m, changed, "quokka-f", dir, p(`arrived\quokka-five.txt`), p(`arrived\inner\quokka-four.txt`))

	// Gone: a file, and a folder with what it held.
	os.Remove(p("quokka-2.txt"))
	os.RemoveAll(p("new"))
	eventuallyAll(t, m, changed, "quokka", dir, p(`arrived\quokka-five.txt`), p(`renamed\quokka-one.txt`), p(`arrived\inner\quokka-four.txt`))

	// What was followed is written when the manager closes, and is there
	// when the next one starts.
	m.Close()
	m2 := NewManager(Config{Dir: m.cfg.Dir, Extra: []string{dir}, Off: m.cfg.Off})
	m2.Start()
	t.Cleanup(m2.Close)
	eventuallyAll(t, m2, nil, "quokka", dir, p(`arrived\quokka-five.txt`), p(`renamed\quokka-one.txt`), p(`arrived\inner\quokka-four.txt`))
	for _, d := range m2.Drives() {
		if rootKey(d.Root) == rootKey(dir+`\`) && d.Built.After(time.Now().Add(-time.Hour)) && d.Files != 3 {
			t.Errorf("the index written holds %d files, want 3", d.Files)
		}
	}
}

// A drive asked for is let go at once, with what was open on it closed,
// and its index starts again when the drive is back.
func TestDriveGoesAndComes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wombat.txt"), nil, 0o644)
	m := NewManager(Config{Dir: t.TempDir(), Extra: []string{dir}})
	for _, d := range localDrives() {
		m.cfg.Off = append(m.cfg.Off, d.Root)
	}
	m.Start()
	t.Cleanup(m.Close)
	eventuallyAll(t, m, nil, "wombat", dir, filepath.Join(dir, "wombat.txt"))

	// What the system tells a guard closes what is held, before it returns.
	closed := 0
	m.mu.Lock()
	d := m.drives[rootKey(dir)]
	m.mu.Unlock()
	forget := d.hold(func() { closed++ })
	if g := watchEject(dir, d.letGo); g == nil {
		t.Log("the system does not tell of this drive being asked for")
	} else {
		g.told(cmQueryRemoveFailed)
		if closed != 0 {
			t.Fatal("let go of a drive that stays")
		}
		g.told(cmQueryRemove)
		if closed != 1 || g.h != 0 {
			t.Fatalf("asked for: %d closed, the guard's handle %v", closed, g.h)
		}
		g.Close()
	}
	forget()

	m.driveWent(dir)
	req := &Request{Query: search.ParseQuery("wombat", search.DefaultOptions(), ':', nil), Scope: dir, Limit: 10}
	if f := m.Search(context.Background(), req); len(f.Results) != 0 || !f.Uncovered {
		t.Fatalf("a drive that went is searched: %+v", f)
	}
	for _, s := range m.Drives() {
		if rootKey(s.Root) == rootKey(dir) && s.State != StateOff {
			t.Fatalf("a drive that went is %v", s.State)
		}
	}
	// Changed while it was away: read again when it is back.
	os.WriteFile(filepath.Join(dir, "wombat-two.txt"), nil, 0o644)
	m.driveCame(dir)
	eventuallyAll(t, m, nil, "wombat", dir, filepath.Join(dir, "wombat.txt"), filepath.Join(dir, "wombat-two.txt"))
}

// A shortcut is of the kind of what it points at: asked for documents, a
// search finds the shortcut to a document, not the one to a program.
func TestShortcutsByTarget(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "thing plan.docx"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "thing tool.exe"), nil, 0o644)
	os.Mkdir(filepath.Join(dir, "thing folder"), 0o755)
	for link, target := range map[string]string{
		"thing to-doc.lnk": "thing plan.docx", "thing to-exe.lnk": "thing tool.exe", "thing to-folder.lnk": "thing folder",
	} {
		script := `$s = (New-Object -ComObject WScript.Shell).CreateShortcut('` + filepath.Join(dir, link) +
			`'); $s.TargetPath = '` + filepath.Join(dir, target) + `'; $s.Save()`
		if out, err := exec.Command("powershell", "-NoProfile", "-Command", script).CombinedOutput(); err != nil {
			t.Skipf("no shortcut made: %v %s", err, out)
		}
	}
	m := NewManager(Config{Dir: t.TempDir()})
	defer m.Close()
	found := func(query, kind string) []string {
		t.Helper()
		req := &Request{Query: search.ParseQuery(query, search.DefaultOptions(), ':', nil), Scope: dir, Limit: 100}
		if kind != "" {
			req.Filter = search.NewFilter(kind, nil)
		}
		f, err := m.SearchFolder(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, r := range f.Results {
			names = append(names, r.Name)
		}
		sort.Strings(names)
		if f.Total != len(names) {
			t.Errorf("%q of %q: %d counted, %d found", query, kind, f.Total, len(names))
		}
		return names
	}
	for _, c := range []struct {
		query, kind string
		want        []string
	}{
		{"thing", "", []string{"thing folder", "thing plan.docx", "thing to-doc.lnk", "thing to-exe.lnk", "thing to-folder.lnk", "thing tool.exe"}},
		{"thing", "@doc", []string{"thing plan.docx", "thing to-doc.lnk"}},
		{"thing", "@app", []string{"thing to-exe.lnk", "thing tool.exe"}},
		{"thing", ":f", []string{"thing folder", "thing to-folder.lnk"}},
		{"thing :@doc", "", []string{"thing plan.docx", "thing to-doc.lnk"}},
		{"thing :@doc|app", "", []string{"thing plan.docx", "thing to-doc.lnk", "thing to-exe.lnk", "thing tool.exe"}},
		// Asked for by its own extension, a shortcut is a shortcut.
		{"thing :.lnk", "", []string{"thing to-doc.lnk", "thing to-exe.lnk", "thing to-folder.lnk"}},
		{"thing :.docx", "", []string{"thing plan.docx"}},
	} {
		if got := found(c.query, c.kind); !slices.Equal(got, c.want) {
			t.Errorf("%q of %q finds %v, want %v", c.query, c.kind, got, c.want)
		}
	}
}
