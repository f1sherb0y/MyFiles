package shell

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unsafe"

	"cairn/internal/fsys"
)

// alphaOf counts the pixels of a picture that are transparent, in
// between and opaque, and those whose colors exceed their alpha, which a
// premultiplied picture has none of.
func alphaOf(img *image.RGBA) (clear, partial, opaque, invalid int) {
	for i := 0; i < len(img.Pix); i += 4 {
		a := img.Pix[i+3]
		switch a {
		case 0:
			clear++
		case 255:
			opaque++
		default:
			partial++
		}
		if img.Pix[i] > a || img.Pix[i+1] > a || img.Pix[i+2] > a {
			invalid++
		}
	}
	return
}

// savePNG writes a picture where a person can look at it.
func savePNG(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "cairn-icons")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// aShortcut finds some shortcut of the Start menu, or "".
func aShortcut() string {
	for _, pattern := range []string{
		filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs\*.lnk`),
		filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs\*\*.lnk`),
		filepath.Join(os.Getenv("AppData"), `Microsoft\Windows\Start Menu\Programs\*\*.lnk`),
	} {
		if found, _ := filepath.Glob(pattern); len(found) > 0 {
			return found[0]
		}
	}
	return ""
}

func TestIcon(t *testing.T) {
	windir := os.Getenv("SystemRoot")
	cases := []struct {
		name, path string
		attr       fsys.Attr
		byType     bool
	}{
		{"folder", windir, fsys.AttrDir, false},
		{"folder-by-type", "x", fsys.AttrDir, true},
		{"notepad", filepath.Join(windir, "notepad.exe"), 0, false},
		{"txt", "x.txt", 0, true},
		{"pdf", "x.pdf", 0, true},
		{"zip", "x.zip", 0, true},
		{"exe-by-type", "x.exe", 0, true},
		{"no-extension", "x", 0, true},
	}
	if lnk := aShortcut(); lnk != "" {
		t.Logf("shortcut: %s", lnk)
		cases = append(cases, struct {
			name, path string
			attr       fsys.Attr
			byType     bool
		}{"shortcut", lnk, 0, false})
	}
	onWorker(t, func() {
		t.Logf("sizes: %v", iconSizes())
		for _, px := range []int{16, 32, 48, 256} {
			for _, c := range cases {
				img, err := Icon(c.path, c.attr, px, c.byType)
				if err != nil {
					t.Errorf("%s at %d: %v", c.name, px, err)
					continue
				}
				clear, partial, opaque, invalid := alphaOf(img)
				t.Logf("%-14s %3d: %v clear %d partial %d opaque %d", c.name, px, img.Bounds().Size(), clear, partial, opaque)
				if img.Bounds().Dx() == 0 || img.Bounds().Dx() != img.Bounds().Dy() {
					t.Errorf("%s at %d: size %v", c.name, px, img.Bounds().Size())
				}
				if opaque+partial == 0 {
					t.Errorf("%s at %d: nothing drawn", c.name, px)
				}
				if clear == 0 {
					t.Errorf("%s at %d: a full square, without alpha", c.name, px)
				}
				if invalid != 0 {
					t.Errorf("%s at %d: %d pixels are not premultiplied", c.name, px, invalid)
				}
				again, err := Icon(c.path, c.attr, px, c.byType)
				if err != nil || again != img {
					t.Errorf("%s at %d: another picture the second time (%v)", c.name, px, err)
				}
				savePNG(t, c.name+"-"+sizeName(px), img)
			}
		}
		// The type's icon and the file's are one picture where the file
		// has none of its own.
		byType, _ := Icon("x.dll", 0, 16, true)
		byFile, _ := Icon(filepath.Join(windir, `System32\kernel32.dll`), 0, 16, false)
		if byType == nil || byType != byFile {
			t.Errorf("a .dll by type is %p, kernel32.dll is %p", byType, byFile)
		}
	})
}

func sizeName(px int) string {
	return map[int]string{16: "small", 32: "large", 48: "extralarge", 256: "jumbo"}[px]
}

func TestIconSizeClass(t *testing.T) {
	sizes := iconSizes()
	for c, size := range sizes {
		if got := classFor(size); got != iconClass(c) {
			t.Errorf("classFor(%d) = %d, want %d", size, got, c)
		}
	}
	if got := classFor(1); got != iconSmall {
		t.Errorf("classFor(1) = %d", got)
	}
	if got := classFor(10000); got != iconJumbo {
		t.Errorf("classFor(10000) = %d", got)
	}
	if got := classFor(sizes[iconSmall] + 1); got != iconSmall {
		t.Errorf("a pixel above small is class %d", got)
	}
}

