package app

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"myfiles/internal/cli"
	"myfiles/internal/fsys"
)

// candidateLimit is how many candidates an address being typed shows: more
// of them are a letter away.
const candidateLimit = 100

// candidates are what an address being typed may go on to: the folders of
// the folder typed so far whose names begin with what is typed after it,
// then those that hold it elsewhere; and the drives, for one that names no
// folder yet.
type candidates struct {
	// dir is the folder whose folders were asked for, and folders are
	// they, in the order of their names, once read. cancel gives up the
	// reading under way.
	dir     string
	folders []fsys.Entry
	cancel  context.CancelFunc
	// drives are the drives, once read: asked tells that they were asked
	// for.
	drives []fsys.Volume
	asked  bool
	// shown are those the list shows, at the one the arrows are on, -1 for
	// none, and scroll set to bring that one into view.
	shown  []candidate
	at     int
	scroll bool
}

// candidate is one of them: what the list calls it, with a note beside, as
// a drive's label; where it leads; and the address as it is once
// completed, which names it and goes on into it. entry is the folder as
// the folder above lists it, of no name for a drive.
type candidate struct {
	name, note string
	path, text string
	entry      fsys.Entry
}

// complete lists what the address typed so far may go on to. Folders and
// drives are read off the main thread, which waits a moment for them: those
// of a local disk are there in the frame that shows the letter typed.
func (w *Window) complete(t *Tab) {
	cs := t.cands
	if cs == nil {
		cs = &candidates{at: -1}
		t.cands = cs
	}
	cs.shown, cs.at = cs.shown[:0], -1
	if strings.TrimSpace(t.addr) == "" {
		return
	}
	cwd := t.place()
	if !real(cwd) {
		cwd = ""
	}
	dir, name := cli.Typing(t.addr, cwd)
	head := t.addr[:len(t.addr)-len(name)]
	// The drives complete a name that follows no folder, as "d" does, and
	// one under the list of drives.
	drives := head == "" || dir == fsys.Computer
	if dir == fsys.Computer {
		dir = ""
	}
	readDrives := drives && !cs.asked
	readDir := dir != cs.dir
	if readDir || readDrives {
		if readDir {
			if cs.cancel != nil {
				cs.cancel()
			}
			cs.dir, cs.folders, cs.cancel = dir, nil, nil
		}
		cs.asked = cs.asked || readDrives
		type read struct {
			folders []fsys.Entry
			drives  []fsys.Volume
		}
		ctx := context.Background()
		if readDir && dir != "" {
			ctx, cs.cancel = context.WithCancel(ctx)
		}
		done := make(chan read, 1)
		go func() {
			var r read
			if readDrives {
				r.drives = fsys.Volumes()
			}
			if readDir && dir != "" {
				r.folders = subfolders(ctx, dir)
			}
			done <- r
		}()
		have := func(r read) bool {
			if t.cands != cs {
				return false
			}
			if readDrives {
				cs.drives = r.drives
			}
			if readDir && cs.dir == dir {
				cs.folders = r.folders
			}
			return true
		}
		timer := time.NewTimer(syncWait)
		select {
		case r := <-done:
			timer.Stop()
			have(r)
		case <-timer.C:
			w.awaited++
			go func() {
				r := <-done
				w.update(func() {
					w.awaited--
					// The address typed by now is completed with them.
					if have(r) && t.editing {
						w.complete(t)
					}
				})
			}()
		}
	}

	// What a completion goes on with: the separator typed last, or the
	// system's.
	sep := string(fsys.Sep)
	if head != "" {
		sep = head[len(head)-1:]
	}
	want := strings.ToLower(name)
	if drives {
		for i := range cs.drives {
			v := &cs.drives[i]
			if strings.HasPrefix(strings.ToLower(v.Root), want) {
				cs.shown = append(cs.shown, candidate{name: v.Root, note: v.Label, path: v.Root, text: v.Root})
			}
		}
	}
	// Folders the system hides are listed once a name is typed: the
	// address goes to them all the same.
	hidden := want != "" || w.app.set.ShowHidden
	var inside []candidate
	for i := range cs.folders {
		e := &cs.folders[i]
		if e.Hidden() && !hidden {
			continue
		}
		at := strings.Index(strings.ToLower(e.Name), want)
		if at < 0 {
			continue
		}
		c := candidate{name: e.Name, path: fsys.Join(cs.dir, e.Name), text: head + e.Name + sep, entry: *e}
		if at == 0 {
			cs.shown = append(cs.shown, c)
		} else if len(inside) < candidateLimit {
			inside = append(inside, c)
		}
		if len(cs.shown) >= candidateLimit {
			break
		}
	}
	cs.shown = append(cs.shown, inside...)
	if len(cs.shown) > candidateLimit {
		cs.shown = cs.shown[:candidateLimit]
	}
}

