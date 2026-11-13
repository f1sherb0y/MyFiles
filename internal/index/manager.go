package index

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"myfiles/internal/lnk"
	"myfiles/internal/search"
)

// State is where a drive's index is.
type State uint8

const (
	// StateOff is a drive the user does not have indexed.
	StateOff State = iota
	// StateLoading is an index being mapped from its file.
	StateLoading
	// StateReading is a drive being read for the first time: nothing to
	// search yet.
	StateReading
	// StateReady is an index that answers searches.
	StateReady
	// StateFailed is a drive that could not be read.
	StateFailed
)

// DriveInfo is a drive, or a folder, to index.
type DriveInfo struct {
	// Root is its path with the trailing separator, as "C:\".
	Root       string
	FileSystem string
	Serial     uint32
	// Journal is set for a file system that notes its changes, as NTFS.
	Journal bool
	// Fixed is unset for drives that come and go, and for the network's.
	Fixed bool
}

// DriveStatus is what the settings show of a drive's index.
type DriveStatus struct {
	DriveInfo
	State State
	// Files and Dirs count what the index holds, and Seen what was read
	// so far of a drive being read.
	Files, Dirs int
	Seen        int64
	// Refreshing is set while the drive is read again behind an index
	// that answers meanwhile.
	Refreshing bool
	// Live is set for an index that follows the drive's changes as they
	// happen.
	Live bool
	// Table is set for an index read from the file table, the fast way.
	Table bool
	Built time.Time
	Took  time.Duration
	Err   string
}

// Config is what a Manager is made with.
type Config struct {
	// Dir is the folder the indexes are kept in.
	Dir   string
	Rules []Rule
	// Expand turns %NAME% in a rule's path into the environment's value.
	Expand func(string) string
	// Off are the roots of the drives the user does not have indexed, and
	// Extra the roots of drives and folders indexed beside the fixed
	// drives.
	Off   []string
	Extra []string
	// Changed is called, from any goroutine, when an index changed or a
	// drive's state did.
	Changed func()
}

// Manager keeps the indexes of the machine's drives: it reads the drives,
// follows their changes, and answers searches.
type Manager struct {
	cfg   Config
	store Store

	mu     sync.Mutex
	rules  *Rules
	drives map[string]*drive
	order  []string
	closed bool
	// folders are indexes of folders no drive's index covers, made for a
	// search and kept a while.
	folders []*folderIndex
	wg      sync.WaitGroup
	// reading is held by the drive being read, of those whose file system
	// notes its changes, and readingSlow by the one of the others: a drive
	// read in seconds does not wait for one that takes minutes.
	reading, readingSlow chan struct{}
	// searched is when a search last ran, in nanoseconds since 1970, and
	// rested whether the indexes' memory was given back since.
	searched atomic.Int64
	rested   atomic.Bool
	// links are what the shortcuts found were read to point at, by their
	// paths.
	links sync.Map
	// present are the drive letters there were when last looked.
	present atomic.Uint32
	resting sync.Once
	quit    chan struct{}
}

type drive struct {
	m    *Manager
	info DriveInfo
	key  string

	// mu guards view: searches read under it, and what follows the
	// drive's changes writes.
	mu   sync.RWMutex
	view *View
	// nextID numbers the files that come to a drive whose file system
	// does not name them.
	nextID uint64
	// holds close what the index has open on the drive, for when the
	// drive is asked for: a drive that can be taken out is not let go
	// while it is open.
	holdMu sync.Mutex
	holds  map[int]func()
	held   int
	// away is set, with when, for a drive that went, or was asked for:
	// its index starts again when the drive is back. m.mu guards them.
	away      bool
	awaySince time.Time
	// reread is set for a drive back from having been away: what changed
	// on it meanwhile only a journal would tell.
	reread atomic.Bool

	state      atomic.Uint32
	seen       atomic.Int64
	refreshing atomic.Bool
	live       atomic.Bool
	err        atomic.Pointer[string]
	// usn is where in the journal the view is up to date with.
	usn    int64
	saved  time.Time
	cancel context.CancelFunc
	done   chan struct{}
	// again asks for the drive to be read again.
	again chan struct{}
}

type folderIndex struct {
	path string
	// mu guards view: a search reads under it, and dropping the index,
	// which unmaps what the search would read, writes.
	mu      sync.RWMutex
	view    *View
	dropped bool
	// file is the file the index is in, "" for one in memory.
	file string
	// at is when it was last used, in nanoseconds since 1970.
	at atomic.Int64
	// ready is closed once the folder is read.
	ready chan struct{}
}

// drop lets go of a folder's index, and of its file.
func (f *folderIndex) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = true
	if f.view != nil {
		f.view.ix.Close()
		f.view = nil
	}
	if f.file != "" {
		os.Remove(f.file)
	}
}

