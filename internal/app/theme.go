package app

import (
	"github.com/egoist/mygo/ui"
)

// The heights of a pane's bars, in DIPs. The interface is compact on
// purpose: the rows of files get the room.
const (
	stripHeight  = 30
	barHeight    = 28
	statusHeight = 20
	controlSize  = 22
)

// themeCache keeps the themes made of the system's appearance and the
// settings, until one of those changes.
type themeCache struct {
	made bool
	// base is the system's theme the others were made of.
	base                ui.Theme
	fontSize, rowHeight float32
	// window is the theme of the window, and table that of the rows of
	// files, which differs in its spacing and in how a chosen row shows.
	window, table ui.Theme
}

// theme returns the window's theme, and makes the table's with it.
func (a *App) theme(c *ui.Context) *ui.Theme {
	tc := &a.themes
	// As the view starts building, the frame's theme is the system's:
	// light or dark, in the user's accent color, with the contrast and
	// the text size the desktop asks for.
	base := c.Theme()
	if tc.made && tc.base == *base && tc.fontSize == a.set.FontSize && tc.rowHeight == a.set.RowHeight {
		return &tc.window
	}
	tc.made, tc.base, tc.fontSize, tc.rowHeight = true, *base, a.set.FontSize, a.set.RowHeight

	t := *base
	t.FontSize = a.set.FontSize
	t.Spacing = 3
	t.Radius = 4
	t.ScrollbarWidth = 8
	tc.window = t

	// A table's rows are as high as its cells: the line of text and
	// their padding, one and a half units of spacing above and below.
	// The unit that gives the row height asked for.
	line := t.FontSize * 1.34
	tt := t
	tt.Spacing = max(min((a.set.RowHeight-line)/3, a.set.RowHeight/8), 0.5)
	// A chosen row is tinted, with its text as it was: every column
	// stays readable, the muted ones too.
	tt.Accent = t.Background.Mix(t.Accent, 0.26)
	tt.AccentText = t.Text
	tt.InactiveAccent = t.Background.Mix(t.Text, 0.11)
	tc.table = tt
	return &tc.window
}

func svg(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

// The icons of the interface's own buttons, drawn in the color of the
// text. The icons of files are the system's.
var (
	icoBack        = svg(`<path d="M9.5 3.5L5 8l4.5 4.5"/>`)
	icoForward     = svg(`<path d="M6.5 3.5L11 8l-4.5 4.5"/>`)
	icoUp          = svg(`<path d="M8 13V3.5M3.5 8L8 3.5 12.5 8"/>`)
	icoRefresh     = svg(`<path d="M13 8a5 5 0 1 1-1.5-3.55M13 2.5v3h-3"/>`)
	icoUpdate      = svg(`<path d="M8 2.5v7.5M4.75 6.75L8 10l3.25-3.25M3 13h10"/>`)
	icoPlus        = svg(`<path d="M8 3.5v9M3.5 8h9"/>`)
	icoClose       = svg(`<path d="M4.5 4.5l7 7M11.5 4.5l-7 7"/>`)
	icoChevron     = svg(`<path d="M6 4l4 4-4 4"/>`)
	icoChevronDown = svg(`<path d="M4 6l4 4 4-4"/>`)
	icoSettings    = svg(`<path d="M2.5 5h5M11 5h2.5M2.5 11h2.5M8.5 11h5"/><circle cx="9.25" cy="5" r="1.75"/><circle cx="6.75" cy="11" r="1.75"/>`)
	icoSidebar     = svg(`<rect x="2.5" y="3" width="11" height="10" rx="1.5"/><path d="M6.25 3v10"/>`)
	icoComputer    = svg(`<rect x="2.5" y="3" width="11" height="7.5" rx="1"/><path d="M6 13h4M8 10.5V13"/>`)
	icoView        = svg(`<rect x="2.75" y="2.75" width="4.25" height="4.25" rx="0.8"/><rect x="9" y="2.75" width="4.25" height="4.25" rx="0.8"/><rect x="2.75" y="9" width="4.25" height="4.25" rx="0.8"/><rect x="9" y="9" width="4.25" height="4.25" rx="0.8"/>`)
	icoMore        = svg(`<circle cx="4" cy="8" r="0.6"/><circle cx="8" cy="8" r="0.6"/><circle cx="12" cy="8" r="0.6"/>`)
)

// iconButton is a small square button showing an icon, which lights up
// under the pointer.
func iconButton(c *ui.Context, key any, icon *ui.SVG, tip string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Size(controlSize+2, controlSize).Shrink(0).Radius(4).Center().Tooltip(tip).Label(tip).TabStop(false)
	// The hover is painted, not built: the pointer crossing the button
	// draws a frame without running the view.
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		switch {
		case b.IsDisabled():
		case b.Pressed():
			p.Fill(r, t.SurfacePressed, 4)
		case b.Hovered():
			p.Fill(r, t.SurfaceHover, 4)
		}
	})
	b.Children(func() { ui.Icon(c, icon).FontSize(15) })
	return b
}
