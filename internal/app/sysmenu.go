package app

import (
	"bytes"
	"image/png"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
)

// systemItems is the system's context menu as a program reads it, to
// draw its items itself: shell.SystemMenu, or a test's.
type systemItems interface {
	Items() []shell.MenuItem
	Submenu(sub int) []shell.MenuItem
	Invoke(id int) (shell.MenuAction, error)
	Close()
}

// sysItem is an item of the system's menu with its icon as a menu shows
// it.
type sysItem struct {
	shell.MenuItem
	icon ui.MenuIcon
}

// sysMenu is the system's menu of what a context menu is for, as the
// locales.MenuMore() submenu shows it. The system takes a tenth of a second to build
// one, and a second the first time: it is built on another thread while
// the menu already shows, and its items come when they come.
type sysMenu struct {
	// key names what the menu is of: the paths, or the folder.
	key string
	tab *Tab
	// paths are the entries the menu is of; none for a folder's own.
	paths []string
	menu  systemItems
	// items are the top level, nil until the system answered; err is why
	// it did not.
	items  []sysItem
	loaded bool
	err    error
	// subs are the submenus read so far, by their items' Sub: nil while
	// one is being read.
	subs map[int][]sysItem
	// used is when a menu last built with it: one that has not for a
	// while belongs to a menu that closed, and is built anew.
	used time.Time
	// gen tells a load from the ones before it.
	gen int
}

// sysMenuFresh is how long after its last use the system's menu is still
// the one of the menu showing.
const sysMenuFresh = 600 * time.Millisecond

// ownVerbs are the commands of the system's menu the menu drawn here has
// already.
var ownVerbs = map[string]bool{
	"open": true, "openas": true, "cut": true, "copy": true, "paste": true, "delete": true,
	"rename": true, "properties": true, "refresh": true,
}

// more adds the locales.MenuMore() submenu: what the system and other programs have
// for the entries chosen, or for the folder, read from the system's menu
// and drawn here; and the system's menu itself, for what cannot be.
func (w *Window) more(m *ui.Menu, t *Tab, background bool) {
	if t.Path == "" || t.Path == fsys.Computer && background || t.bin() {
		return
	}
	if t.searching() && (background || len(t.chosenPaths()) == 0) {
		// What a search found is of no one folder, and a program found is
		// no file: the system has no menu for either.
		return
	}
	// Asked for as the menu opens: there by the time the submenu is.
	sm := w.systemMenuOf(t, background)
	m.Submenu(locales.MenuMore(), func(m *ui.Menu) {
		switch {
		case sm.err != nil:
			m.Item(locales.MenuMoreFailed()).Disabled(true)
		case !sm.loaded:
			m.Item(locales.MenuLoading()).Disabled(true)
		default:
			w.sysItems(m, sm, sm.items)
		}
		m.Separator()
		if m.Item(locales.MenuSystem()).Chosen() {
			w.systemMenu(t, background)
		}
	})
}

// sysItems adds items of the system's menu to a menu.
func (w *Window) sysItems(m *ui.Menu, sm *sysMenu, items []sysItem) {
	for i := range items {
		it := &items[i]
		switch {
		case it.Separator:
			m.Separator()
		case it.Sub != 0:
			sub := it.Sub
			item := m.Submenu(it.Text, func(m *ui.Menu) {
				if items, ok := w.sysSub(sm, sub); ok {
					w.sysItems(m, sm, items)
				} else {
					m.Item(locales.MenuLoading()).Disabled(true)
				}
			})
			if it.icon != nil {
				item.Icon(it.icon)
			}
		default:
			item := m.Item(it.Text).ID("sys" + strconv.Itoa(it.ID)).Disabled(it.Disabled)
			if it.Checked {
				item.Checked(true)
			}
			if it.icon != nil {
				item.Icon(it.icon)
			}
			if item.Chosen() {
				w.sysInvoke(sm, it.ID)
			}
		}
	}
}

