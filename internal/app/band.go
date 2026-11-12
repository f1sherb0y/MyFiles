package app

import (
	"github.com/egoist/mygo/ui"
)

// band is a press on the empty room of a folder's view: it lets go of
// what was chosen, and dragged it draws a rectangle that chooses the
// entries it touches, as in the system's file manager.
type band struct {
	// on is set while the press lasts, and active once the pointer went
	// far enough for a rectangle.
	on, active bool
	// The rectangle goes from where the press began to where the pointer
	// is, in the view's box. Its start moves with the entries as the view
	// scrolls.
	x0, y0, x1, y1 float32
	// ox and oy are where the view's box is in the window.
	ox, oy float32
	// base are the entries chosen before, which a press with Ctrl or
	// Shift keeps; hit tells which the rectangle touched, for those in
	// view when it last moved.
	base, hit map[string]bool
	// ref is an entry in view and refY where it was, to tell how far the
	// view scrolled since; refSeen is set when the frame built it.
	ref     string
	refY    float32
	refSeen bool
}

// bandStart is how far the pointer goes before a press on empty room
// draws a rectangle, in DIPs.
const bandStart = 4

// bandOn has a view's element take the presses on its empty room: the
// entries are elements of their own in it, with their own presses.
func (w *Window) bandOn(c *ui.Context, t *Tab, area ui.Element) {
	b := &t.band
	if origin := area.Bounds(); origin.W > 0 {
		b.ox, b.oy = origin.X, origin.Y
	}
	if b.on && !b.refSeen {
		// Scrolled out of view: another entry tells how far it scrolls.
		b.ref = ""
	}
	b.refSeen = false
	area.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			if ev.Button != 0 {
				return false
			}
			// The presses on the entries come here too: they are the
			// entries' own.
			for _, r := range t.entryBoxes {
				if ev.X >= r.X-b.ox && ev.X < r.X+r.W-b.ox && ev.Y >= r.Y-b.oy && ev.Y < r.Y+r.H-b.oy {
					return false
				}
			}
			*b = band{on: true, x0: ev.X, y0: ev.Y, x1: ev.X, y1: ev.Y, ox: b.ox, oy: b.oy,
				base: map[string]bool{}, hit: map[string]bool{}}
			if ev.Mods&(ui.Ctrl|ui.Shift) != 0 {
				// Added to what is chosen.
				for name := range t.sel.All() {
					b.base[name] = true
				}
			} else if t.sel.Len() > 0 {
				t.sel.Clear()
				t.cursor = -1
				t.selStale = true
			}
			w.invalidate()
		case ui.InputPointerMove:
			if !b.on {
				return false
			}
			if ev.Button != 0 {
				// The pointer moves with no button held: the press ended
				// where its release did not come here, as when the system
				// took it for a drag of files, or it was let go outside
				// the window. The rectangle ends with it.
				b.on, b.active = false, false
				w.invalidate()
				return false
			}
			b.x1, b.y1 = ev.X, ev.Y
			if dx, dy := b.x1-b.x0, b.y1-b.y0; dx > bandStart || dx < -bandStart || dy > bandStart || dy < -bandStart {
				b.active = true
			}
			w.invalidate()
		case ui.InputPointerUp:
			if b.on {
				b.on, b.active = false, false
				w.invalidate()
			}
		}
		// The view does with the press what it does without: it takes
		// the keyboard.
		return false
	})
	if !b.active {
		return
	}
	th := c.Theme()
	area.DrawOver(func(p *ui.Painter, r ui.Rect) {
		x0, y0 := max(min(b.x0, b.x1), 0), max(min(b.y0, b.y1), 0)
		x1, y1 := min(max(b.x0, b.x1), r.W), min(max(b.y0, b.y1), r.H)
		if x1 <= x0 || y1 <= y0 {
			return
		}
		box := ui.Rect{X: r.X + x0, Y: r.Y + y0, W: x1 - x0, H: y1 - y0}
		p.Fill(box, th.Accent.Alpha(0.16), 0)
		p.Stroke(box, th.Accent.Alpha(0.8), 0, 1)
	})
}

// bandEntry chooses an entry the rectangle touches, and lets go of one it
// touches no more, as the view builds the entry's element.
func (w *Window) bandEntry(t *Tab, el ui.Element, name string) {
	b := &t.band
	r := el.Bounds()
	if r.W <= 0 || r.H <= 0 {
		return // new in this frame: placed in the next
	}
	t.entryBoxes = append(t.entryBoxes, r)
	if !b.active {
		return
	}
	x, y := r.X-b.ox, r.Y-b.oy
	switch b.ref {
	case "":
		b.ref, b.refY, b.refSeen = name, y, true
	case name:
		// The start of the rectangle stays on the entries it began on.
		if dy := y - b.refY; dy != 0 {
			b.y0 += dy
			b.refY = y
		}
		b.refSeen = true
	}
	hit := x < max(b.x0, b.x1) && x+r.W > min(b.x0, b.x1) && y < max(b.y0, b.y1) && y+r.H > min(b.y0, b.y1)
	if hit == b.hit[name] {
		return
	}
	b.hit[name] = hit
	switch {
	case hit:
		t.sel.Add(name)
	case !b.base[name]:
		t.sel.Remove(name)
	}
	t.selStale = true
	// The entries built before this one show the choice in the next frame.
	w.invalidate()
}
