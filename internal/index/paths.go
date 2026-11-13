package index

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// pathEvent is a change the system tells of by the path of what changed,
// under the root that is listened to.
type pathEvent struct {
	rel    string
	action uint32
}

// What happened to a path: the actions of the system's notes of changes.
const (
	actAdded      = 1
	actRemoved    = 2
	actModified   = 3
	actRenamedOld = 4
	actRenamedNew = 5
)

// pathWatcher tells of what changes under a root. read waits for changes;
// errLost says more happened than was kept.
type pathWatcher interface {
	read() ([]pathEvent, error)
	Close()
}

var errLost = errors.New("index: changes were lost")

// pathFeed gathers what a watcher tells while nobody takes it: a drive is
// listened to from before it is read, so that what changes while it is
// read is not missed.
type pathFeed struct {
	w    pathWatcher
	mu   sync.Mutex
	evs  []pathEvent
	lost bool
	err  error
	// wake holds a token while there is something to take.
	wake chan struct{}
}

// feedAtMost is how many changes a feed keeps untaken before it gives up
// on telling them one by one.
const feedAtMost = 200_000

func newPathFeed(root string) *pathFeed {
	w, err := openWatcher(root)
	if err != nil {
		return nil
	}
	f := &pathFeed{w: w, wake: make(chan struct{}, 1)}
	go func() {
		for {
			evs, err := w.read()
			f.mu.Lock()
			switch {
			case errors.Is(err, errLost):
				f.lost, f.evs = true, nil
			case err != nil:
				f.err = err
			case len(f.evs)+len(evs) > feedAtMost:
				f.lost, f.evs = true, nil
			default:
				f.evs = append(f.evs, evs...)
			}
			failed := f.err != nil
			f.mu.Unlock()
			select {
			case f.wake <- struct{}{}:
			default:
			}
			if failed {
				return
			}
		}
	}()
	return f
}

// take is what came since the last take, and whether some was lost.
func (f *pathFeed) take() (evs []pathEvent, lost bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	evs, lost, err = f.evs, f.lost, f.err
	f.evs, f.lost = nil, false
	return
}

func (f *pathFeed) Close() { f.w.Close() }

// rereadAfter is the least time between two readings of a drive whose
// changes were lost: a drive busier than can be followed is not read over
// and over.
const rereadAfter = 10 * time.Minute

// followPaths keeps the drive's index up to date with what a feed tells,
// until the drive must be read again. fresh is set when the drive was read
// just now, with the feed listening: what the feed lost meanwhile the
// reading has.
func (d *drive) followPaths(ctx context.Context, f *pathFeed, fresh bool) (again bool) {
	d.mu.Lock()
	d.nextID = d.view.maxID() + 1
	d.mu.Unlock()
	save := time.NewTicker(30 * time.Second)
	defer save.Stop()
	stale := time.NewTimer(staleAfter)
	defer stale.Stop()
	d.live.Store(true)
	d.m.changed()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-d.again:
			return true
		case <-stale.C:
			// What changed while the program was not running nothing
			// told: the drive is read again now and then.
			return true
		case <-save.C:
			d.save(false)
		case <-f.wake:
			// More often follows at once: gathered, it is applied
			// together.
			select {
			case <-time.After(gatherFor):
			case <-ctx.Done():
				return false
			}
			evs, lost, err := f.take()
			if err != nil {
				// The drive went, or stopped telling: as one that tells
				// nothing, from here.
				d.live.Store(false)
				return d.follow(ctx, nil)
			}
			if lost && !fresh {
				d.live.Store(false)
				d.mu.RLock()
				built := time.Unix(d.view.ix.Meta.Built, 0)
				d.mu.RUnlock()
				select {
				case <-ctx.Done():
					return false
				case <-d.again:
				case <-time.After(max(rereadAfter-time.Since(built), 2*time.Second)):
				}
				return true
			}
			fresh = false
			d.applyPaths(ctx, evs)
		}
	}
}

