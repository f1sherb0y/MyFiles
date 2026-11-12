package watch

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Hub watches folders by the drives they are on: one watch a drive, from
// its root.
type Hub struct {
	mu      sync.Mutex
	volumes map[string]*volume
	closed  bool
}

// New makes a Hub.
func New() *Hub { return &Hub{volumes: map[string]*volume{}} }

// volume is the watch of a drive.
type volume struct {
	hub *Hub
	// name is the drive, as "D:".
	name   string
	handle windows.Handle
	// entries are the folders watched on the drive: replaced, never
	// changed, for the reader to go through them without a lock.
	entries atomic.Pointer[[]*entry]
	// closing tells the reader its failure to read is the end asked for.
	closing atomic.Bool
	stop    chan struct{}
	done    chan struct{}
	// linger closes the watch a while after its last folder left it: a
	// tab going from folder to folder leaves and comes back at once.
	linger *time.Timer
}

// entry is a folder watched.
type entry struct {
	vol    *volume
	notify func(Event)
	dead   atomic.Bool
	// exact tells that the path asked for is the folder's real one, with
	// no link on the way, so that its path after a rename can be told.
	exact bool
	// rel is the folder's real path under the root of its drive, without
	// slashes at its ends, and fold the same in lower case. The reader
	// alone reads and writes them.
	rel, fold []uint16
	// modified is when the folder was last seen changed, for the times
	// the changes are not read one by one.
	modified time.Time

	mu      sync.Mutex
	pending bool
	first   time.Time
	timer   *time.Timer
}

// floods counts the times a drive went into flood, for the tests.
var floods atomic.Int64

// lingerFor is how long the watch of a drive outlives its last folder.
const lingerFor = 2 * time.Second

