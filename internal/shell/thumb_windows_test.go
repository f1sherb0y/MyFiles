package shell

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestThumbnail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "red.png")
	src := image.NewRGBA(image.Rect(0, 0, 300, 200))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+3] = 220, 255
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var img *image.RGBA
	onWorker(t, func() { img, err = Thumbnail(path, 96) })
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != 96 || b.Dy() != 64 {
		t.Errorf("the thumbnail of a 300x200 picture is %dx%d, want 96x64", b.Dx(), b.Dy())
	}
	if c := img.RGBAAt(b.Dx()/2, b.Dy()/2); c != (color.RGBA{220, 0, 0, 255}) {
		t.Errorf("its middle is %v, want red", c)
	}

	// A file of a type without pictures has none.
	text := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(text, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	onWorker(t, func() { img, err = Thumbnail(text, 96) })
	if err == nil {
		t.Errorf("a text file has a thumbnail of %v", img.Bounds())
	}
}