// systemMenuOf is the system's menu for what a tab's context menu is of,
// being built if it is not there yet.
func (w *Window) systemMenuOf(t *Tab, background bool) *sysMenu {
	paths := t.chosenPaths()
	dir := ""
	if background || len(paths) == 0 {
		paths, dir = nil, t.Path
	}
	key := dir + "\x00" + strings.Join(paths, "\x00")
	now := time.Now()
	if sm := w.sys; sm != nil && sm.key == key && now.Sub(sm.used) < sysMenuFresh {
		sm.used = now
		return sm
	}
	old := w.sys
	sm := &sysMenu{key: key, tab: t, paths: paths, used: now, subs: map[int][]sysItem{}}
	if old != nil {
		sm.gen = old.gen + 1
		if old.key == key && old.loaded {
			// The same menu again, as when it opens a second time: what
			// it showed last shows until the system answered anew.
			sm.items, sm.loaded, sm.menu, sm.subs = old.items, true, old.menu, old.subs
		} else if old.menu != nil {
			go old.menu.Close()
		}
	}
	w.sys = sm
	owner, kept := w.handle(), sm.menu
	go func() {
		menu, err := w.app.desk.loadMenu(owner, paths, dir)
		var items []shell.MenuItem
		if err == nil {
			items = menu.Items()
		}
		ready := w.app.sysIcons(items)
		w.update(func() {
			if w.sys != sm {
				// Another menu took its place meanwhile.
				if menu != nil {
					go menu.Close()
				}
				return
			}
			if kept != nil {
				go kept.Close()
			}
			sm.menu, sm.err, sm.loaded = menu, err, true
			sm.items, sm.subs = tidy(ready()), map[int][]sysItem{}
			w.invalidate()
		})
	}()
	return sm
}

// sysSub is a submenu of the system's menu, and whether it was read yet:
// it is read, on another thread, the first time it is asked for.
func (w *Window) sysSub(sm *sysMenu, sub int) ([]sysItem, bool) {
	items, asked := sm.subs[sub]
	if asked {
		return items, items != nil
	}
	sm.subs[sub] = nil
	menu, subs := sm.menu, sm.subs
	if menu == nil {
		return nil, false
	}
	go func() {
		ready := w.app.sysIcons(menu.Submenu(sub))
		w.update(func() {
			items := ready()
			if items == nil {
				items = []sysItem{}
			}
			subs[sub] = items
			w.invalidate()
		})
	}()
	return nil, false
}

// sysIcons makes, off the main thread, the pictures of the icons of items
// of the system's menu, and returns what makes the icons of them on it.
func (a *App) sysIcons(items []shell.MenuItem) func() []sysItem {
	pictures := make([][]byte, len(items))
	for i, it := range items {
		var buf bytes.Buffer
		if it.Icon != nil && png.Encode(&buf, it.Icon) == nil {
			pictures[i] = buf.Bytes()
		}
	}
	return func() []sysItem {
		out := make([]sysItem, len(items))
		for i, it := range items {
			out[i].MenuItem = it
			if pictures[i] != nil {
				if icon, err := a.desk.menuIcon(pictures[i]); err == nil {
					out[i].icon = icon
				}
			}
		}
		return out
	}
}

// tidy leaves out of the system's top level what the menu drawn here has
// itself, and the separators that then separate nothing.
func tidy(items []sysItem) []sysItem {
	out := items[:0:0]
	for _, it := range items {
		switch {
		case it.Separator:
			if len(out) == 0 || out[len(out)-1].Separator {
				continue
			}
		case it.Sub == 0 && ownVerbs[strings.ToLower(it.Verb)]:
			continue
		}
		out = append(out, it)
	}
	for len(out) > 0 && out[len(out)-1].Separator {
		out = out[:len(out)-1]
	}
	return out
}

// sysInvoke runs a command of the system's menu, and shows what it did
// to the folder.
func (w *Window) sysInvoke(sm *sysMenu, id int) {
	menu, t, paths := sm.menu, sm.tab, sm.paths
	if menu == nil {
		return
	}
	go func() {
		action, err := menu.Invoke(id)
		w.report(err)
		w.update(func() {
			// The menu is spent: the next one is built anew.
			if w.sys == sm {
				w.sys = nil
			}
			go menu.Close()
			if w.tabs[t.ID] != t {
				return
			}
			if action == shell.MenuRename && len(paths) == 1 {
				t.rename = t.nameOfPath(paths[0])
				t.wantFocus = true
				return
			}
			switch {
			case action == shell.MenuOpen:
				// As a double click opens: by the program found here.
				w.openChosen(t)
				return
			case action == shell.MenuOpenWith && len(paths) == 1:
				w.choose(paths[0], false)
				return
			}
			w.refresh(t)
		})
	}()
}

// warmSystemMenu builds the system's menu of a file and of a folder once,
// unseen: the first time takes the system a second, loading what other
// programs added to it, and this is that first time rather than the
// user's first right click.
func (a *App) warmSystemMenu() {
	if a.exe == "" {
		return
	}
	dir, ok := fsys.Parent(a.exe)
	if !ok {
		return
	}
	// A file, a folder, and the room in a folder: each has handlers of
	// its own.
	for _, of := range []struct {
		paths      []string
		background string
	}{{paths: []string{a.exe}}, {paths: []string{dir}}, {background: dir}} {
		if menu, err := a.desk.loadMenu(0, of.paths, of.background); err == nil {
			menu.Close()
		}
	}
}
