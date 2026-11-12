package app

import (
	"image"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"myfiles/internal/fsys"
)

func TestIconFile(t *testing.T) {
	shared := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range shared.Pix {
		shared.Pix[i] = byte(i)
	}
	other := image.NewRGBA(image.Rect(0, 0, 32, 32))
	other.Pix[5] = 9
	in := map[string]*image.RGBA{".go": shared, ".mod": shared, ".txt": other, ".none": nil, "": other, dirClass: shared}
	data := encodeIcons(in)
	// The picture two types share is written once.
	if want := 8 + (4 + 16*16*4) + (4 + 32*32*4) + 4; len(data) < want || len(data) > want+len(in)*16 {
		t.Errorf("the file is %d bytes, the pictures alone %d", len(data), want)
	}
	out, err := decodeIcons(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("read %d keys, wrote %d", len(out), len(in))
	}
	for key, img := range in {
		got, ok := out[key]
		switch {
		case !ok:
			t.Errorf("%q is missing", key)
		case img == nil:
			if got != nil {
				t.Errorf("%q has a picture, and had none", key)
			}
		case got == nil || got.Rect != img.Rect || !slices.Equal(got.Pix, img.Pix):
			t.Errorf("%q came back another picture", key)
		}
	}
	if out[".go"] != out[".mod"] {
		t.Error("types that shared a picture have one each")
	}
	// A file cut short, or of something else, is no icons, and no crash.
	for n := range len(data) {
		if _, err := decodeIcons(data[:n]); err == nil {
			t.Fatalf("%d of %d bytes read as a file of icons", n, len(data))
		}
	}
	if _, err := decodeIcons([]byte("not icons at all")); err == nil {
		t.Error("another file read as one of icons")
	}
}

// TestIconsFitTheirSize checks that the system's largest pictures are
// made as small as a view shows them, once per picture, and that smaller
// ones stay as they are.
func TestIconsFitTheirSize(t *testing.T) {
	ic := newIcons(1, func(func()) {}, func() {}, nil)
	large := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for i := range large.Pix {
		large.Pix[i] = 200
	}
	got := ic.fit(large, 60)
	if got.Rect.Dx() != 60 || got.Rect.Dy() != 60 {
		t.Fatalf("a 256 pixel icon for 60 pixels is %v", got.Rect)
	}
	if p := got.Pix[4*(30*60+30):][:4]; p[0] != 200 || p[3] != 200 {
		t.Errorf("the middle of an even picture came out %v", p)
	}
	if ic.fit(large, 60) != got {
		t.Error("the same picture was made small twice")
	}
	small := image.NewRGBA(image.Rect(0, 0, 48, 48))
	if ic.fit(small, 60) != small || ic.fit(nil, 60) != nil {
		t.Error("a picture small enough was made anew")
	}
}

// mainThread stands in for the main thread: what is posted waits until
// the test runs it.
type mainThread struct {
	mu    sync.Mutex
	queue []func()
}

func (m *mainThread) post(fn func()) {
	m.mu.Lock()
	m.queue = append(m.queue, fn)
	m.mu.Unlock()
}