// applyPaths makes the index follow changes told by their paths.
func (d *drive) applyPaths(ctx context.Context, evs []pathEvent) {
	if len(evs) == 0 {
		return
	}
	root := strings.TrimRight(d.info.Root, `\`)
	abs := func(rel string) string { return root + `\` + rel }
	// Each file asked once, as it is now, before the index is held.
	stats := make(map[string]*pathStat, len(evs))
	for _, ev := range evs {
		if ev.action == actRemoved || ev.action == actRenamedOld {
			continue
		}
		if _, seen := stats[ev.rel]; !seen {
			c, ok := statPath(abs(ev.rel))
			stats[ev.rel] = &pathStat{c, ok}
		}
	}
	rules := d.m.currentRules()
	touched := false
	var made []string
	d.mu.Lock()
	v := d.view
	idAt := func(rel string) (uint64, bool) {
		if ref, ok := v.Resolve(abs(rel)); ok && ref != 0 {
			return v.idOf(ref), true
		}
		return 0, false
	}
	remove := func(rel string) {
		if id, ok := idAt(rel); ok {
			touched = v.Remove(id) || touched
		}
	}
	var put func(rel string, c Change, id uint64, depth int) bool
	put = func(rel string, c Change, id uint64, depth int) bool {
		dir := ""
		if i := strings.LastIndexByte(rel, '\\'); i >= 0 {
			dir = rel[:i]
		}
		c.Parent = v.ix.id[0]
		if dir != "" {
			parent, ok := idAt(dir)
			if !ok {
				// In a folder the index does not know, as one made in
				// this very batch and told of after: the folders above it
				// first.
				pc, there := statPath(abs(dir))
				if !there || pc.Flags&FlagDir == 0 || depth > 64 {
					return false
				}
				d.nextID++
				if !put(dir, pc, d.nextID, depth+1) {
					return false
				}
				parent, _ = idAt(dir)
			}
			c.Parent = parent
		}
		c.ID = id
		return v.Apply(c, rules)
	}
	renamedFrom, renaming := "", false
	for _, ev := range evs {
		if renaming && ev.action != actRenamedNew {
			// Renamed to where nothing is listened to.
			remove(renamedFrom)
			renaming = false
		}
		switch ev.action {
		case actRemoved:
			remove(ev.rel)
		case actRenamedOld:
			renamedFrom, renaming = ev.rel, true
		default:
			st := stats[ev.rel]
			from, wasRenaming := renamedFrom, renaming
			renaming = false
			if !st.ok {
				// Gone again since.
				remove(ev.rel)
				if wasRenaming {
					remove(from)
				}
				continue
			}
			id, known := idAt(ev.rel)
			if ev.action == actRenamedNew && wasRenaming {
				if old, ok := idAt(from); ok {
					if known && id != old {
						// What had the name before is replaced.
						v.Remove(id)
					}
					id, known = old, true
				}
			}
			if known && ev.action == actModified && st.c.Flags&FlagDir != 0 {
				// A folder's time, as a file in it changed: nothing a
				// search shows.
				continue
			}
			if !known {
				d.nextID++
				id = d.nextID
				if st.c.Flags&FlagDir != 0 && st.c.Flags&FlagLink == 0 {
					// A folder that came with what it holds, as one moved
					// here, is told of alone.
					made = append(made, ev.rel)
				}
			}
			touched = put(ev.rel, st.c, id, 0) || touched
		}
	}
	if renaming {
		remove(renamedFrom)
	}
	d.mu.Unlock()
	d.m.rested.Store(false)
	for i, rel := range made {
		// One read with the folder it is in is read.
		inside := false
		for _, above := range made[:i] {
			inside = inside || strings.HasPrefix(rel, above+`\`)
		}
		if !inside {
			touched = d.takeIn(ctx, abs(rel), rules) || touched
		}
	}
	// Written at once when much changed.
	d.save(false)
	if touched {
		d.m.changed()
	}
}

type pathStat struct {
	c  Change
	ok bool
}

// takeIn reads a folder that came to the drive with what it holds, and
// has the index take that.
func (d *drive) takeIn(ctx context.Context, path string, rules *Rules) bool {
	sc := NewScratch(d.m.cfg.Dir)
	defer sc.Close()
	scans, rootID, err := Walk(ctx, path, WalkOptions{Scratch: sc, Workers: 2})
	if err != nil {
		return false
	}
	total := 0
	for _, s := range scans {
		total += s.n
	}
	if total == 0 {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	v := d.view
	ref, ok := v.Resolve(path)
	if !ok || ref == 0 {
		return false
	}
	// The walk numbered what it met in its own way: each file takes the
	// number the index has for it, when it has the file already, or the
	// next one.
	ids := map[uint64]uint64{rootID: v.idOf(ref)}
	// A file after its folder: the walk tells of them in no such order.
	left := make([]at, 0, total)
	for si, s := range scans {
		for ri := range s.n {
			left = append(left, at{uint32(si), uint32(ri)})
		}
	}
	touched := false
	for len(left) > 0 {
		kept := left[:0]
		for _, a := range left {
			s := scans[a.scan]
			rec := s.rec(int(a.rec))
			parent, ok := ids[rec.parent]
			if !ok {
				kept = append(kept, a)
				continue
			}
			in, ok := v.find(parent)
			if !ok && parent != v.ix.id[0] {
				continue
			}
			name := string(s.name(rec))
			var id uint64
			if child, ok := v.childOf(in, name); ok {
				id = v.idOf(child)
			} else {
				d.nextID++
				id = d.nextID
			}
			ids[rec.id] = id
			c := Change{Entry: Entry{
				ID: id, Parent: parent, Flags: rec.flags, Size: rec.size,
				Modified: rec.mtime, Created: rec.ctime, Accessed: rec.atime,
			}, Name: name}
			touched = v.Apply(c, rules) || touched
		}
		if len(kept) == len(left) {
			break
		}
		left = kept
	}
	return touched
}

// maxID is the highest number a file of the view has, of those the walk
// or the following of paths gave.
func (v *View) maxID() uint64 {
	top := uint64(len(v.ix.byRec))
	for i := range v.d.rows {
		if id := v.d.rows[i].id & recMask; id > top {
			top = id
		}
	}
	return top
}