// NewManager makes a manager. Start has it look for the drives.
func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg, store: Store{Dir: cfg.Dir}, rules: NewRules(cfg.Rules, cfg.Expand),
		drives: map[string]*drive{}, reading: make(chan struct{}, 1), readingSlow: make(chan struct{}, 1), quit: make(chan struct{})}
}

// rootKey names a root however it is written: with or without the
// separator at its end, in any case.
func rootKey(root string) string { return strings.ToUpper(strings.TrimRight(root, `\`) + `\`) }

// keyOf names a root's file in the store.
func keyOf(root string) string {
	if len(root) == 3 && root[1] == ':' {
		return strings.ToLower(root[:1])
	}
	sum := sha1.Sum([]byte(strings.ToLower(root)))
	return "f-" + hex.EncodeToString(sum[:8])
}

func (m *Manager) changed() {
	if m.cfg.Changed != nil {
		m.cfg.Changed()
	}
}

// Start looks for the drives and brings their indexes up, each on a
// goroutine of its own. It returns at once.
func (m *Manager) Start() {
	m.resting.Do(func() { m.wg.Go(m.restLoop) })
	m.present.Store(drivesPresent())
	m.wg.Go(func() {
		m.store.Sweep()
		infos := localDrives()
		for _, extra := range m.cfg.Extra {
			if info, ok := driveInfo(extra); ok {
				infos = append(infos, info)
			}
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed {
			return
		}
		// Those that are read fast first: the others wait their turn.
		slices.SortStableFunc(infos, func(a, b DriveInfo) int {
			switch {
			case a.Journal == b.Journal:
				return 0
			case a.Journal:
				return -1
			}
			return 1
		})
		for _, info := range infos {
			k := rootKey(info.Root)
			if _, dup := m.drives[k]; dup {
				continue
			}
			extra := slices.ContainsFunc(m.cfg.Extra, func(r string) bool { return rootKey(r) == k })
			d := &drive{m: m, info: info, key: keyOf(info.Root), again: make(chan struct{}, 1)}
			m.drives[k] = d
			m.order = append(m.order, k)
			off := slices.ContainsFunc(m.cfg.Off, func(r string) bool { return rootKey(r) == k })
			if off || !info.Fixed && !extra {
				continue
			}
			m.startDrive(d)
		}
		m.changed()
	})
}

// startDrive runs a drive's index. m.mu is held.
func (m *Manager) startDrive(d *drive) {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.done = cancel, make(chan struct{})
	d.state.Store(uint32(StateLoading))
	done := d.done
	m.wg.Go(func() {
		defer close(done)
		d.run(ctx)
	})
}

// stopDrive stops a drive's index and waits for it. m.mu is not held.
func (d *drive) stop() {
	d.m.mu.Lock()
	cancel, done := d.cancel, d.done
	d.cancel, d.done = nil, nil
	d.m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// SetEnabled has a drive indexed, or not: turned off, its index is
// forgotten.
func (m *Manager) SetEnabled(root string, on bool) {
	m.mu.Lock()
	d := m.drives[rootKey(root)]
	m.mu.Unlock()
	if d == nil {
		if !on {
			return
		}
		info, ok := driveInfo(root)
		if !ok {
			return
		}
		d = &drive{m: m, info: info, key: keyOf(info.Root), again: make(chan struct{}, 1)}
		m.mu.Lock()
		m.drives[rootKey(root)] = d
		m.order = append(m.order, rootKey(root))
		m.mu.Unlock()
	}
	d.stop()
	if on {
		m.mu.Lock()
		if !m.closed {
			m.startDrive(d)
		}
		m.mu.Unlock()
	} else {
		d.mu.Lock()
		if d.view != nil {
			d.view.ix.Close()
			d.view = nil
		}
		d.mu.Unlock()
		d.state.Store(uint32(StateOff))
		m.store.Remove(d.key)
	}
	m.changed()
}

// Rebuild reads a drive again, "" for all of them. The index there
// answers until the new one takes its place.
func (m *Manager) Rebuild(root string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.drives {
		if root != "" && k != rootKey(root) || d.cancel == nil {
			continue
		}
		select {
		case d.again <- struct{}{}:
		default:
		}
	}
}

// SetRules takes the user's rules as they are now.
func (m *Manager) SetRules(rules []Rule) {
	rs := NewRules(rules, m.cfg.Expand)
	m.mu.Lock()
	m.rules = rs
	drives := make([]*drive, 0, len(m.drives))
	for _, d := range m.drives {
		drives = append(drives, d)
	}
	folders := m.folders
	m.folders = nil
	m.mu.Unlock()
	for _, f := range folders {
		// Read again under the rules as they are now, when next asked.
		go func() {
			<-f.ready
			f.drop()
		}()
	}
	for _, d := range drives {
		d.mu.Lock()
		if d.view != nil {
			d.view.Mark(rs)
		}
		d.mu.Unlock()
	}
	m.changed()
}

func (m *Manager) currentRules() *Rules {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rules
}

// Drives tells of the drives and their indexes, in the order found.
func (m *Manager) Drives() []DriveStatus {
	m.mu.Lock()
	drives := make([]*drive, 0, len(m.order))
	for _, k := range m.order {
		drives = append(drives, m.drives[k])
	}
	m.mu.Unlock()
	out := make([]DriveStatus, len(drives))
	for i, d := range drives {
		s := DriveStatus{DriveInfo: d.info, State: State(d.state.Load()), Seen: d.seen.Load(),
			Refreshing: d.refreshing.Load(), Live: d.live.Load()}
		if e := d.err.Load(); e != nil {
			s.Err = *e
		}
		d.mu.RLock()
		if d.view != nil {
			meta := d.view.ix.Meta
			s.Files, s.Dirs, s.Table = meta.Files, meta.Dirs, meta.Table
			s.Built, s.Took = time.Unix(meta.Built, 0), time.Duration(meta.Took)*time.Millisecond
		}
		d.mu.RUnlock()
		out[i] = s
	}
	return out
}

// restAfter is how long after the last search the memory the searches
// brought in of the indexes' files is given back.
const restAfter = 10 * time.Second

// restLoop gives back the indexes' memory when no search ran for a while:
// a search over millions of files brings much of an index's file in, which
// the program need not hold on to between one bout of searching and the
// next.
func (m *Manager) restLoop() {
	tick := time.NewTicker(restAfter / 2)
	defer tick.Stop()
	drives := time.NewTicker(lookForDrivesEvery)
	defer drives.Stop()
	for {
		select {
		case <-m.quit:
			return
		case <-drives.C:
			m.lookForDrives()
			continue
		case <-tick.C:
		}
		if !m.rested.Load() && m.idle() {
			m.rest()
		}
		m.expireFolders()
	}
}

// expireFolders lets go of the indexes of folders read for searches that
// were last used a while ago, with their files.
func (m *Manager) expireFolders() {
	m.mu.Lock()
	var gone []*folderIndex
	kept := m.folders[:0]
	for _, f := range m.folders {
		select {
		case <-f.ready:
			if time.Since(time.Unix(0, f.at.Load())) > folderTTL {
				gone = append(gone, f)
				continue
			}
		default:
		}
		kept = append(kept, f)
	}
	m.folders = kept
	m.mu.Unlock()
	for _, f := range gone {
		f.drop()
	}
}

// lookForDrivesEvery is how often the drives there are are counted, and
// backAfter how long after a drive was asked for, and did not go, its
// index starts again.
const (
	lookForDrivesEvery = 3 * time.Second
	backAfter          = 20 * time.Second
)

// lookForDrives takes up the drives that came since it last looked, as a
// stick put in, and lets go of those that went.
func (m *Manager) lookForDrives() {
	now := drivesPresent()
	was := m.present.Swap(now)
	for i := range 26 {
		bit, root := uint32(1)<<i, string(rune('A'+i))+":"+`\`
		switch {
		case was&bit != 0 && now&bit == 0:
			m.driveWent(root)
		case was&bit == 0 && now&bit != 0:
			m.driveCame(root)
		case now&bit != 0:
			// Asked for, and still there: the asking came to nothing.
			m.mu.Lock()
			d := m.drives[rootKey(root)]
			back := d != nil && d.away && time.Since(d.awaySince) > backAfter
			m.mu.Unlock()
			if back {
				m.driveCame(root)
			}
		}
	}
}

// driveWent lets go of the index of a drive that is gone, or about to
// be: nothing of it is searched until it is back.
func (m *Manager) driveWent(root string) {
	m.mu.Lock()
	d := m.drives[rootKey(root)]
	if d == nil || d.away || d.cancel == nil {
		m.mu.Unlock()
		return
	}
	d.away, d.awaySince = true, time.Now()
	m.mu.Unlock()
	d.letGo()
	d.stop()
	d.mu.Lock()
	if d.view != nil {
		d.view.ix.Close()
		d.view = nil
	}
	d.mu.Unlock()
	d.state.Store(uint32(StateOff))
	m.changed()
}

// driveCame takes up a drive that is there now: one that was away, or
// one not seen before, as the drives found at the start are.
func (m *Manager) driveCame(root string) {
	info, ok := driveInfo(root)
	if !ok {
		return
	}
	k := rootKey(root)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	d := m.drives[k]
	switch {
	case d == nil:
		d = &drive{m: m, info: info, key: keyOf(info.Root), again: make(chan struct{}, 1)}
		m.drives[k] = d
		m.order = append(m.order, k)
		extra := slices.ContainsFunc(m.cfg.Extra, func(r string) bool { return rootKey(r) == k })
		off := slices.ContainsFunc(m.cfg.Off, func(r string) bool { return rootKey(r) == k })
		if off || !info.Fixed && !extra {
			go m.changed()
			return
		}
	case !d.away || d.cancel != nil:
		return
	}
	// Another stick may have the letter now.
	d.reread.Store(d.away)
	d.away, d.info = false, info
	m.startDrive(d)
	go m.changed()
}

// idle reports whether the user last searched a while ago.
func (m *Manager) idle() bool {
	return time.Since(time.Unix(0, m.searched.Load())) >= restAfter
}

// rest gives the memory of the indexes' files back.
func (m *Manager) rest() {
	m.rested.Store(true)
	m.mu.Lock()
	drives := make([]*drive, 0, len(m.drives))
	for _, d := range m.drives {
		drives = append(drives, d)
	}
	folders := slices.Clone(m.folders)
	m.mu.Unlock()
	for _, d := range drives {
		d.mu.RLock()
		if d.view != nil {
			d.view.ix.Rest()
		}
		d.mu.RUnlock()
	}
	for _, f := range folders {
		select {
		case <-f.ready:
			f.mu.Lock()
			if f.view != nil {
				f.view.ix.Rest()
			}
			f.mu.Unlock()
		default:
		}
	}
}

// noteSearch notes that a search runs, and returns what is to run when
// it is done. A search the user did not ask for, run again as the files
// changed, does not keep the indexes' memory: with the user away, what it
// brought in goes back at once.
func (m *Manager) noteSearch(req *Request) (done func()) {
	if !req.Background {
		m.searched.Store(time.Now().UnixNano())
		m.rested.Store(false)
		return func() {}
	}
	return func() {
		if m.idle() {
			m.rest()
		} else {
			m.rested.Store(false)
		}
	}
}

// Found is what a search found.
type Found struct {
	Results []Result
	// Total counts all that matches, of which Results are the best.
	Total int
	// Reading is set while a drive the search would look in is still
	// being read for the first time: there is more to come.
	Reading bool
	// Uncovered is set when no index covers the folder searched in.
	Uncovered bool
}

// Search finds what a request asks for in the indexes of the drives it
// concerns: the drive of its scope, or all of them.
func (m *Manager) Search(ctx context.Context, req *Request) Found {
	var f Found
	defer m.noteSearch(req)()
	m.mu.Lock()
	var drives []*drive
	for _, k := range m.order {
		d := m.drives[k]
		if req.Scope != "" {
			if _, ok := relTo(d.info.Root, req.Scope); !ok {
				continue
			}
		}
		drives = append(drives, d)
	}
	m.mu.Unlock()
	covered := false
	for _, d := range drives {
		switch State(d.state.Load()) {
		case StateLoading, StateReading:
			f.Reading = true
		}
		d.mu.RLock()
		if d.view != nil {
			covered = true
			res, total := d.view.Search(ctx, req)
			f.Results = append(f.Results, res...)
			f.Total += total
		}
		d.mu.RUnlock()
		if ctx.Err() != nil {
			return Found{}
		}
	}
	f.Uncovered = req.Scope != "" && !covered
	var dropped int
	f.Results, dropped = m.byTargets(req, f.Results)
	f.Total -= dropped
	if len(drives) > 1 {
		slices.SortFunc(f.Results, func(a, b Result) int { return Compare(&a, &b) })
		if len(f.Results) > req.Limit && req.Limit > 0 {
			f.Results = f.Results[:req.Limit]
		}
	}
	return f
}

// relTo is a path's part under a root, and whether it is under it.
func relTo(root, path string) (string, bool) {
	root = strings.TrimRight(root, `\`)
	if len(path) < len(root) || !strings.EqualFold(path[:len(root)], root) {
		return "", false
	}
	rest := path[len(root):]
	if rest != "" && rest[0] != '\\' {
		return "", false
	}
	return strings.Trim(rest, `\`), true
}

// folderTTL is how long the index of a folder made for a search is kept.
const folderTTL = 90 * time.Second

// SearchFolder searches a folder no drive's index covers: the folder is
// read, which takes as long as it takes, and an index of it kept a while
// for the searches that follow.
func (m *Manager) SearchFolder(ctx context.Context, req *Request) (Found, error) {
	return m.searchFolder(ctx, req, false)
}

func (m *Manager) searchFolder(ctx context.Context, req *Request, again bool) (Found, error) {
	key := rootKey(strings.TrimRight(req.Scope, `\`) + `\`)
	defer m.noteSearch(req)()
	m.mu.Lock()
	var fi *folderIndex
	kept := m.folders[:0]
	for _, f := range m.folders {
		select {
		case <-f.ready:
			if time.Since(time.Unix(0, f.at.Load())) > folderTTL {
				// Not under m.mu: a search may still read it.
				go f.drop()
				continue
			}
		default:
		}
		kept = append(kept, f)
		if f.path == key {
			fi = f
		}
	}
	m.folders = kept
	fresh := fi == nil
	if fresh {
		fi = &folderIndex{path: key, ready: make(chan struct{})}
		m.folders = append(m.folders, fi)
	}
	rules := m.rules
	m.mu.Unlock()
	if fresh {
		// Read whether or not this search waits for it: the next one may.
		go func() {
			defer close(fi.ready)
			root := strings.TrimRight(req.Scope, `\`) + `\`
			sc := NewScratch(m.cfg.Dir)
			defer sc.Close()
			scans, rootID, err := Walk(context.Background(), root, WalkOptions{Scratch: sc})
			if err == nil {
				var ix *Index
				if m.cfg.Dir != "" && os.MkdirAll(m.cfg.Dir, 0o755) == nil {
					// In a file of its own, which goes with the index.
					fi.file = filepath.Join(m.cfg.Dir, "tmp-"+keyOf(key)+"-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".idx")
					ix, _ = BuildFile(fi.file, root, rootID, scans, false, Meta{}, sc)
				}
				if ix == nil {
					ix = Build(root, rootID, scans, false)
				}
				fi.view = NewView(ix, rules)
			}
			fi.at.Store(time.Now().UnixNano())
		}()
	}
	select {
	case <-fi.ready:
	case <-ctx.Done():
		return Found{}, ctx.Err()
	}
	fi.mu.RLock()
	defer fi.mu.RUnlock()
	if fi.dropped {
		// Let go of between finding it and here: read again.
		if again {
			return Found{}, errors.New("index: the folder could not be read")
		}
		return m.searchFolder(ctx, req, true)
	}
	if fi.view == nil {
		return Found{}, errors.New("index: the folder could not be read")
	}
	// In the folder's own index the folder is the root.
	sub := *req
	sub.Scope = fi.view.ix.Root
	// Used now: kept that much longer.
	fi.at.Store(time.Now().UnixNano())
	res, total := fi.view.Search(ctx, &sub)
	// The caller's own: what a view found it may hand out again.
	res, dropped := m.byTargets(req, slices.Clone(res))
	return Found{Results: res, Total: total - dropped}, nil
}

// linkTarget is what a shortcut was read to point at, when it was last
// changed at a time.
type linkTarget struct {
	modified uint32
	target   lnk.Target
	ok       bool
}

// target reads what a shortcut found points at, once for as long as the
// shortcut is not changed.
func (m *Manager) target(r *Result) (lnk.Target, bool) {
	if v, ok := m.links.Load(r.Path); ok {
		if t := v.(linkTarget); t.modified == r.Modified {
			return t.target, t.ok
		}
	}
	t, ok := lnk.Read(r.Path)
	m.links.Store(r.Path, linkTarget{r.Modified, t, ok})
	return t, ok
}

// byTargets leaves out, of results a kind of file was asked of, the
// shortcuts to what is not of the kind: a shortcut is let through by the
// search whatever it is called, and is a document, a folder or a program
// as what it points at is. It returns how many it left out.
func (m *Manager) byTargets(req *Request, res []Result) ([]Result, int) {
	filter := req.Query.Filter.And(req.Filter)
	if !filter.Links() {
		return res, 0
	}
	kept := res[:0]
	for i := range res {
		r := res[i]
		name := []byte(r.Name)
		if !r.IsDir() && search.IsLink(name) && !filter.AdmitsTarget(name, false) {
			t, ok := m.target(&r)
			switch {
			case !ok || t.Path == "":
				// One that names no path, as those of programs from the
				// Store: a program.
				ok = filter.AdmitsProgram()
			default:
				ok = filter.AdmitsTarget([]byte(filepath.Base(t.Path)), t.Dir)
			}
			if !ok {
				continue
			}
		}
		kept = append(kept, r)
	}
	return kept, len(res) - len(kept)
}

// Standing is what the rules, and the file system, make of a file.
type Standing struct {
	// Low is set for a file the rules put after the others, Excluded for
	// one they leave out, and Hidden for one that is hidden or in a
	// hidden folder.
	Low, Excluded, Hidden bool
}

// Lookup is a file as the index of its drive has it, by its path.
func (m *Manager) Lookup(path string) (Result, Standing, bool) {
	m.mu.Lock()
	drives := make([]*drive, 0, len(m.drives))
	for _, d := range m.drives {
		if _, in := relTo(d.info.Root, path); in {
			drives = append(drives, d)
		}
	}
	m.mu.Unlock()
	for _, d := range drives {
		d.mu.RLock()
		if v := d.view; v != nil {
			if ref, ok := v.Resolve(path); ok && ref != 0 {
				r := v.resultOf(ref)
				marks := v.marksOf(ref)
				d.mu.RUnlock()
				return r, Standing{marks&markLow != 0, marks&markExcluded != 0, marks&markHidden != 0}, true
			}
		}
		d.mu.RUnlock()
	}
	return Result{}, Standing{}, false
}

// Forget lets go of the index of a folder read for a search, when a path
// is in it: what changed there it would not hear of. It reports whether
// there was one.
func (m *Manager) Forget(path string) bool {
	m.mu.Lock()
	var gone []*folderIndex
	kept := m.folders[:0]
	for _, f := range m.folders {
		if _, ok := relTo(f.path, path); ok {
			gone = append(gone, f)
			continue
		}
		kept = append(kept, f)
	}
	m.folders = kept
	m.mu.Unlock()
	for _, f := range gone {
		go func() {
			<-f.ready
			f.drop()
		}()
	}
	return len(gone) > 0
}

// Close stops following the drives, and writes the indexes that changed.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	drives := make([]*drive, 0, len(m.drives))
	for _, d := range m.drives {
		drives = append(drives, d)
	}
	m.mu.Unlock()
	for _, d := range drives {
		d.stop()
	}
	select {
	case <-m.quit:
	default:
		close(m.quit)
	}
	m.wg.Wait()
	m.mu.Lock()
	folders := m.folders
	m.folders = nil
	m.mu.Unlock()
	for _, f := range folders {
		<-f.ready
		f.drop()
	}
}

func (d *drive) fail(err error) {
	msg := err.Error()
	d.err.Store(&msg)
	if d.view == nil {
		d.state.Store(uint32(StateFailed))
	}
	d.m.changed()
}

// run brings a drive's index up and keeps it up to date, until ctx ends.
func (d *drive) run(ctx context.Context) {
	m := d.m
	defer d.live.Store(false)
	var j follower
	// feed tells what changes on a drive that keeps no journal, from here
	// on: before the drive is read, so that nothing between is missed.
	var feed *pathFeed
	if !d.info.Journal {
		if feed = newPathFeed(d.info.Root); feed != nil {
			defer d.hold(feed.Close)()
		}
	}
	// A drive that can be taken out tells when it is asked for.
	if !d.info.Fixed && len(d.info.Root) == 3 {
		if guard := watchEject(d.info.Root, func() { d.letGo(); go m.driveWent(d.info.Root) }); guard != nil {
			defer func() { go guard.Close() }()
		}
	}
	unhold := func() {}
	defer func() {
		unhold()
		if j != nil {
			j.Close()
		}
		if feed != nil {
			feed.Close()
		}
		d.save(true)
	}()
	fresh := false
	if ix, err := m.store.Load(d.key); err == nil {
		// One written before files without the system's names were given
		// numbers to find them by cannot be followed: read again.
		if ix.Meta.Serial == d.info.Serial && strings.EqualFold(ix.Root, d.info.Root) && (len(ix.byRec) > 0 || ix.n <= 1) {
			v := NewView(ix, m.currentRules())
			d.mu.Lock()
			d.view, d.usn = v, ix.Meta.NextUSN
			d.mu.Unlock()
			d.saved = time.Now()
			d.state.Store(uint32(StateReady))
			m.changed()
		} else {
			ix.Close()
		}
	}
	for ctx.Err() == nil {
		if j == nil && d.info.Journal {
			if j, _ = openFollower(d.info.Root); j != nil {
				unhold = d.hold(j.Close)
			}
		}
		need := d.view == nil
		if d.reread.Swap(false) && j == nil {
			need = true
		}
		if !need && !fresh {
			meta := d.view.ix.Meta
			switch {
			case j != nil:
				// Up to date with a journal that still reaches back there.
				id, first := j.Position()
				need = meta.JournalID != id || meta.NextUSN < first || meta.NextUSN == 0
			default:
				// Nothing tells what changed: read again what is not
				// from this hour.
				need = time.Since(time.Unix(meta.Built, 0)) > staleAfter
			}
		}
		if need {
			if feed != nil {
				// What changed before the reading starts, the reading has.
				feed.take()
			}
			if err := d.read(ctx, j); err != nil {
				if ctx.Err() != nil {
					return
				}
				d.fail(err)
			} else {
				d.err.Store(nil)
			}
		}
		fresh = true
		// Follow the drive until it must be read again.
		again := false
		if j == nil && feed != nil && d.view != nil {
			again = d.followPaths(ctx, feed, need)
		} else {
			again = d.follow(ctx, j)
		}
		if !again {
			return
		}
		fresh = false
		if j != nil {
			unhold()
			unhold = func() {}
			j.Close()
			j = nil
		}
		d.mu.Lock()
		if d.view != nil {
			// Read again whatever the index says of itself.
			d.view.ix.Meta.NextUSN, d.view.ix.Meta.Built = 0, 0
		}
		d.mu.Unlock()
	}
}

// hold notes what closes something the index has open on the drive, and
// returns what forgets it.
func (d *drive) hold(close func()) (forget func()) {
	d.holdMu.Lock()
	defer d.holdMu.Unlock()
	if d.holds == nil {
		d.holds = map[int]func(){}
	}
	d.held++
	n := d.held
	d.holds[n] = close
	return func() {
		d.holdMu.Lock()
		delete(d.holds, n)
		d.holdMu.Unlock()
	}
}

// letGo closes what the index has open on the drive, at once.
func (d *drive) letGo() {
	d.holdMu.Lock()
	holds := make([]func(), 0, len(d.holds))
	for _, h := range d.holds {
		holds = append(holds, h)
	}
	d.holdMu.Unlock()
	for _, h := range holds {
		h()
	}
}

// staleAfter is how old an index nothing keeps up to date may be before
// its drive is read again: reading such a drive may take minutes.
const staleAfter = 6 * time.Hour

// read reads the drive whole and makes its index of what is found.
func (d *drive) read(ctx context.Context, j follower) error {
	m := d.m
	if d.view == nil {
		d.state.Store(uint32(StateReading))
	} else {
		d.refreshing.Store(true)
		defer d.refreshing.Store(false)
	}
	d.seen.Store(0)
	m.changed()
	// One drive of a kind at a time: each read takes the disks' time, and
	// room in the system's memory for what it found.
	lane := m.reading
	if !d.info.Journal {
		lane = m.readingSlow
	}
	select {
	case lane <- struct{}{}:
		defer func() { <-lane }()
	case <-ctx.Done():
		return ctx.Err()
	}
	start := time.Now()
	var meta Meta
	if j != nil {
		// What changes while the drive is read is in the journal from
		// here on.
		if err := j.Refresh(); err == nil {
			meta.JournalID, meta.NextUSN = j.Position2()
		}
	}
	// What is read, and the index made of it, are in files from the
	// first: the program's own memory holds neither.
	sc := NewScratch(m.cfg.Dir)
	defer sc.Close()
	scans, rootID, table, err := readDrive(ctx, d.info, &d.seen, sc)
	if err != nil {
		return err
	}
	meta.Serial, meta.FileSystem, meta.Table = d.info.Serial, d.info.FileSystem, table
	meta.Built, meta.Took = start.Unix(), time.Since(start).Milliseconds()
	if err := d.install(rootID, scans, meta, sc); err != nil {
		return err
	}
	d.state.Store(uint32(StateReady))
	m.changed()
	return nil
}

// install makes the index of what scans hold, in its file, and has it be
// the drive's.
func (d *drive) install(rootID uint64, scans []*Scan, meta Meta, sc *Scratch) error {
	m := d.m
	ix, err := m.store.Build(d.key, d.info.Root, rootID, scans, true, meta, sc)
	if err != nil {
		return err
	}
	d.saved = time.Now()
	v := NewView(ix, m.currentRules())
	d.mu.Lock()
	old := d.view
	d.view, d.usn = v, meta.NextUSN
	d.mu.Unlock()
	if old != nil {
		old.ix.Close()
	}
	// What reading took of the program's own memory goes back.
	debug.FreeOSMemory()
	return nil
}

// The pace of following a drive: changes are gathered for a moment, and
// the index is built again with them and written when much changed, or a
// while after something did. What is not written is not lost: the journal
// has it, and the index catches up from it when the program starts.
const (
	gatherFor    = 250 * time.Millisecond
	saveAfter    = 15 * time.Minute
	compactAfter = 50_000
)

// follow keeps the index up to date with the drive's journal, until ctx
// ends or the drive must be read again, which it reports.
func (d *drive) follow(ctx context.Context, j follower) (again bool) {
	if j == nil || d.view == nil {
		// Nothing tells of changes: wait to be asked, or for the index to
		// grow old.
		timer := time.NewTimer(staleAfter)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-d.again:
			return true
		case <-timer.C:
			return true
		}
	}
	type batch struct {
		changes []change
		next    int64
		err     error
	}
	usn := d.usn
	out := make(chan batch)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		wait := false
		for {
			changes, next, err := j.Read(usn, wait)
			if err != nil {
				select {
				case out <- batch{err: err}:
				case <-stop:
				}
				return
			}
			if len(changes) == 0 && next == usn {
				if !wait {
					// Caught up: from here on, as changes come.
					wait = true
					select {
					case out <- batch{next: next}:
					case <-stop:
						return
					}
				}
				continue
			}
			usn = next
			select {
			case out <- batch{changes: changes, next: next}:
			case <-stop:
				return
			}
			if wait {
				// More often follows at once: gathered, it is applied
				// together.
				select {
				case <-time.After(gatherFor):
				case <-stop:
					return
				}
			}
		}
	}()
	save := time.NewTicker(30 * time.Second)
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-d.again:
			return true
		case <-save.C:
			d.save(false)
		case b := <-out:
			if b.err != nil {
				if ctx.Err() != nil {
					return false
				}
				// The journal was reset, or what was asked is gone from
				// it: read the drive again.
				d.live.Store(false)
				select {
				case <-ctx.Done():
					return false
				case <-time.After(2 * time.Second):
				}
				return true
			}
			if len(b.changes) == 0 {
				d.live.Store(true)
				d.m.changed()
				continue
			}
			d.apply(j, b.changes, b.next)
		}
	}
}

// apply makes the index follow a batch of changes.
func (d *drive) apply(j follower, changes []change, next int64) {
	// A file changed several times is asked once, as it is now.
	last := make(map[uint64]int, len(changes))
	for i, c := range changes {
		last[c.id] = i
	}
	type step struct {
		id     uint64
		change Change
		ok     bool
	}
	steps := make([]step, 0, len(last))
	for i, c := range changes {
		if last[c.id] != i {
			continue
		}
		s := step{id: c.id}
		if !c.gone {
			if s.change, s.ok = j.Stat(c.id); s.ok {
				s.change.Parent = c.parent
			}
		}
		steps = append(steps, s)
	}
	rules := d.m.currentRules()
	touched := false
	d.mu.Lock()
	v := d.view
	for _, s := range steps {
		if !s.ok {
			touched = v.Remove(s.id) || touched
			continue
		}
		if v.Apply(s.change, rules) {
			touched = true
			continue
		}
		// In a folder the index does not know, as one made in this very
		// batch and told of after: the folders above it first.
		if d.adopt(j, s.change.Parent, rules, 0) && v.Apply(s.change, rules) {
			touched = true
		}
	}
	d.usn = next
	d.mu.Unlock()
	// Following read of the index's file: given back when next looked.
	d.m.rested.Store(false)
	// Written at once when much changed.
	d.save(false)
	if touched {
		d.m.changed()
	}
}

// adopt takes into the view a folder it does not know, and those above
// it. d.mu is held.
func (d *drive) adopt(j follower, id uint64, rules *Rules, depth int) bool {
	if depth > 64 {
		return false
	}
	c, ok := j.Stat(id)
	if !ok || c.Flags&FlagDir == 0 {
		return false
	}
	parent, ok := j.ParentOf(id)
	if !ok {
		return false
	}
	c.Parent = parent
	if d.view.Apply(c, rules) {
		return true
	}
	return d.adopt(j, parent, rules, depth+1) && d.view.Apply(c, rules)
}

// save builds the index again with what changed since it was written,
// and writes it: when much changed, or something did a while ago. With
// now, as when the program ends, it writes what is worth the wait.
func (d *drive) save(now bool) {
	d.mu.RLock()
	changes := 0
	if d.view != nil {
		changes = d.view.Changes()
	}
	d.mu.RUnlock()
	since := time.Since(d.saved)
	switch {
	case changes == 0:
		return
	case now && !d.info.Journal:
		// No journal tells the next run what this one saw.
	case changes >= compactAfter:
	case now && (changes >= 2000 || since >= saveAfter/2):
	case !now && since >= saveAfter:
	default:
		return
	}
	sc := NewScratch(d.m.cfg.Dir)
	defer sc.Close()
	d.mu.RLock()
	scan := d.view.rescan(sc)
	meta := d.view.ix.Meta
	rootID := d.view.ix.id[0]
	meta.NextUSN = d.usn
	d.mu.RUnlock()
	// What changes while the file is written is in the journal from
	// where the view was: none does, as this goroutine applies it.
	_ = d.install(rootID, []*Scan{scan}, meta, sc)
}

// change is a file the journal says changed.
type change struct {
	id, parent uint64
	gone       bool
}

// follower is the journal of a drive's changes.
type follower interface {
	// Position is the journal's identity and where it starts.
	Position() (id uint64, first int64)
	// Position2 is its identity and where it will write next, as of the
	// last Refresh.
	Position2() (id uint64, next int64)
	Refresh() error
	Read(from int64, wait bool) ([]change, int64, error)
	Stat(id uint64) (Change, bool)
	// ParentOf is the folder a file is in, by what the file system calls
	// them.
	ParentOf(id uint64) (uint64, bool)
	Close()
}
