package app

import (
	"image"
	"slices"
	"testing"

	"myfiles/internal/workspace"
)

// TestNoFrameOfTheOldSize checks that when the room a folder has changes,
// as a pane splits, the window is resized or the sidebar goes, no frame
// shows it laid out for the room it had: every frame drawn is the picture
// that stays. The view builds with the sizes of the frame before (how
// many columns fit, which parts of the address do), and the frame builds
// again when the layout gives others.
func TestNoFrameOfTheOldSize(t *testing.T) {
	entries := []string{"a/", "b/", "c/", "d/", "e/"}
	for i := range 60 {
		entries = append(entries, "file"+string(rune('a'+i%26))+string(rune('a'+i/26))+".txt")
	}
	actions := []struct {
		name string
		do   func(h *harness)
	}{
		{"split right", func(h *harness) { h.w.splitCurrent(workspace.ZoneRight) }},
		{"split down", func(h *harness) { h.w.splitCurrent(workspace.ZoneBottom) }},
		{"sidebar off", func(h *harness) { h.a.set.Sidebar = false; h.w.invalidate() }},
		{"window resized", func(h *harness) { h.tt.SetSize(700, 600) }},
		{"window widened", func(h *harness) { h.tt.SetSize(1300, 700) }},
		{"new tab", func(h *harness) { h.w.newTab(h.w.layout.Focus, h.root, "") }},
		{"settings tab", func(h *harness) { h.w.openSettings() }},
	}
	for mode, spec := range viewSpecs {
		for _, action := range actions {
			t.Run(spec.id+"/"+action.name, func(t *testing.T) {
				h := newHarness(t, entries...)
				h.a.set.Sidebar = true
				h.w.setView(h.w.current(), viewMode(mode))
				h.settle()
				var frames []*image.RGBA
				h.presented = func() {
					img := h.tt.Image()
					frames = append(frames, &image.RGBA{Pix: slices.Clone(img.Pix), Stride: img.Stride, Rect: img.Rect})
				}
				action.do(h)
				h.settle()
				h.presented = nil
				if len(frames) == 0 {
					t.Fatal("no frame drawn")
				}
				last := frames[len(frames)-1]
				for i, f := range frames {
					if f.Rect != last.Rect {
						continue // drawn before the window took its new size
					}
					// The status bars tell how long the folder took once
					// it shows: the window's last rows of pixels.
					within := f.Rect
					within.Max.Y -= statusHeight
					if box := differ(f, last, within); !box.Empty() {
						t.Errorf("frame %d of %d differs from the one that stays within %v", i+1, len(frames), box)
					}
				}
			})
		}
	}
}
