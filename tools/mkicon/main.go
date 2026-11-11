// Mkicon draws Cairn's icon, a cairn of three stones, and writes it as a
// PNG: go run ./tools/mkicon internal/app/icon.png
package main

import (
	"image/png"
	"log"
	"os"

	"github.com/egoist/mygo/ui"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: mkicon <file.png>")
	}
	img := ui.Render(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			// A rounded tile, and three stones on it, the largest below.
			p.FillGradient(ui.Rect{X: 8, Y: 8, W: 240, H: 240},
				ui.LinearGradient{From: ui.Hex("#2b3a55"), To: ui.Hex("#141c2b"), Angle: 180}, 52)
			stones := []struct {
				w, h, y float32
				color   string
			}{
				{150, 58, 150, "#f2b65a"},
				{112, 50, 104, "#f6c877"},
				{72, 42, 66, "#fadc9c"},
			}
			for _, s := range stones {
				p.Fill(ui.Rect{X: 128 - s.w/2, Y: s.y, W: s.w, H: s.h}, ui.Hex(s.color), s.h/2)
			}
		})
	}, 256, 256, 1)
	f, err := os.Create(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}
