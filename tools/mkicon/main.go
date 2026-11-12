// Mkicon draws MyFiles's icon, a folder on a dark tile, and writes it as a
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
			// A rounded tile, and a folder on it: its back with the tab,
			// a sheet of paper in it, and its front over both.
			p.FillGradient(ui.Rect{X: 8, Y: 8, W: 240, H: 240},
				ui.LinearGradient{From: ui.Hex("#2b3a55"), To: ui.Hex("#141c2b"), Angle: 180}, 52)
			back := ui.Hex("#e09a2c")
			p.FillRadii(ui.Rect{X: 48, Y: 70, W: 76, H: 40}, [4]float32{14, 14, 0, 0}, back)
			p.FillRadii(ui.Rect{X: 48, Y: 92, W: 160, H: 100}, [4]float32{0, 14, 14, 14}, back)
			p.Fill(ui.Rect{X: 66, Y: 98, W: 124, H: 60}, ui.Hex("#f4f6fa"), 6)
			p.FillGradient(ui.Rect{X: 40, Y: 118, W: 176, H: 80},
				ui.LinearGradient{From: ui.Hex("#fbd475"), To: ui.Hex("#f2b040"), Angle: 180}, 16)
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