// TestIconConcurrent asks several workers for the same icons at once:
// they must agree on the pictures.
func TestIconConcurrent(t *testing.T) {
	exes, _ := filepath.Glob(filepath.Join(os.Getenv("SystemRoot"), `System32\*.exe`))
	if len(exes) > 60 {
		exes = exes[:60]
	}
	const workers = 4
	results := make([][]*image.RGBA, workers)
	var wg sync.WaitGroup
	for i := range workers {
		w := NewWorker()
		defer w.Close()
		wg.Add(1)
		w.Do(func() {
			defer wg.Done()
			for _, exe := range exes {
				img, _ := Icon(exe, 0, 32, false)
				results[i] = append(results[i], img)
			}
		})
	}
	wg.Wait()
	for i := 1; i < workers; i++ {
		for j := range exes {
			if results[i][j] == nil || results[i][j] != results[0][j] {
				t.Fatalf("workers 0 and %d differ on %s", i, exes[j])
			}
		}
	}
}

func TestOwnIcon(t *testing.T) {
	for name, want := range map[string]bool{
		"a.exe": true, "A.EXE": true, "b.lnk": true, "c.ico": true, "d.url": true, "e.cur": true,
		"f.ani": true, "g.scr": true, "h.msi": true, "i.msc": true, "j.cpl": true,
		"k.appref-ms": true, "l.library-ms": true,
		"m.txt": false, "exe": false, ".exe": false, "n.exe.txt": false, "": false,
	} {
		if got := OwnIcon(name, 0); got != want {
			t.Errorf("OwnIcon(%q) = %v", name, got)
		}
	}
	if OwnIcon("dir.exe", fsys.AttrDir) {
		t.Error("a directory named like a program")
	}
	if !OwnIcon("dir", fsys.AttrDir|fsys.AttrCustomIcon) {
		t.Error("a directory with an icon of its own")
	}
}

func TestTypeName(t *testing.T) {
	onWorker(t, func() {
		for _, ext := range []string{".txt", ".exe", ".zip", ".qqqzzz", ""} {
			name := TypeName(ext, false)
			t.Logf("%q: %q", ext, name)
			if name == "" {
				t.Errorf("no name for %q", ext)
			}
			if again := TypeName(ext, false); again != name {
				t.Errorf("%q is %q, then %q", ext, name, again)
			}
		}
		dir := TypeName("", true)
		t.Logf("directory: %q", dir)
		if dir == "" || dir == TypeName("", false) {
			t.Errorf("a directory is %q", dir)
		}
	})
}

// resetIcons forgets every picture, to time making them.
func resetIcons() {
	icons.mu.Lock()
	icons.byKey, icons.byType = nil, nil
	icons.mu.Unlock()
}

// TestIconTimes logs what icons cost, as the numbers to quote: run it
// with -v.
func TestIconTimes(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	exes, _ := filepath.Glob(filepath.Join(os.Getenv("SystemRoot"), `System32\*.exe`))
	if len(exes) > 200 {
		exes = exes[:200]
	}
	exts := []string{".txt", ".pdf", ".zip", ".png", ".jpg", ".mp3", ".mp4", ".docx", ".xlsx", ".html", ".go", ".json", ".xml", ".dll", ".ini", ".log", ".bat", ".js", ".css", ".md"}
	onWorker(t, func() {
		Icon("warm.up", 0, 16, true)
		for _, px := range []int{16, 32} {
			resetIcons()
			start := time.Now()
			for _, ext := range exts {
				Icon("x"+ext, 0, px, true)
			}
			t.Logf("%d px, by type, first time: %v a call over %d types", px, time.Since(start)/time.Duration(len(exts)), len(exts))

			const rounds = 1000
			start = time.Now()
			for range rounds {
				for _, ext := range exts {
					Icon("x"+ext, 0, px, true)
				}
			}
			t.Logf("%d px, by type, again: %v a call", px, time.Since(start)/time.Duration(rounds*len(exts)))

			if len(exes) == 0 {
				continue
			}
			resetIcons()
			start = time.Now()
			pictures := map[*image.RGBA]bool{}
			for _, exe := range exes {
				img, _ := Icon(exe, 0, px, false)
				pictures[img] = true
			}
			t.Logf("%d px, %d programs of System32, first time: %v a call, %d pictures", px, len(exes), time.Since(start)/time.Duration(len(exes)), len(pictures))
			start = time.Now()
			for _, exe := range exes {
				Icon(exe, 0, px, false)
			}
			t.Logf("%d px, the same programs again: %v a call", px, time.Since(start)/time.Duration(len(exes)))
		}
	})
}

func BenchmarkIconByTypeCached(b *testing.B) {
	onWorker(b, func() {
		Icon("x.txt", 0, 16, true)
		b.ResetTimer()
		for b.Loop() {
			Icon("x.txt", 0, 16, true)
		}
	})
}

