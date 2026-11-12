package app

import (
	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
)

// chooser is the question which program opens a file, asked in the
// window: the system has a dialog for it, which a program cannot count
// on showing (see shell.Open), and which sets what opens the type for
// the whole system, as no program may.
type chooser struct {
	open bool
	path string
	// ext is the file's extension, "" for none: the type a choice may be
	// kept for.
	ext string
	// apps are the programs to choose from, with their icons, nil for a
	// program without one; known is set once the system listed them, and
	// all once every program that opens files is listed, or asked for,
	// and not only the ones recommended for the type.
	apps  []shell.App
	icons []*ui.Bitmap
	known bool
	all   bool
	// always keeps the choice for the files of the type.
	always bool
	// focus is the program that takes the keyboard in the next frame,
	// the first of those just listed, or -1.
	focus int
}

const (
	chooserWidth = 340
	chooserRow   = 30
	chooserIcon  = 20
)

// openFile opens a file as a double click does: with the program the
// user chose here for its type, else with the one the system has for it,
// and where no program is set to open it, after asking which.
func (w *Window) openFile(path, dir string) {
	a := w.app
	owner := w.handle()
	ext := fsys.ExtOf(fsys.Base(path))
	id := a.set.OpenWith[ext]
	go func() {
		if id != "" {
			err := a.desk.openIn(owner, path, shell.App{ID: id})
			if err != shell.ErrChoose {
				w.report(err)
				return
			}
			// The program chosen is gone: the choice is forgotten.
			w.update(func() {
				if a.set.OpenWith[ext] == id {
					delete(a.set.OpenWith, ext)
					a.saveSoon()
				}
			})
		}
		err := a.desk.open(owner, path, dir)
		if err == shell.ErrChoose {
			w.update(func() { w.choose(path, true) })
			return
		}
		w.report(err)
	}()
}

// choose asks which program opens a file; always is whether the choice
// is to be kept for the file's type unless the user says otherwise.
func (w *Window) choose(path string, always bool) {
	p := &chooser{open: true, path: path, ext: fsys.ExtOf(fsys.Base(path)), always: always, focus: -1}
	w.pick = p
	w.chooserLoad(p, false)
	w.invalidate()
}

// chooserLoad lists the programs to choose from, off the main thread:
// the ones recommended for the file, or all, as also where none is
// recommended.
func (w *Window) chooserLoad(p *chooser, all bool) {
	a := w.app
	p.all = all
	path := p.path
	go func() {
		apps := a.desk.apps(path, all)
		if len(apps) == 0 && !all {
			all = true
			apps = a.desk.apps(path, true)
		}
		w.update(func() {
			if w.pick != p {
				return
			}
			icons := make([]*ui.Bitmap, len(apps))
			for i, app := range apps {
				if app.Icon != nil {
					icons[i] = ui.NewBitmap(app.Icon)
				}
			}
			// The keyboard goes to the first program, or to the first of
			// those More Apps added.
			p.focus = 0
			if len(apps) > len(p.apps) && len(p.apps) > 0 {
				p.focus = len(p.apps)
			}
			p.apps, p.icons, p.known, p.all = apps, icons, true, all
		})
	}()
}

// chooserDialog builds the question which program opens a file, while
// it is asked.
func (w *Window) chooserDialog(c *ui.Context) {
	p := w.pick
	if p == nil {
		return
	}
	a := w.app
	th := c.Theme()
	kept := a.set.OpenWith[p.ext]
	chosen := -1
	ui.Modal(c.Key("chooser"), &p.open, func() {
		ui.Text(c, locales.ChooserTitle(fsys.Base(p.path))).FontWeight(600).Width(chooserWidth).Wrap()
		var rows []ui.Element
		ui.Scroll(c.Key("chooser-apps")).Width(chooserWidth).MaxHeight(chooserRow * 9.5).Children(func() {
			ui.Column(c).AlignItems(ui.Stretch).Children(func() {
				switch {
				case len(p.apps) > 0:
				case !p.known:
					ui.Text(c, locales.MenuOpenWithLooking()).TextColor(th.TextMuted).Padding(6, 8)
				default:
					ui.Text(c, locales.ChooserNone()).Wrap().TextColor(th.TextMuted).Padding(6, 8)
				}
				for i, app := range p.apps {
					row := chooserItem(c, app.ID, p.icons[i], app.Name, app.ID == kept)
					if row.Clicked() {
						chosen = i
					}
					rows = append(rows, row)
				}
				if p.known && !p.all {
					more := chooserItem(c, "::more", nil, locales.ChooserMore(), false)
					if more.Clicked() {
						w.chooserLoad(p, true)
					}
					rows = append(rows, more)
				}
			})
		})
		if p.focus >= 0 && p.focus < len(rows) {
			rows[p.focus].Focus()
			rows[p.focus].ScrollIntoView()
		}
		p.focus = -1
		// The arrows go from one program to the next, as in a menu.
		step := 0
		if c.Shortcut(0, ui.KeyDown) {
			step = 1
		} else if c.Shortcut(0, ui.KeyUp) {
			step = -1
		}
		if step != 0 && len(rows) > 0 {
			at := -1
			for i, row := range rows {
				if row.Focused() {
					at = i
				}
			}
			at = min(max(at+step, 0), len(rows)-1)
			rows[at].Focus()
			rows[at].ScrollIntoView()
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if p.ext != "" {
				ui.Checkbox(c, &p.always, locales.ChooserAlways(p.ext))
			}
			ui.Spacer(c)
			if ui.Button(c, locales.Cancel()).Clicked() {
				p.open = false
			}
		})
	})
	if chosen >= 0 {
		app := p.apps[chosen]
		p.open = false
		if p.always && p.ext != "" {
			if a.set.OpenWith == nil {
				a.set.OpenWith = map[string]string{}
			}
			a.set.OpenWith[p.ext] = app.ID
			a.saveSoon()
		}
		w.openIn(p.path, app)
	}
	if !p.open {
		w.pick = nil
		if t := w.current(); t != nil {
			t.wantFocus = true
		}
	}
}

// chooserItem is a row of the programs to choose from: an icon and a
// name, with a mark on the one kept for the type, which lights up under
// the pointer and with the focus.
func chooserItem(c *ui.Context, key any, icon *ui.Bitmap, name string, kept bool) ui.Element {
	th := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Height(chooserRow).Shrink(0).Padding(0, 8).Gap(8).Radius(4).
		Justify(ui.Start).AlignItems(ui.Center).Label(name)
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		switch {
		case b.Pressed():
			p.Fill(r, th.SurfacePressed, 4)
		case b.Hovered(), b.Focused():
			p.Fill(r, th.SurfaceHover, 4)
		}
	})
	b.Children(func() {
		if icon != nil {
			ui.Image(c, icon).Size(chooserIcon, chooserIcon).Shrink(0)
		} else {
			ui.Box(c).Size(chooserIcon, chooserIcon).Shrink(0)
		}
		ui.Text(c, name).SingleLine().Grow(1).MinWidth(0)
		if kept {
			ui.Icon(c, icoCheck).FontSize(14).TextColor(th.TextMuted).Shrink(0)
		}
	})
	return b
}
