package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"cairn/internal/shell"
)

// saveSoon writes the settings, and the main window's session, a moment
// from now: once for all that changes meanwhile.
func (a *App) saveSoon() {
	a.saveMu.Lock()
	pending := a.saving
	a.saving = true
	a.saveMu.Unlock()
	if pending || a.ephemeral {
		return
	}
	time.AfterFunc(800*time.Millisecond, func() {
		a.desk.post(func() {
			a.saveMu.Lock()
			a.saving = false
			a.saveMu.Unlock()
			_ = a.set.Save()
			for _, w := range a.windows {
				w.save()
			}
		})
	})
}

// openSettings shows the settings, with what the system has the program
// registered for, read off the main thread.
func (w *Window) openSettings() {
	w.settings = true
	a := w.app
	go func() {
		d := shell.DefaultFileManager(a.exe)
		w.update(func() { a.registered = d })
	}()
}

// register makes the program the system's file manager, or gives that
// back, as the settings now say.
func (w *Window) register(d shell.Default) {
	a := w.app
	a.registered = d
	go func() {
		err := shell.SetDefaultFileManager(a.exe, d)
		now := shell.DefaultFileManager(a.exe)
		w.update(func() {
			a.registered = now
			if err != nil {
				w.say(err.Error())
			}
		})
	}()
}

// dialogs builds what shows over the window: the settings, and the
// question asked before something that cannot be undone.
func (w *Window) dialogs(c *ui.Context) {
	if q := w.ask; q != nil {
		switch ui.AlertDialog(c, &q.open, q.title, q.message, "Cancel", q.confirm) {
		case 1:
			q.yes()
		}
		if !q.open {
			w.ask = nil
			if t := w.current(); t != nil {
				t.wantFocus = true
			}
		}
	}
	if !w.settings {
		return
	}
	a := w.app
	th := c.Theme()
	set := &a.set
	changed := false
	ui.Modal(c, &w.settings, func() {
		ui.Column(c).Width(460).Gap(10).Children(func() {
			ui.Text(c, "Settings").FontSize(th.FontSize + 3).FontWeight(600)

			section(c, "Default file manager")
			reg := a.registered
			if ui.Checkbox(c, &reg.Folders, "Open folders with Cairn").Changed() {
				w.register(reg)
			}
			hint(c, "Folders opened from the desktop and from other programs open here instead of in File Explorer. Programs that ask File Explorer itself to show a file still get File Explorer.")
			if ui.Checkbox(c, &reg.Shortcut, "Open Cairn with Win+E").Changed() {
				w.register(reg)
			}
			hint(c, "Both are settings of your user account alone, and turning them off puts back what was there.")

			section(c, "Files")
			if ui.Checkbox(c, &set.ShowHidden, "Show hidden files").Changed() {
				w.setHidden(set.ShowHidden)
			}
			if ui.Checkbox(c, &set.DirsFirst, "List folders before files").Changed() {
				changed = true
				for _, win := range a.windows {
					for _, t := range win.tabs {
						if t.Path != "" {
							t.review(set.ShowHidden, set.DirsFirst)
						}
					}
				}
			}
			changed = ui.Checkbox(c, &set.ConfirmTrash, "Ask before moving files to the Recycle Bin").Changed() || changed
			changed = ui.Checkbox(c, &set.RestoreTabs, "Reopen the last session's tabs at startup").Changed() || changed

			section(c, "Appearance")
			option(c, "Theme", func() {
				themes := []string{"system", "light", "dark"}
				at := 0
				for i, name := range themes {
					if set.Theme == name {
						at = i
					}
				}
				if ui.Segmented(c, &at, "System", "Light", "Dark").Changed() {
					set.Theme = themes[at]
					changed = true
					a.desk.appearance(set.Theme)
				}
			})
			option(c, "Rows", func() {
				heights := []float32{20, 22, 26, 30}
				at := 1
				for i, h := range heights {
					if set.RowHeight == h {
						at = i
					}
				}
				if ui.Segmented(c, &at, "Tight", "Compact", "Regular", "Roomy").Changed() {
					set.RowHeight = heights[at]
					changed = true
				}
			})
			option(c, "Text size", func() {
				sizes := []float32{11, 12, 13, 14}
				at := 1
				for i, s := range sizes {
					if set.FontSize == s {
						at = i
					}
				}
				if ui.Segmented(c, &at, "11", "12", "13", "14").Changed() {
					set.FontSize = sizes[at]
					changed = true
				}
			})

			ui.Row(c).Justify(ui.End).MarginY(4).Children(func() {
				if ui.PrimaryButton(c, "Done").Clicked() {
					w.settings = false
				}
			})
		})
	})
	if changed {
		a.saveSoon()
		a.invalidate()
	}
	if !w.settings {
		if t := w.current(); t != nil {
			t.wantFocus = true
		}
	}
}

func section(c *ui.Context, title string) {
	ui.Text(c, title).FontWeight(600).TextColor(c.Theme().TextMuted).MarginY(4)
}

func hint(c *ui.Context, text string) {
	th := c.Theme()
	ui.Text(c, text).Wrap().TextColor(th.TextMuted).FontSize(th.FontSize-1).Margin(-6, 0, 0, 24)
}

func option(c *ui.Context, label string, control func()) {
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, label).Width(90)
		control()
	})
}