// subfolders lists the folders of a folder, in the order of their names:
// none for one that cannot be read.
func subfolders(ctx context.Context, dir string) []fsys.Entry {
	l, err := fsys.List(ctx, dir)
	if err != nil {
		return nil
	}
	folders := l.Entries[:0]
	for _, e := range l.Entries {
		if e.IsDir() {
			folders = append(folders, e)
		}
	}
	view := fsys.All(folders)
	fsys.Sort(folders, view, fsys.Order{})
	out := make([]fsys.Entry, len(view))
	for i, at := range view {
		out[i] = folders[at]
	}
	return out
}

// endAddress leaves the address as the folder's components again.
func (w *Window) endAddress(t *Tab) {
	t.editing = false
	if cs := t.cands; cs != nil && cs.cancel != nil {
		cs.cancel()
	}
	t.cands = nil
}

// candidateKeys moves among the candidates as the keys pressed in the
// address ask: Down and Up from one to the next, with the address typed
// above the first; Tab completes the address with the one the arrows are
// on, or the first, and lists what it goes on to.
func (w *Window) candidateKeys(t *Tab, in ui.Element) {
	cs := t.cands
	if cs == nil || len(cs.shown) == 0 {
		// Tab is then the focus's, which goes on.
		return
	}
	switch {
	case in.Shortcut(0, ui.KeyDown):
		cs.at, cs.scroll = min(cs.at+1, len(cs.shown)-1), true
	case in.Shortcut(0, ui.KeyUp):
		cs.at, cs.scroll = max(cs.at-1, -1), true
	case in.Shortcut(0, ui.KeyTab):
		t.addr = cs.shown[max(cs.at, 0)].text
		t.addrEnd = true
		w.complete(t)
	}
}

// candidateList builds the candidates under the address, and tells the one
// clicked.
func (w *Window) candidateList(c *ui.Context, t *Tab, box ui.Element) (chosen *candidate) {
	cs := t.cands
	if cs == nil || len(cs.shown) == 0 {
		return nil
	}
	th := c.Theme()
	const row = controlSize + 2
	ui.Overlay(c, func() {
		// A press in the list leaves the keyboard with the address, which
		// leaving would end.
		panel := ui.Scroll(c.Key("candidates")).AttachTo(box, ui.AnchorBottomStart, ui.AnchorTopStart).
			Width(max(box.Bounds().W, 260)).MaxHeight(row*10.5+8).Margin(3, 0, 0, 0).Padding(4).
			Radius(6).Background(th.Background).Border(1, th.Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18)).
			Role(ui.RoleList).KeepFocus()
		panel.Children(func() {
			ui.Column(c).AlignItems(ui.Stretch).Children(func() {
				for i := range cs.shown {
					cand := &cs.shown[i]
					item := ui.ButtonBase(c.Key(cand.path)).Height(row).Padding(0, 6).Gap(6).Radius(4).
						AlignItems(ui.Center).TabStop(false).Role(ui.RoleListItem)
					switch {
					case i == cs.at:
						item.Background(th.Accent.Alpha(0.25))
					case item.Hovered():
						item.Background(th.SurfaceHover)
					}
					if i == cs.at && cs.scroll {
						cs.scroll = false
						item.ScrollIntoView()
					}
					item.Children(func() {
						icons := w.app.icons.at(iconSize)
						var icon *ui.Bitmap
						if cand.entry.Name != "" {
							icon, _ = icons.forEntry(cs.dir, &cand.entry)
						} else {
							icon, _ = icons.forPath(cand.path, fsys.AttrDir)
						}
						if icon != nil {
							ui.Image(c, icon).Size(iconSize, iconSize).Shrink(0)
						} else {
							ui.Box(c).Size(iconSize, iconSize).Shrink(0)
						}
						ui.Text(c, cand.name).SingleLine().Grow(1).MinWidth(0)
						if cand.note != "" {
							ui.Text(c, cand.note).SingleLine().FontSize(th.FontSize - 1).TextColor(th.TextMuted).Shrink(0)
						}
					})
					if item.Clicked() {
						chosen = cand
					}
				}
			})
		})
	})
	return chosen
}