// run runs what was posted until cond holds.
func (m *mainThread) run(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.mu.Lock()
		q := m.queue
		m.queue = nil
		m.mu.Unlock()
		for _, fn := range q {
			fn()
		}
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTypesKnownFromDisk checks that what one run learned of types the
// next knows from its first frame, and that the system saying the same
// again changes nothing on screen.
func TestTypesKnownFromDisk(t *testing.T) {
	store := &iconStore{dir: t.TempDir()}
	file := &fsys.Entry{Name: "notes.txt"}
	dir := &fsys.Entry{Name: "docs", Attr: fsys.AttrDir}

	main := &mainThread{}
	first := newIcons(1, main.post, func() {}, store)
	set := first.at(iconSize)
	if name := first.typeName(file); name != "" {
		t.Errorf("a type not known yet is called %q, where nothing should show", name)
	}
	if _, final := set.forEntry(`C:\x`, file); final {
		t.Error("an icon not fetched yet is final")
	}
	first.typeName(dir)
	main.run(t, "the system's answers", func() bool {
		_, icon := set.byKey[".txt"]
		return first.types[".txt"] != "" && first.types[dirClass] != "" && icon && set.dir != nil && set.file != nil
	})
	if !first.saving || !first.namesDirty || !set.dirty {
		t.Fatal("what was learned is not due to be written")
	}
	first.save()
	main.run(t, "the files", func() bool {
		_, names := os.Stat(filepath.Join(store.dir, "types.json"))
		_, icons := os.Stat(store.iconFile(set.px))
		return names == nil && icons == nil
	})
	// The write is whole before the next run reads it.
	store.mu.Lock()
	store.mu.Unlock()

	changes := 0
	second := newIcons(1, main.post, func() { changes++ }, store)
	set2 := second.at(iconSize)
	if got, want := second.typeName(file), first.types[".txt"]; got != want {
		t.Errorf("the type is called %q from the start, the system calls it %q", got, want)
	}
	if got, want := second.typeName(dir), first.types[dirClass]; got != want {
		t.Errorf("folders are called %q from the start, the system calls them %q", got, want)
	}
	if icon, final := set2.forEntry(`C:\x`, file); !final || icon == nil {
		t.Error("the type's icon is not there from the start")
	}
	if icon, final := set2.forEntry(`C:\x`, dir); !final || icon == nil {
		t.Error("the folder icon is not there from the start")
	}
	// The system is asked all the same, and says what the disk said.
	main.run(t, "the system's answers", func() bool {
		second.mu.Lock()
		defer second.mu.Unlock()
		return len(second.queue) == 0 && len(second.results) == 0 && !second.posted
	})
	if changes != 0 || second.epoch != 0 {
		t.Errorf("the same icons and names again drew the windows %d times and replaced %d icons", changes, second.epoch)
	}

	// A name the disk has wrong is put right, and the windows told.
	store.save(map[string]string{".txt": "Something else"}, nil)
	third := newIcons(1, main.post, func() { changes++ }, store)
	if got := third.typeName(file); got != "Something else" {
		t.Fatalf("the name on disk is %q", got)
	}
	main.run(t, "the right name", func() bool { return third.types[".txt"] == first.types[".txt"] })
	if changes == 0 {
		t.Error("a name that changed drew nothing again")
	}
}

// TestAwait checks that what a view is about to show is waited for, and
// what is known already is not.
func TestAwait(t *testing.T) {
	main := &mainThread{}
	ic := newIcons(1, main.post, func() {}, nil)
	set := ic.at(iconSize)
	file := &fsys.Entry{Name: "notes.txt"}
	ic.await(10*time.Second, func() {
		set.forEntry(`C:\x`, file)
		ic.typeName(file)
	})
	if ic.types[".txt"] == "" {
		t.Error("the type's name was not waited for")
	}
	if _, ok := set.byKey[".txt"]; !ok {
		t.Error("the type's icon was not waited for")
	}
	start := time.Now()
	ic.await(10*time.Second, func() {
		set.forEntry(`C:\x`, file)
		ic.typeName(file)
	})
	if took := time.Since(start); took > time.Second {
		t.Errorf("what is known was waited for: %v", took)
	}
	// What takes longer than the limit is not waited out.
	start = time.Now()
	ic.await(time.Millisecond, func() {
		for _, name := range []string{"a.png", "b.zip", "c.pdf", "d.html", "e.mp3", "f.docx"} {
			set.forEntry(`C:\x`, &fsys.Entry{Name: name})
		}
	})
	if took := time.Since(start); took > time.Second {
		t.Errorf("the limit did not hold: %v", took)
	}
}

// TestFolderShowsAtOnce checks that the first frame of a folder is the
// picture that stays: nothing in it moves or changes in the frames after,
// which the eye would take for a flicker. Only the status bar, which
// tells how long the folder took once it shows, may differ.
func TestFolderShowsAtOnce(t *testing.T) {
	old := iconWait
	iconWait = 10 * time.Second
	defer func() { iconWait = old }()

	entries := []string{"a/", "b/", "c/", "notes.txt"}
	for i := range 40 {
		entries = append(entries, "docs/file"+string(rune('a'+i%26))+string(rune('a'+i/26))+[]string{".txt", ".zip", ".html"}[i%3])
	}
	entries = append(entries, "docs/sub/", "docs/more/")
	for mode, spec := range viewSpecs {
		t.Run(spec.id, func(t *testing.T) {
			h := newHarness(t, entries...)
			// Beside the sidebar the files have less than the window's
			// width.
			h.a.set.Sidebar = true
			tab := h.w.current()
			h.w.setView(tab, viewMode(mode))
			h.settle()

			h.staysAsShown("the folder", func() *Tab {
				h.w.navigate(tab, h.path("docs"), navPush, "")
				return tab
			})
			// So does a folder in a new tab, whose rows no frame has
			// measured yet.
			h.staysAsShown("the new tab", func() *Tab {
				return h.w.tabs[h.w.newTab(h.w.layout.Focus, h.path("docs"), "")]
			})
		})
	}
}

// staysAsShown runs show, which shows a folder in a tab, and fails the
// test when the first frame drawn after it is not the picture that stays.
func (h *harness) staysAsShown(what string, show func() *Tab) {
	h.t.Helper()
	var frames []*image.RGBA
	h.presented = func() {
		img := h.tt.Image()
		frames = append(frames, &image.RGBA{Pix: slices.Clone(img.Pix), Stride: img.Stride, Rect: img.Rect})
	}
	tab := show()
	slow := tab.loading
	h.settle()
	h.presented = nil
	if slow {
		h.t.Logf("%s took too long to read for its first frame", what)
		return
	}
	if len(frames) < 2 {
		h.t.Fatalf("%s: %d frames drawn", what, len(frames))
	}
	first, last := frames[0], frames[len(frames)-1]
	// The status bar is the window's last rows of pixels.
	above := first.Rect
	above.Max.Y -= statusHeight
	if box := differ(first, last, above); !box.Empty() {
		h.t.Errorf("the first frame of %s differs from the one that stays within %v (of %v)", what, box, above)
	}
}

// differ is the box around the pixels of two pictures that differ within
// a rectangle, empty when none does.
func differ(a, b *image.RGBA, within image.Rectangle) image.Rectangle {
	var box image.Rectangle
	for y := within.Min.Y; y < within.Max.Y; y++ {
		ra, rb := a.Pix[y*a.Stride:], b.Pix[y*b.Stride:]
		for x := within.Min.X; x < within.Max.X; x++ {
			if [4]byte(ra[4*x:]) != [4]byte(rb[4*x:]) {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

// TestFolderShowsWithTypes checks that a folder's first frame has the
// names of its types, where the system gives them in time.
func TestFolderShowsWithTypes(t *testing.T) {
	old := iconWait
	iconWait = 10 * time.Second
	defer func() { iconWait = old }()

	h := newHarness(t, "docs/a.txt", "docs/b.zip")
	tab := h.w.current()
	h.w.navigate(tab, h.path("docs"), navPush, "")
	if tab.loading {
		t.Skip("the folder took too long to read for its first frame")
	}
	ic := h.a.icons
	for _, ext := range []string{".txt", ".zip"} {
		if ic.types[ext] == "" {
			t.Errorf("the folder arrived without the name of %s files", ext)
		}
		if _, ok := ic.at(iconSize).byKey[ext]; !ok {
			t.Errorf("the folder arrived without the icon of %s files", ext)
		}
	}
	h.tt.Frame()
	if texts := h.tt.Texts(); !slices.Contains(texts, ic.types[".txt"]) {
		t.Errorf("the first frame does not show %q; texts %q", ic.types[".txt"], texts)
	}
	h.settle()
}
