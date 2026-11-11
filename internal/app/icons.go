package app

import (
	"image"
	"math"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"cairn/internal/fsys"
	"cairn/internal/shell"
)

// iconSize is the side of the icons of rows, tabs and the sidebar, in
// DIPs. The views of larger icons ask for their own sizes.
const iconSize = 16

// iconWorkers is how many threads ask the system for icons at once.
const iconWorkers = 3

// ownIconsMax is how many icons of entries of their own a size keeps
// before it lets them all go: large ones weigh a quarter of a megabyte
// each, and a walk through folders of programs would keep them all.
// Those in view are fetched again.
const ownIconsMax = 600

// dirClass is the key of the icon every plain directory shows, which no
// extension can be.
const dirClass = "/"

// iconRequest asks for the icon of a class of entries (a type) or of one
// entry at a size, and typeOf, when set, for the name of a type instead.
type iconRequest struct {
	key    string
	path   string
	attr   fsys.Attr
	size   int
	byType bool
	typeOf bool
}

type iconResult struct {
	req  iconRequest
	img  *image.RGBA
	name string
}

// iconSet is the icons of one size.
type iconSet struct {
	ic *icons
	// size is the side in DIPs, and px the size asked of the system.
	size, px int
	// byKey holds the icons of types by their extension (dirClass for
	// directories, "" for files without one), and those of entries with
	// icons of their own by their path. own counts the latter.
	byKey map[string]*ui.Bitmap
	own   int
	asked map[string]bool
	// file and dir stand in for icons not fetched yet.
	file, dir *ui.Bitmap
}

// icons keeps the icons and type names the views show. Rows ask as they
// are built, so only what comes into view is ever asked for; what is not
// known yet shows a stand-in and is fetched on other threads, the most
// recently asked first, since that is what the user is looking at.
//
// The maps belong to the main thread. The queue is shared.
type icons struct {
	// scale is how many pixels a DIP is.
	scale float64
	sets  map[int]*iconSet
	// bitmaps holds one bitmap per picture: many keys share one.
	bitmaps map[*image.RGBA]*ui.Bitmap
	types   map[string]string
	askedOf map[string]bool

	// changed is called on the main thread after new icons or names
	// arrived, for the windows to draw again.
	changed func()
	// post runs a function on the main thread, without waiting for it.
	post func(func())

	mu      sync.Mutex
	wake    *sync.Cond
	queue   []iconRequest
	results []iconResult
	posted  bool
}

func newIcons(scale float64, post func(func()), changed func()) *icons {
	if scale <= 0 {
		scale = 1
	}
	ic := &icons{
		scale:   scale,
		sets:    map[int]*iconSet{},
		bitmaps: map[*image.RGBA]*ui.Bitmap{},
		types:   map[string]string{},
		askedOf: map[string]bool{},
		post:    post,
		changed: changed,
	}
	ic.wake = sync.NewCond(&ic.mu)
	for range iconWorkers {
		go ic.work(shell.NewWorker())
	}
	// The two icons most rows show, before the first row asks.
	ic.at(iconSize)
	return ic
}

// at is the icons of a size in DIPs.
func (ic *icons) at(size int) *iconSet {
	if s := ic.sets[size]; s != nil {
		return s
	}
	s := &iconSet{
		ic:    ic,
		size:  size,
		px:    int(math.Round(float64(size) * ic.scale)),
		byKey: map[string]*ui.Bitmap{},
		asked: map[string]bool{},
	}
	ic.sets[size] = s
	s.ask(iconRequest{key: dirClass, path: "folder", attr: fsys.AttrDir, byType: true})
	s.ask(iconRequest{key: "", path: "file", byType: true})
	return s
}

// work takes requests, the newest first, and answers them on a thread of
// the system's liking.
func (ic *icons) work(w *shell.Worker) {
	for {
		ic.mu.Lock()
		for len(ic.queue) == 0 {
			ic.wake.Wait()
		}
		req := ic.queue[len(ic.queue)-1]
		ic.queue = ic.queue[:len(ic.queue)-1]
		ic.mu.Unlock()

		done := make(chan iconResult, 1)
		w.Do(func() {
			res := iconResult{req: req}
			if req.typeOf {
				res.name = shell.TypeName(req.path, req.attr&fsys.AttrDir != 0)
			} else {
				res.img, _ = shell.Icon(req.path, req.attr, req.size, req.byType)
			}
			done <- res
		})
		res := <-done

		ic.mu.Lock()
		ic.results = append(ic.results, res)
		post := !ic.posted
		ic.posted = true
		ic.mu.Unlock()
		if post {
			ic.post(ic.deliver)
		}
	}
}

