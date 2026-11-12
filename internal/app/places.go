package app

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"myfiles/internal/config"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
)

// What the sidebar shows beside the folders of the file system is kept
// here, for every window: the folders pinned, those used lately, what the
// system calls them, and the folders inside those the tree has open.

// recentShown is how many folders used lately the sidebar lists, and
// recentKept how many the program remembers.
const (
	recentShown = 4
	recentKept  = 200
)

// sideKids are the folders a node of the tree holds, read off the main
// thread when the node first opens and again as its folder changes.
type sideKids struct {
	paths   []string
	loaded  bool
	loading bool
	// err is why they could not be read, as a computer that does not
	// answer.
	err error
}

// places is the sidebar's state.
type places struct {
	// names are what the system calls folders and its own places, ""
	// while asked for.
	names map[string]string
	// kids are the children of the tree's nodes, by the node's path.
	kids map[string]*sideKids
	// open tells which nodes of the tree show their children.
	open map[string]bool
	// recent are the folders shown lately, by path.
	recent      map[string]*config.RecentFolder
	recentDirty bool
	// shown are the recent folders the sidebar lists, worked out again
	// when the folders or the pins change.
	shown      []string
	shownStale bool
}

func newPlaces(set *config.Settings, recent []config.RecentFolder) *places {
	p := &places{
		names:      map[string]string{},
		kids:       map[string]*sideKids{},
		open:       map[string]bool{},
		recent:     map[string]*config.RecentFolder{},
		shownStale: true,
	}
	for _, path := range set.Expanded {
		p.open[path] = true
	}
	for i := range recent {
		r := recent[i]
		p.recent[r.Path] = &r
	}
	return p
}

// importPins makes the folders pinned those the system's file manager has
// pinned to its Quick access, the first time the program runs; meanwhile,
// and where it has none, the user's own folders.
func (a *App) importPins() {
	set := &a.set
	if set.PinsImported {
		return
	}
	set.PinsImported = true
	if len(set.Pinned) == 0 {
		for _, p := range fsys.Places() {
			set.Pinned = append(set.Pinned, p.Path)
		}
		set.Pinned = append(set.Pinned, shell.RecycleBin)
	}
	go func() {
		quick := a.desk.quickAccess()
		a.desk.post(func() {
			var pinned []string
			for _, q := range quick {
				switch {
				case q.Pinned:
					pinned = append(pinned, q.Path)
				case !strings.HasPrefix(q.Path, "::"):
					// What it lists by itself: the folders used most.
					if _, ok := a.side.recent[q.Path]; !ok {
						a.side.recent[q.Path] = &config.RecentFolder{Path: q.Path, Count: 3, Last: time.Now().Unix()}
						a.side.recentDirty = true
					}
				}
			}
			if len(pinned) > 0 {
				set.Pinned = pinned
			}
			a.side.shownStale = true
			a.saveSoon()
			a.invalidate()
		})
	}()
}

// pinned tells whether a folder is pinned to the sidebar.
func (a *App) pinned(path string) bool {
	return slices.ContainsFunc(a.set.Pinned, func(p string) bool { return fsys.SameName(p, path) })
}

// pin adds a folder to those pinned, and unpin takes it away.
func (a *App) pin(path string) {
	if !a.pinned(path) {
		a.set.Pinned = append(a.set.Pinned, path)
		a.pinsChanged()
	}
}

func (a *App) unpin(path string) {
	a.set.Pinned = slices.DeleteFunc(a.set.Pinned, func(p string) bool { return fsys.SameName(p, path) })
	a.pinsChanged()
}

// movePin puts a pinned folder where another is: before it, or after.
func (a *App) movePin(path, target string, after bool) {
	if fsys.SameName(path, target) {
		return
	}
	pins := slices.DeleteFunc(slices.Clone(a.set.Pinned), func(p string) bool { return fsys.SameName(p, path) })
	at := slices.IndexFunc(pins, func(p string) bool { return fsys.SameName(p, target) })
	if at < 0 {
		return
	}
	if after {
		at++
	}
	a.set.Pinned = slices.Insert(pins, at, path)
	a.pinsChanged()
}

func (a *App) pinsChanged() {
	a.side.shownStale = true
	a.saveSoon()
	a.invalidate()
}

// visited notes that a folder was shown, for the sidebar's recent ones.
func (a *App) visited(path string) {
	if !real(path) {
		return
	}
	if _, inside := fsys.Parent(path); !inside {
		return // a drive is in the tree already
	}
	s := a.side
	r := s.recent[path]
	if r == nil {
		r = &config.RecentFolder{Path: path}
		s.recent[path] = r
		if len(s.recent) > recentKept {
			s.forgetOldest()
		}
	}
	r.Count++
	r.Last = time.Now().Unix()
	s.recentDirty, s.shownStale = true, true
	a.saveSoon()
}

// forgetOldest drops the folder shown longest ago.
func (s *places) forgetOldest() {
	var oldest *config.RecentFolder
	for _, r := range s.recent {
		if oldest == nil || r.Last < oldest.Last {
			oldest = r
		}
	}
	if oldest != nil {
		delete(s.recent, oldest.Path)
	}
}

// forgetRecent takes a folder out of the recent ones.
func (a *App) forgetRecent(path string) {
	delete(a.side.recent, path)
	a.side.recentDirty, a.side.shownStale = true, true
	a.saveSoon()
	a.invalidate()
}