// BenchmarkIconByTypeNewType is a type not asked for before whose icon
// is one already made: the system is asked, no picture is made.
func BenchmarkIconByTypeNewType(b *testing.B) {
	onWorker(b, func() {
		Icon("x.txt", 0, 16, true)
		b.ResetTimer()
		for b.Loop() {
			icons.mu.Lock()
			icons.byType = nil
			icons.mu.Unlock()
			Icon("x.txt", 0, 16, true)
		}
	})
}

// BenchmarkIconByTypeUncached makes the picture each time.
func BenchmarkIconByTypeUncached(b *testing.B) {
	onWorker(b, func() {
		Icon("x.txt", 0, 16, true)
		b.ResetTimer()
		for b.Loop() {
			resetIcons()
			Icon("x.txt", 0, 16, true)
		}
	})
}

func BenchmarkIconOfProgramCached(b *testing.B) {
	notepad := filepath.Join(os.Getenv("SystemRoot"), "notepad.exe")
	onWorker(b, func() {
		Icon(notepad, 0, 16, false)
		b.ResetTimer()
		for b.Loop() {
			Icon(notepad, 0, 16, false)
		}
	})
}

func TestCropCorner(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := range 48 {
		for x := range 48 {
			img.Pix[y*img.Stride+x*4+3] = 255
		}
	}
	cropped := cropCorner(img, 48)
	if cropped.Bounds() != image.Rect(0, 0, 48, 48) {
		t.Fatalf("cropped to %v", cropped.Bounds())
	}
	if _, _, opaque, _ := alphaOf(cropped); opaque != 48*48 {
		t.Errorf("%d pixels kept", opaque)
	}
	// One pixel outside the corner, to the right or below, and the
	// picture is a whole one.
	for _, at := range []image.Point{{48, 10}, {10, 48}, {255, 255}} {
		whole := image.NewRGBA(img.Rect)
		copy(whole.Pix, img.Pix)
		whole.Pix[at.Y*whole.Stride+at.X*4+3] = 1
		if cropCorner(whole, 48) != whole {
			t.Errorf("cropped with a pixel at %v", at)
		}
	}
}

// madeIcon makes an icon of 16 pixels from its rows of blue, green, red
// and alpha, and a mask that hides its right half.
func madeIcon(t *testing.T, bgra []byte) uintptr {
	t.Helper()
	createBitmap := gdi32.NewProc("CreateBitmap")
	createIconIndirect := user32.NewProc("CreateIconIndirect")
	mask := make([]byte, 16*2)
	for y := range 16 {
		mask[y*2+1] = 0xFF
	}
	color, _, _ := createBitmap.Call(16, 16, 1, 32, uintptr(unsafe.Pointer(&bgra[0])))
	maskBitmap, _, _ := createBitmap.Call(16, 16, 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	defer procDeleteObject.Call(color)
	defer procDeleteObject.Call(maskBitmap)
	info := iconInfo{isIcon: 1, mask: maskBitmap, color: color}
	icon, _, err := createIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	if icon == 0 {
		t.Fatal(err)
	}
	return icon
}

// TestIconPixels reads icons made of known pixels: one without alpha,
// whose shape is its mask, and one with, whose colors are to be
// multiplied by it.
func TestIconPixels(t *testing.T) {
	at := func(img *image.RGBA, x, y int) [4]byte {
		return [4]byte(img.Pix[y*img.Stride+x*4:])
	}
	pixels := func(b, g, r, a byte) []byte {
		out := make([]byte, 16*16*4)
		for i := 0; i < len(out); i += 4 {
			out[i], out[i+1], out[i+2], out[i+3] = b, g, r, a
		}
		return out
	}

	old := madeIcon(t, pixels(10, 20, 200, 0))
	defer procDestroyIcon.Call(old)
	img, err := iconPixels(old)
	if err != nil {
		t.Fatal(err)
	}
	if got := at(img, 3, 5); got != [4]byte{200, 20, 10, 255} {
		t.Errorf("without alpha, where the mask shows: %v", got)
	}
	if got := at(img, 12, 5); got != [4]byte{} {
		t.Errorf("without alpha, where the mask hides: %v", got)
	}

	withAlpha := madeIcon(t, pixels(10, 100, 200, 128))
	defer procDestroyIcon.Call(withAlpha)
	if img, err = iconPixels(withAlpha); err != nil {
		t.Fatal(err)
	}
	// 200, 100 and 10 at an alpha of 128 of 255.
	if got := at(img, 3, 5); got != [4]byte{100, 50, 5, 128} {
		t.Errorf("with alpha: %v", got)
	}
}
