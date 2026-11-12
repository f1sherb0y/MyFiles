package app

import (
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/config"
	"myfiles/internal/shell"
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
			if a.side.recentDirty {
				a.side.recentDirty = false
				_ = config.SaveRecent(a.side.recents())
			}
			for _, w := range a.windows {
				w.save()
			}
		})
	})
}

// settingsPath stands for the settings where a session keeps the folders
// of its tabs: no folder has it for a path.
const settingsPath = "::settings"

// openSettings shows the settings: in a tab, the one the window has for
// them or a new one in the pane with the focus, with what the system has
// the program registered for, read off the main thread.
func (w *Window) openSettings() {
	if w.building {
		w.defer_(w.openSettings)
		return
	}
	a := w.app
	shown := false
	for id, t := range w.tabs {
		if t.settings {
			w.show(id)
			shown = true
			break
		}
	}
	if p := w.layout.Focused(); !shown && p != nil {
		t := &Tab{ID: a.nextTab, cursor: -1, settings: true}
		a.nextTab++
		w.tabs[t.ID] = t
		at := -1
		if len(p.Tabs) > 0 {
			at = p.Active + 1
		}
		w.layout.AddTab(p.ID, t.ID, at)
		w.changed()
	}
	w.readSettings()
}

// readSettings reads what the settings show that is kept elsewhere: the
// language chosen, and what the system has the program registered for,
// off the main thread.
func (w *Window) readSettings() {
	a := w.app
	a.settingsRead = true
	a.lang, a.langs = a.desk.chosenLocale(), a.desk.languages()
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

// dialogs builds what shows over the window: the question asked before
// something that cannot be undone, and the one which program opens a
// file.
func (w *Window) dialogs(c *ui.Context) {
	w.chooserDialog(c)
	if q := w.ask; q != nil {
		switch ui.AlertDialog(c, &q.open, q.title, q.message, locales.Cancel(), q.confirm) {
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
}

// settingsPage builds the settings, as the tab that shows them does in
// place of a folder.
func (w *Window) settingsPage(c *ui.Context) {
	a := w.app
	th := c.Theme()
	set := &a.set
	changed := false
	if !a.settingsRead {
		// A settings tab the session brought back.
		w.readSettings()
	}
	ui.Scroll(c.Key("settings")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).MaxWidth(560).Gap(10).Padding(20, 24).Children(func() {
			ui.Text(c, locales.Settings()).FontSize(th.FontSize + 5).FontWeight(600)

			section(c, locales.SettingsDefault())
			reg := a.registered
			if ui.Checkbox(c, &reg.Folders, locales.SettingsOpenFolders()).Changed() {
				w.register(reg)
			}
			hint(c, locales.SettingsOpenFoldersHint())
			if ui.Checkbox(c, &reg.Shortcut, locales.SettingsWinE()).Changed() {
				w.register(reg)
			}
			hint(c, locales.SettingsDefaultHint())

			section(c, locales.SettingsFiles())
			if ui.Checkbox(c, &set.ShowHidden, locales.SettingsShowHidden()).Changed() {
				w.setHidden(set.ShowHidden)
			}
			if ui.Checkbox(c, &set.DirsFirst, locales.SettingsDirsFirst()).Changed() {
				changed = true
				for _, win := range a.windows {
					for _, t := range win.tabs {
						if t.Path != "" {
							t.review(set.ShowHidden, set.DirsFirst)
						}
					}
				}
			}
			changed = ui.Checkbox(c, &set.ConfirmTrash, locales.SettingsConfirmTrash()).Changed() || changed
			changed = ui.Checkbox(c, &set.RestoreTabs, locales.SettingsRestoreTabs()).Changed() || changed

			section(c, locales.SettingsUpdates())
			if ui.Checkbox(c, &set.AutoUpdate, locales.SettingsAutoUpdate()).Changed() {
				changed = true
				if set.AutoUpdate {
					a.checkUpdate()
				}
			}
			hint(c, locales.SettingsVersion(Version))

			section(c, locales.SettingsAppearance())
			option(c, locales.SettingsLanguage(), func() {
				// The system's, then each language named in itself.
				labels, at := []string{locales.SettingsSystem()}, 0
				for i, tag := range a.langs {
					labels = append(labels, i18n.Autonym(tag))
					if tag == a.lang {
						at = i + 1
					}
				}
				if ui.Segmented(c, &at, labels...).Changed() {
					a.lang = ""
					if at > 0 {
						a.lang = a.langs[at-1]
					}
					a.desk.setLocale(a.lang)
					a.invalidate()
				}
			})
			option(c, locales.SettingsTheme(), func() {
				themes := []string{"system", "light", "dark"}
				at := 0
				for i, name := range themes {
					if set.Theme == name {
						at = i
					}
				}
				if ui.Segmented(c, &at, locales.SettingsSystem(), locales.SettingsLight(), locales.SettingsDark()).Changed() {
					set.Theme = themes[at]
					changed = true
					a.desk.appearance(set.Theme)
				}
			})
			option(c, locales.SettingsRows(), func() {
				heights := []float32{20, 22, 26, 30}
				at := 1
				for i, h := range heights {
					if set.RowHeight == h {
						at = i
					}
				}
				if ui.Segmented(c, &at, locales.SettingsRowsTight(), locales.SettingsRowsCompact(), locales.SettingsRowsRegular(), locales.SettingsRowsRoomy()).Changed() {
					set.RowHeight = heights[at]
					changed = true
				}
			})
			option(c, locales.SettingsTextSize(), func() {
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
		})
	})
	if changed {
		a.saveSoon()
		a.invalidate()
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