// recentFolders are the folders the sidebar lists as recent: those shown
// most and latest that are not pinned, the best first. A folder shown
// often long ago gives way to one shown a few times today.
func (a *App) recentFolders() []string {
	s := a.side
	if !s.shownStale {
		return s.shown
	}
	s.shownStale = false
	now := time.Now().Unix()
	type scored struct {
		path  string
		score float64
	}
	var all []scored
	for path, r := range s.recent {
		if a.pinned(path) {
			continue
		}
		days := float64(max(now-r.Last, 0)) / 86400
		all = append(all, scored{path, float64(r.Count) / (1 + days)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].path < all[j].path
	})
	s.shown = s.shown[:0]
	for i := 0; i < len(all) && i < recentShown; i++ {
		s.shown = append(s.shown, all[i].path)
	}
	return s.shown
}

// recents is what is kept of the folders shown, for the next run.
func (s *places) recents() []config.RecentFolder {
	out := make([]config.RecentFolder, 0, len(s.recent))
	for _, r := range s.recent {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// nameOf is what a row of the sidebar calls a folder or a place: the
// system's name for it once the system said, the folder's own meanwhile.
func (a *App) nameOf(path string) string {
	s := a.side
	name, asked := s.names[path]
	if !asked {
		s.names[path] = ""
		go func() {
			name := a.desk.displayName(path)
			a.desk.post(func() {
				if name != "" && name != s.names[path] {
					s.names[path] = name
					a.invalidate()
				}
			})
		}()
	}
	if name != "" {
		return name
	}
	if strings.HasPrefix(path, "::") {
		return ""
	}
	return strings.TrimSuffix(fsys.Base(path), `\`)
}

// toggle opens a node of the tree, reading what it holds, or closes it.
func (a *App) toggle(path string) {
	s := a.side
	if s.open[path] {
		delete(s.open, path)
	} else {
		s.open[path] = true
		a.readKids(path)
	}
	a.set.Expanded = a.set.Expanded[:0]
	for p := range s.open {
		a.set.Expanded = append(a.set.Expanded, p)
	}
	slices.Sort(a.set.Expanded)
	a.saveSoon()
	a.invalidate()
}

// kidsOf are the children of a node that is open, read if they were not
// yet: nil while they are.
func (a *App) kidsOf(path string) *sideKids {
	k := a.side.kids[path]
	if k == nil {
		a.readKids(path)
		k = a.side.kids[path]
	}
	return k
}

// readKids reads what a node of the tree holds, off the main thread: the
// folders of a folder, the folders a computer shares, the computers of
// the network, the Linux distributions.
func (a *App) readKids(path string) {
	s := a.side
	k := s.kids[path]
	if k == nil {
		k = &sideKids{}
		s.kids[path] = k
	}
	if k.loading {
		return
	}
	k.loading = true
	showHidden := a.set.ShowHidden
	go func() {
		paths, err := a.kids(path, showHidden)
		a.desk.post(func() {
			k.loading, k.loaded, k.err = false, true, err
			if err == nil && !slices.Equal(k.paths, paths) {
				k.paths = paths
			}
			a.invalidate()
		})
	}()
}

// kids lists the children of a node. It runs off the main thread.
func (a *App) kids(path string, showHidden bool) ([]string, error) {
	switch {
	case path == fsys.Computer:
		var out []string
		for _, v := range fsys.Volumes() {
			out = append(out, v.Root)
		}
		return out, nil
	case path == shell.Network:
		return a.desk.computers(), nil
	case path == shell.Linux:
		var out []string
		for _, name := range a.desk.distros() {
			out = append(out, shell.DistroRoot(name))
		}
		return out, nil
	case isComputer(path):
		names, err := a.desk.shares(path)
		var out []string
		for _, name := range names {
			out = append(out, path+`\`+name+`\`)
		}
		return out, err
	}
	l := load(context.Background(), path, showHidden, "", fsys.Order{DirsFirst: true})
	if l.err != nil {
		return nil, l.err
	}
	return foldersOf(path, l), nil
}

// foldersOf are the folders of a listing, in its order.
func foldersOf(path string, l *loaded) []string {
	var out []string
	for _, i := range l.view {
		if e := &l.entries[i]; e.IsDir() {
			out = append(out, fsys.Join(path, e.Name))
		}
	}
	return out
}

// listed tells the tree what a folder holds, as a tab just read it: a
// node of it that is open shows the folders made or gone at once.
func (a *App) listed(path string, l *loaded) {
	k := a.side.kids[path]
	if k == nil || !k.loaded || l.err != nil || path == fsys.Computer {
		return
	}
	if paths := foldersOf(path, l); !slices.Equal(k.paths, paths) {
		k.paths = paths
	}
}

// isComputer tells a computer of the network, `\\NAME`, which is no
// folder, from the folders it shares.
func isComputer(path string) bool {
	return strings.HasPrefix(path, `\\`) && !strings.Contains(path[2:], `\`)
}

// holdsFolders tells whether a row of the sidebar is a place of the
// file system, which a tab can show and files can be dropped on.
func holdsFolders(path string) bool {
	return !strings.HasPrefix(path, "::") && !isComputer(path)
}