// Watch tells notify of what happens to the folder at path, until cancel
// is called. notify runs on goroutines of the Hub's, one call at a time
// for Moved and Gone, and must not block. It reports false for a folder
// that is not the Hub's to watch: one of a network, of a drive that can
// be taken out, or of no drive at all. It reads the disk, and is not for
// the main thread.
func (h *Hub) Watch(path string, notify func(Event)) (cancel func(), ok bool) {
	if h == nil {
		return nil, false
	}
	name, rel, ok := resolve(path)
	if !ok {
		return nil, false
	}
	e := &entry{notify: notify, rel: units(rel), fold: folded(units(rel))}
	asked := strings.TrimRight(strings.TrimPrefix(path, `\\?\`), `\`)
	e.exact = strings.EqualFold(asked, strings.TrimRight(name+`\`+rel, `\`))
	if info, err := os.Stat(name + `\` + rel); err == nil {
		e.modified = info.ModTime()
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false
	}
	v := h.volumes[name]
	if v == nil {
		root, err := windows.UTF16PtrFromString(name + `\`)
		if err != nil {
			return nil, false
		}
		handle, err := windows.CreateFile(root, windows.FILE_LIST_DIRECTORY,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
			windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			return nil, false
		}
		v = &volume{hub: h, name: name, handle: handle, stop: make(chan struct{}), done: make(chan struct{})}
		empty := []*entry{}
		v.entries.Store(&empty)
		h.volumes[name] = v
		go v.run()
	}
	if v.linger != nil {
		v.linger.Stop()
		v.linger = nil
	}
	e.vol = v
	list := append(append([]*entry{}, *v.entries.Load()...), e)
	v.entries.Store(&list)

	var once sync.Once
	return func() { once.Do(func() { h.leave(e) }) }, true
}

// leave ends the watch of a folder.
func (h *Hub) leave(e *entry) {
	e.dead.Store(true)
	e.mu.Lock()
	if e.timer != nil {
		e.timer.Stop()
	}
	e.mu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	v := e.vol
	old := *v.entries.Load()
	list := make([]*entry, 0, len(old))
	for _, other := range old {
		if other != e {
			list = append(list, other)
		}
	}
	v.entries.Store(&list)
	if len(list) == 0 && h.volumes[v.name] == v && v.linger == nil {
		v.linger = time.AfterFunc(lingerFor, func() {
			h.mu.Lock()
			idle := h.volumes[v.name] == v && len(*v.entries.Load()) == 0
			if idle {
				delete(h.volumes, v.name)
			}
			h.mu.Unlock()
			if idle {
				v.close()
			}
		})
	}
}

// Close ends every watch.
func (h *Hub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.closed = true
	volumes := h.volumes
	h.volumes = map[string]*volume{}
	h.mu.Unlock()
	for _, v := range volumes {
		v.close()
	}
}

// close ends the watch of a drive and waits for its reader.
func (v *volume) close() {
	if v.closing.Swap(true) {
		<-v.done
		return
	}
	close(v.stop)
	for {
		// The reader may be about to read, with nothing yet to cancel.
		_ = windows.CancelIoEx(v.handle, nil)
		select {
		case <-v.done:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// resolve finds the drive a folder is really on, as "D:", and its path
// under the drive's root, following the links on the way to it. It
// reports false for what is on no drive that stays: a network, a drive
// that can be taken out.
func resolve(path string) (name, rel string, ok bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", "", false
	}
	h, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", "", false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil || n == 0 || int(n) >= len(buf) {
		return "", "", false
	}
	full := windows.UTF16ToString(buf[:n])
	// `\\?\D:\folder`; `\\?\UNC\server\share` is of a network.
	if len(full) < 6 || !strings.HasPrefix(full, `\\?\`) || full[5] != ':' {
		return "", "", false
	}
	name = strings.ToUpper(full[4:6])
	root, err := windows.UTF16PtrFromString(name + `\`)
	if err != nil || windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return "", "", false
	}
	return name, strings.Trim(full[6:], `\`), true
}

func units(s string) []uint16 {
	u, _ := windows.UTF16FromString(s)
	return u[:len(u)-1]
}

// fold is a character of a name in lower case, as names compare.
func fold(c uint16) uint16 {
	switch {
	case c < 0x80:
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		return c
	case c >= 0xD800 && c <= 0xDFFF:
		return c
	}
	if l := unicode.ToLower(rune(c)); l <= 0xFFFF {
		return uint16(l)
	}
	return c
}

func folded(u []uint16) []uint16 {
	out := make([]uint16, len(u))
	for i, c := range u {
		out[i] = fold(c)
	}
	return out
}

// relation is what the path of a change is to a folder watched.
type relation int

const (
	apart relation = iota
	// inside: of something the folder holds itself.
	inside
	// above: of the folder, or of a folder it is in.
	above
)

// relate tells what a changed path is to a folder, whose path is given in
// lower case. It makes nothing: most changes are of no folder watched.
func relate(changed, folder []uint16) relation {
	if len(changed) == 0 {
		return apart
	}
	n := min(len(changed), len(folder))
	for i := 0; i < n; i++ {
		if fold(changed[i]) != folder[i] {
			return apart
		}
	}
	switch {
	case len(changed) == len(folder):
		return above
	case len(changed) < len(folder):
		if folder[len(changed)] == '\\' {
			return above
		}
		return apart
	}
	rest := changed[len(folder):]
	if len(folder) > 0 {
		if rest[0] != '\\' {
			return apart
		}
		rest = rest[1:]
	}
	for _, c := range rest {
		if c == '\\' {
			return apart // deeper than what the folder shows
		}
	}
	return inside
}

const (
	actionAdded      = 1
	actionRemoved    = 2
	actionModified   = 3
	actionRenamedOld = 4
	actionRenamedNew = 5
)

// run reads the changes of the drive until the watch is closed.
func (v *volume) run() {
	defer close(v.done)
	defer windows.CloseHandle(v.handle)
	const mask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
		windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE
	buf := make([]byte, 64<<10)
	// renamed is the old name of something being renamed: its new one is
	// the next change told.
	var renamed []uint16
	var (
		flood    bool
		since    = time.Now()
		count    int
		overflow bool
	)
	for {
		if flood {
			// Not one by one: what the drive tells meanwhile, beyond what
			// it keeps, is dropped there, and costs nothing here.
			select {
			case <-v.stop:
				return
			case <-time.After(floodPause):
			}
		}
		var n uint32
		err := windows.ReadDirectoryChanges(v.handle, &buf[0], uint32(len(buf)), true, mask, &n, nil, 0)
		if v.closing.Load() {
			return
		}
		if err != nil && err != windows.ERROR_NOTIFY_ENUM_DIR {
			v.lost()
			return
		}
		now := time.Now()
		if now.Sub(since) > time.Second {
			// After a calm: the rate is of what comes now.
			since, count, overflow = now, 0, false
		}
		if err != nil || n == 0 {
			// More came than was kept: what changed is not known.
			overflow = true
			renamed = nil
			v.unsure(flood)
		} else {
			count += v.read(buf[:n], &renamed)
		}
		if took := now.Sub(since); took >= floodPause {
			rate := float64(count) / took.Seconds()
			switch {
			case !flood && (rate > floodRate || overflow):
				flood = true
				floods.Add(1)
			case flood && rate < calmRate && !overflow:
				flood = false
				// What the modification times did not tell, as files that
				// grew.
				for _, e := range *v.entries.Load() {
					e.changed()
				}
			}
			since, count, overflow = now, 0, false
		}
	}
}

// read tells the folders watched of the changes in a buffer read, and
// returns how many changes it held.
func (v *volume) read(buf []byte, renamed *[]uint16) int {
	entries := *v.entries.Load()
	count := 0
	for off := uint32(0); ; {
		info := (*windows.FileNotifyInformation)(unsafe.Pointer(&buf[off]))
		name := unsafe.Slice(&info.FileName, info.FileNameLength/2)
		count++
		action := info.Action
		for _, e := range entries {
			switch relate(name, e.fold) {
			case inside:
				e.changed()
			case above:
				switch action {
				case actionRemoved:
					e.tell(Event{Kind: Gone})
				case actionAdded:
					// Back, as a folder moved away and returned.
					e.changed()
				}
			}
		}
		switch action {
		case actionRenamedOld:
			*renamed = append((*renamed)[:0], name...)
		case actionRenamedNew:
			if old := *renamed; len(old) > 0 {
				for _, e := range entries {
					if relate(old, e.fold) != above {
						continue
					}
					// The folder is where it was under the new name.
					rel := append(append([]uint16{}, name...), e.rel[len(old):]...)
					e.rel, e.fold = rel, folded(rel)
					if e.exact {
						e.tell(Event{Kind: Moved, To: strings.TrimRight(v.name+`\`+windows.UTF16ToString(rel), `\`)})
					} else {
						e.tell(Event{Kind: Gone})
					}
				}
			}
			*renamed = (*renamed)[:0]
		}
		if info.NextEntryOffset == 0 {
			return count
		}
		off += info.NextEntryOffset
	}
}

// unsure tells the folders watched that they may have changed, when the
// changes of the drive were not all read. In a flood, which may last, it
// tells those only whose modification time changed, as it does when
// something is made, deleted or renamed in a folder.
func (v *volume) unsure(flood bool) {
	for _, e := range *v.entries.Load() {
		info, err := os.Stat(v.name + `\` + windows.UTF16ToString(e.rel))
		switch {
		case err != nil:
			e.tell(Event{Kind: Gone})
		case !flood || !info.ModTime().Equal(e.modified):
			e.modified = info.ModTime()
			e.changed()
		}
	}
}

// lost ends the watch of a drive that can be read no more.
func (v *volume) lost() {
	h := v.hub
	h.mu.Lock()
	if h.volumes[v.name] == v {
		delete(h.volumes, v.name)
	}
	v.closing.Store(true)
	h.mu.Unlock()
	for _, e := range *v.entries.Load() {
		e.tell(Event{Kind: Lost})
	}
}

// tell tells of what happened, unless the watch was cancelled.
func (e *entry) tell(ev Event) {
	if !e.dead.Load() {
		e.notify(ev)
	}
}

// changed tells that what the folder holds changed, once for the changes
// that come together.
func (e *entry) changed() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if !e.pending {
		e.pending, e.first = true, now
		e.timer = time.AfterFunc(quiet, e.fire)
		return
	}
	if now.Sub(e.first) < latest-quiet {
		e.timer.Reset(quiet)
	}
}

func (e *entry) fire() {
	e.mu.Lock()
	e.pending = false
	e.mu.Unlock()
	e.tell(Event{Kind: Changed})
}