// deliver takes what the workers fetched into the maps, on the main
// thread: once for all that came since it was posted.
func (ic *icons) deliver() {
	ic.mu.Lock()
	results := ic.results
	ic.results = nil
	ic.posted = false
	ic.mu.Unlock()
	for _, r := range results {
		if r.req.typeOf {
			if r.name != "" {
				ic.types[r.req.key] = r.name
			}
			continue
		}
		for _, s := range ic.sets {
			if s.px == r.req.size {
				s.took(r.req.key, ic.bitmap(r.img))
			}
		}
	}
	if len(results) > 0 && ic.changed != nil {
		ic.changed()
	}
}

// took keeps an icon that was fetched.
func (s *iconSet) took(key string, b *ui.Bitmap) {
	if _, had := s.byKey[key]; !had && isOwn(key) {
		if s.own >= ownIconsMax {
			for k := range s.byKey {
				if isOwn(k) {
					delete(s.byKey, k)
					delete(s.asked, k)
				}
			}
			s.own = 0
		}
		s.own++
	}
	s.byKey[key] = b
	switch key {
	case dirClass:
		s.dir = b
	case "":
		s.file = b
	}
}

// isOwn tells the key of an entry's own icon, a path, from a type's.
func isOwn(key string) bool {
	return len(key) > 1 && key[0] != '.'
}

// bitmap is the bitmap of a picture, nil for none.
func (ic *icons) bitmap(img *image.RGBA) *ui.Bitmap {
	if img == nil {
		return nil
	}
	if b, ok := ic.bitmaps[img]; ok {
		return b
	}
	b := ui.NewBitmap(img)
	ic.bitmaps[img] = b
	return b
}

func (ic *icons) enqueue(req iconRequest) {
	ic.mu.Lock()
	ic.queue = append(ic.queue, req)
	ic.mu.Unlock()
	ic.wake.Signal()
}

func (s *iconSet) ask(req iconRequest) {
	if s.asked[req.key] {
		return
	}
	s.asked[req.key] = true
	req.size = s.px
	s.ic.enqueue(req)
}

// forEntry is the icon of an entry of dir, and whether it is the final
// one: a stand-in comes with false while the real icon is fetched, and
// the row asks again in a later frame.
func (s *iconSet) forEntry(dir string, e *fsys.Entry) (*ui.Bitmap, bool) {
	if dir == fsys.Computer {
		return s.forPath(e.Name, e.Attr)
	}
	if shell.OwnIcon(e.Name, e.Attr) {
		path := fsys.Join(dir, e.Name)
		b, ok := s.byKey[path]
		if ok && b != nil {
			return b, true
		}
		if !ok {
			s.ask(iconRequest{key: path, path: path, attr: e.Attr})
		}
		// The type's icon meanwhile, and for good when the entry has
		// none the system could give.
		b, final := s.forType(e)
		return b, ok && final
	}
	return s.forType(e)
}

// forType is the icon of an entry's type.
func (s *iconSet) forType(e *fsys.Entry) (*ui.Bitmap, bool) {
	if e.IsDir() {
		return s.dir, s.dir != nil
	}
	ext := e.Ext()
	if b, ok := s.byKey[ext]; ok {
		if b == nil {
			return s.file, s.file != nil
		}
		return b, true
	}
	s.ask(iconRequest{key: ext, path: "file" + ext, byType: true})
	return s.file, false
}

// forPath is the icon of a path of its own, as a drive's or a place's.
func (s *iconSet) forPath(path string, attr fsys.Attr) (*ui.Bitmap, bool) {
	b, ok := s.byKey[path]
	if ok && b != nil {
		return b, true
	}
	if !ok {
		s.ask(iconRequest{key: path, path: path, attr: attr})
	}
	if attr&fsys.AttrDir != 0 {
		return s.dir, ok && s.dir != nil
	}
	return s.file, ok && s.file != nil
}

// forPath is the small icon of a path of its own.
func (ic *icons) forPath(path string, attr fsys.Attr) (*ui.Bitmap, bool) {
	return ic.at(iconSize).forPath(path, attr)
}

// typeName is what the system calls an entry's type, a guess from the
// extension until the system answered.
func (ic *icons) typeName(e *fsys.Entry) string {
	key := dirClass
	if !e.IsDir() {
		key = e.Ext()
	}
	if key == "" {
		// The system has no name for files without an extension.
		return "File"
	}
	if name, ok := ic.types[key]; ok {
		return name
	}
	if !ic.askedOf[key] {
		ic.askedOf[key] = true
		req := iconRequest{key: key, path: key, attr: e.Attr & fsys.AttrDir, typeOf: true}
		if e.IsDir() {
			req.path = ""
		}
		ic.enqueue(req)
	}
	if e.IsDir() {
		return "File folder"
	}
	return strings.ToUpper(key[1:]) + " File"
}

// forget drops the icons of the entries of a folder, which a refresh
// fetches anew: a program replaced by another version shows its new icon.
func (ic *icons) forget(dir string) {
	for _, s := range ic.sets {
		for key := range s.byKey {
			if isOwn(key) && len(key) > len(dir) && fsys.Within(key, dir) {
				delete(s.byKey, key)
				delete(s.asked, key)
				s.own--
			}
		}
	}
}
