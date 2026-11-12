package watch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// seen collects what a watch tells.
type seen struct {
	mu     sync.Mutex
	events []Event
}

func (s *seen) note(e Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

// take returns what was told since the last call.
func (s *seen) take() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.events
	s.events = nil
	return e
}

// wait waits for an event of a kind, and returns it.
func (s *seen) wait(t *testing.T, what string, kind Kind) Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range s.take() {
			if e.Kind == kind {
				return e
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("not told of %s", what)
	return Event{}
}

// none fails when something is told within a while.
func (s *seen) none(t *testing.T, what string) {
	t.Helper()
	time.Sleep(latest + 200*time.Millisecond)
	if e := s.take(); len(e) > 0 {
		t.Fatalf("told of %s: %+v", what, e)
	}
}

// root is a folder of the test's, by its real path.
func root(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if long, err := filepath.EvalSymlinks(dir); err == nil {
		dir = long
	}
	return dir
}

func watch(t *testing.T, h *Hub, path string) (*seen, func()) {
	t.Helper()
	s := &seen{}
	cancel, ok := h.Watch(path, s.note)
	if !ok {
		t.Skipf("%s is on no drive the hub watches", path)
	}
	// The watch of the drive starts on a goroutine of its own.
	time.Sleep(100 * time.Millisecond)
	return s, cancel
}

func TestWatch(t *testing.T) {
	base := root(t)
	shown := filepath.Join(base, "above", "shown")
	if err := os.MkdirAll(filepath.Join(shown, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New()
	defer h.Close()
	s, cancel := watch(t, h, shown)
	defer cancel()

	// What the folder holds: made, written, renamed, deleted.
	file := filepath.Join(shown, "a.txt")
	steps := []struct {
		what string
		do   func() error
	}{
		{"a file made", func() error { return os.WriteFile(file, []byte("one"), 0o644) }},
		{"a file written", func() error { return os.WriteFile(file, []byte("one two"), 0o644) }},
		{"a file renamed", func() error { return os.Rename(file, filepath.Join(shown, "b.txt")) }},
		{"a file deleted", func() error { return os.Remove(filepath.Join(shown, "b.txt")) }},
		{"a folder made", func() error { return os.Mkdir(filepath.Join(shown, "new"), 0o755) }},
	}
	for _, step := range steps {
		if err := step.do(); err != nil {
			t.Fatal(step.what, err)
		}
		s.wait(t, step.what, Changed)
		s.none(t, "more after "+step.what)
	}

	// Many changes together are told once or twice, not each.
	for i := 0; i < 200; i++ {
		if err := os.WriteFile(filepath.Join(shown, fmt.Sprintf("f%d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.wait(t, "many files made", Changed)
	time.Sleep(latest + 200*time.Millisecond)
	if n := len(s.take()); n > 3 {
		t.Errorf("200 files made were told %d times more", n)
	}

	// What is deeper, or beside, is not the folder's.
	if err := os.WriteFile(filepath.Join(shown, "sub", "deep.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "above", "beside.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "above", "shown2"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.none(t, "changes that are not the folder's")

	// The folder above it can be renamed, which no watch of the folder
	// itself allows, and the watch follows.
	if err := os.Rename(filepath.Join(base, "above"), filepath.Join(base, "Renamed Above")); err != nil {
		t.Fatalf("the folder above a watched one cannot be renamed: %v", err)
	}
	moved := filepath.Join(base, "Renamed Above", "shown")
	if e := s.wait(t, "the rename of the folder above", Moved); e.To != moved {
		t.Fatalf("after the rename the folder is said to be at %q, not %q", e.To, moved)
	}
	if err := os.WriteFile(filepath.Join(moved, "after.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.wait(t, "a file made under the new name", Changed)

	// So can the folder itself.
	again := filepath.Join(base, "Renamed Above", "shown again")
	if err := os.Rename(moved, again); err != nil {
		t.Fatal(err)
	}
	if e := s.wait(t, "the rename of the folder", Moved); e.To != again {
		t.Fatalf("after its rename the folder is said to be at %q, not %q", e.To, again)
	}
	s.take()

	// Deleted, with all it holds.
	if err := os.RemoveAll(again); err != nil {
		t.Fatal(err)
	}
	s.wait(t, "the folder deleted", Gone)

	// Nothing is told once cancelled.
	other := filepath.Join(base, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	s2, cancel2 := watch(t, h, other)
	cancel2()
	cancel2()
	if err := os.WriteFile(filepath.Join(other, "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s2.none(t, "a change after the watch was cancelled")
}

// A folder above a watched one that is deleted takes it along.
func TestWatchAboveDeleted(t *testing.T) {
	base := root(t)
	shown := filepath.Join(base, "above", "between", "shown")
	if err := os.MkdirAll(shown, 0o755); err != nil {
		t.Fatal(err)
	}
	h := New()
	defer h.Close()
	s, cancel := watch(t, h, shown)
	defer cancel()
	if err := os.RemoveAll(filepath.Join(base, "above")); err != nil {
		t.Fatal(err)
	}
	s.wait(t, "the folder above deleted", Gone)
}

// A folder reached through a link is watched where it really is.
func TestWatchThroughLink(t *testing.T) {
	base := root(t)
	real := filepath.Join(base, "real", "shown")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, filepath.Join(base, "real")).CombinedOutput(); err != nil {
		t.Skipf("no junction: %v: %s", err, out)
	}
	defer os.Remove(link)
	h := New()
	defer h.Close()
	s, cancel := watch(t, h, filepath.Join(link, "shown"))
	defer cancel()
	if err := os.WriteFile(filepath.Join(real, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.wait(t, "a file made in the folder the link leads to", Changed)
	// Renamed where it really is: where the link shows it now cannot be
	// told, and it is said gone from where it was shown.
	if err := os.Rename(real, filepath.Join(base, "real", "renamed")); err != nil {
		t.Fatal(err)
	}
	s.wait(t, "the rename behind the link", Gone)
}

// What is on no drive is not the hub's.
func TestWatchOnlyDrives(t *testing.T) {
	h := New()
	defer h.Close()
	for _, path := range []string{`\\localhost\nothing\here`, `C:\no such folder anywhere 7f3a`, ""} {
		if cancel, ok := h.Watch(path, func(Event) {}); ok {
			cancel()
			t.Errorf("%q is watched", path)
		}
	}
	var none *Hub
	if _, ok := none.Watch(os.TempDir(), func(Event) {}); ok {
		t.Error("no hub watches")
	}
}

// While another program writes files by the thousand, the changes of the
// folder watched are told still, and once the drive is calm.
func TestWatchInFlood(t *testing.T) {
	oldFlood, oldCalm := floodRate, calmRate
	floodRate, calmRate = 200, 50
	defer func() { floodRate, calmRate = oldFlood, oldCalm }()

	base := root(t)
	shown := filepath.Join(base, "shown")
	noisy := filepath.Join(base, "noisy")
	for _, dir := range []string{shown, noisy} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h := New()
	defer h.Close()
	s, cancel := watch(t, h, shown)
	defer cancel()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				name := filepath.Join(noisy, fmt.Sprintf("w%d-%d", w, i%500))
				os.WriteFile(name, []byte("x"), 0o644)
				os.Remove(name)
			}
		}()
	}
	// Well into the flood.
	before := floods.Load()
	time.Sleep(1200 * time.Millisecond)
	if floods.Load() == before {
		t.Fatal("thousands of changes a second are read one by one still")
	}
	s.take()
	if err := os.WriteFile(filepath.Join(shown, "in the flood.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.wait(t, "a file made during the flood", Changed)
	// Nothing more of the folder: the flood beside it is not its own.
	time.Sleep(time.Second)
	if e := s.take(); len(e) > 0 {
		t.Errorf("the flood beside the folder was told to it: %+v", e)
	}
	close(stop)
	wg.Wait()
	// Calm again: told once of what the times may not have shown, then
	// change by change as before.
	s.wait(t, "the end of the flood", Changed)
	time.Sleep(latest + 300*time.Millisecond)
	s.take()
	if err := os.WriteFile(filepath.Join(shown, "after.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.wait(t, "a file made after the flood", Changed)
}
