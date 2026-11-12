package app

import (
	"fmt"
	"hash/maphash"
	"image"
	"maps"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
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

// thumbWorkers is how many threads make pictures of files' contents,
// thumbMinSize the smallest icons, in DIPs, that show them, and thumbsMax
// how many a size keeps.
const (
	thumbWorkers = 2
	thumbMinSize = 48
	thumbsMax    = 400
)

// pictured are the extensions of the files whose content shows in place
// of their icon: pictures and videos.
var pictured = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".bmp": true, ".webp": true,
	".tif": true, ".tiff": true, ".heic": true, ".heif": true, ".avif": true, ".jxl": true,
	".ico": true, ".svg": true, ".psd": true, ".raw": true, ".cr2": true, ".nef": true, ".arw": true, ".dng": true,
	".mp4": true, ".mkv": true, ".mov": true, ".avi": true, ".webm": true, ".wmv": true, ".m4v": true, ".flv": true,
	".pdf": true,
}

// dirClass is the key of the icon every plain directory shows, which no
// extension can be.
const dirClass = "/"

// iconWait is how long a folder that was read waits, on the main thread,
// for the icons and type names of its first rows that are not known yet:
// what comes within it shows in the folder's first frame, where it would
// replace a stand-in a frame later, which the eye takes for a flicker.
// Types seen before are known from the start, so this is spent on new
// ones and on icons of entries of their own, as programs'.
var iconWait = 10 * time.Millisecond

// saveDelay is how long after something new was learned of a type it is
// written to disk: once for all that comes meanwhile.
const saveDelay = 2 * time.Second

// iconRequest asks for the icon of a class of entries (a type) or of one
// entry at a size, and typeOf, when set, for the name of a type instead.
type iconRequest struct {
	key    string
	path   string
	attr   fsys.Attr
	size   int
	byType bool
	typeOf bool
	// thumb asks for a picture of the file's content instead.
	thumb bool
	// check asks again for what is known from disk already: nothing
	// waits for its answer.
	check bool
	// batch is the gathering the request is part of, 0 for none.
	batch uint64
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
	// kept holds the pictures of the icons of types that go to disk, and
	// dirty is set when the file is behind them.
	kept  map[string]*image.RGBA
	dirty bool
	// file and dir stand in for icons not fetched yet.
	file, dir *ui.Bitmap
	// thumbs holds the pictures of files' contents, by path and time of
	// change, nil for files that have none; thumbAsked those asked for.
	thumbs     map[string]*ui.Bitmap
	thumbAsked map[string]bool
}

// icons keeps the icons and type names the views show. Rows ask as they
// are built, so only what comes into view is ever asked for; what is not
// known yet shows a stand-in and is fetched on other threads, the most
// recently asked first, since that is what the user is looking at.
//
// The names and the small icons of types are kept on disk between runs,
// and a folder's first frame waits a moment for the rest (await), so that
// little is left to change once the folder shows.
//
// The maps belong to the main thread. The queue is shared.
type icons struct {
	// scale is how many pixels a DIP is.
	scale float64
	sets  map[int]*iconSet
	// bitmaps holds one bitmap per picture, and same one per picture of
	// the same pixels: many keys share one, and a picture read from disk
	// is the one the system gives later.
	bitmaps map[*image.RGBA]*ui.Bitmap
	same    map[pixelsKey]*ui.Bitmap
	seed    maphash.Seed
	types   map[string]string
	askedOf map[string]bool
	// epoch counts the icons that took the place of others: rows holding
	// icons of an earlier count ask anew.
	epoch int

	// store keeps names and icons on disk, nil for none. namesDirty is
	// set when its names are behind, and saving while a write is due.
	store      *iconStore
	namesDirty bool
	saving     bool

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
	// thumbQueue are the pictures asked for, which workers of their own
	// make: reading a large picture must not hold icons back.
	thumbWake  *sync.Cond
	thumbQueue []iconRequest
	// batch numbers the gatherings (await); while gathering is set the
	// requests made are of the batch, awaited counts those of it not
	// answered yet, and ready is closed once none is left.
	batch     uint64
	gathering bool
	awaited   int
	ready     chan struct{}

	// fits holds the pictures made to fit a size (fit).
	fitMu sync.Mutex
	fits  map[fitKey]*image.RGBA
}

// pixelsKey tells pictures apart by their content.
type pixelsKey struct {
	w, h int
	sum  uint64
}

// newIcons makes the icons of a screen of a scale. store, when not nil,
// keeps what is learned of types on disk.
func newIcons(scale float64, post func(func()), changed func(), store *iconStore) *icons {
	if scale <= 0 {
		scale = 1
	}
	ic := &icons{
		scale:   scale,
		sets:    map[int]*iconSet{},
		bitmaps: map[*image.RGBA]*ui.Bitmap{},
		same:    map[pixelsKey]*ui.Bitmap{},
		seed:    maphash.MakeSeed(),
		types:   map[string]string{},
		askedOf: map[string]bool{},
		fits:    map[fitKey]*image.RGBA{},
		store:   store,
		post:    post,
		changed: changed,
	}
	if store != nil {
		ic.types = store.names()
	}
	ic.wake = sync.NewCond(&ic.mu)
	ic.thumbWake = sync.NewCond(&ic.mu)
	for range iconWorkers {
		go ic.work(shell.NewWorker(), &ic.queue, ic.wake)
	}
	for range thumbWorkers {
		go ic.work(shell.NewWorker(), &ic.thumbQueue, ic.thumbWake)
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
		kept:  map[string]*image.RGBA{},

		thumbs:     map[string]*ui.Bitmap{},
		thumbAsked: map[string]bool{},
	}
	ic.sets[size] = s
	if s.stored() {
		// What the last run knew of types, there from the first frame.
		for key, img := range ic.store.icons(s.px) {
			s.byKey[key], s.kept[key] = ic.bitmap(img), img
		}
		s.dir, s.file = s.byKey[dirClass], s.byKey[""]
	}
	s.ask(iconRequest{key: dirClass, path: "folder", attr: fsys.AttrDir, byType: true})
	s.ask(iconRequest{key: "", path: "file", byType: true})
	return s
}

// work takes the requests of a queue, the newest first, and answers them
// on a thread of the system's liking.
func (ic *icons) work(w *shell.Worker, queue *[]iconRequest, wake *sync.Cond) {
	for {
		ic.mu.Lock()
		for len(*queue) == 0 {
			wake.Wait()
		}
		req := (*queue)[len(*queue)-1]
		*queue = (*queue)[:len(*queue)-1]
		ic.mu.Unlock()

		done := make(chan iconResult, 1)
		w.Do(func() {
			res := iconResult{req: req}
			switch {
			case req.typeOf:
				res.name = shell.TypeName(req.path, req.attr&fsys.AttrDir != 0)
			case req.thumb:
				res.img, _ = shell.Thumbnail(req.path, req.size)
			default:
				res.img, _ = shell.Icon(req.path, req.attr, req.size, req.byType)
				res.img = ic.fit(res.img, req.size)
			}
			done <- res
		})
		res := <-done

		ic.mu.Lock()
		ic.results = append(ic.results, res)
		post := !ic.posted
		ic.posted = true
		if req.batch != 0 && req.batch == ic.batch {
			ic.awaited--
			if ic.awaited == 0 && !ic.gathering && ic.ready != nil {
				close(ic.ready)
				ic.ready = nil
			}
		}
		ic.mu.Unlock()
		if post {
			ic.post(ic.deliver)
		}
	}
}

// await runs ask, which asks for the icons and names a view is about to
// show, and waits until those not known yet are, or limit passed: on the
// main thread, for the view's first frame to have them. What is known
// already, from this run or from disk, is not waited for.
func (ic *icons) await(limit time.Duration, ask func()) {
	ready := make(chan struct{})
	ic.mu.Lock()
	ic.batch++
	ic.awaited, ic.gathering, ic.ready = 0, true, ready
	ic.mu.Unlock()
	ask()
	ic.mu.Lock()
	ic.gathering = false
	waiting := ic.awaited > 0
	if !waiting {
		ic.ready = nil
	}
	ic.mu.Unlock()
	if !waiting {
		return
	}
	timer := time.NewTimer(limit)
	select {
	case <-ready:
		timer.Stop()
	case <-timer.C:
	}
	ic.deliver()
}

// deliver takes what the workers fetched into the maps, on the main
// thread: once for all that came since it was posted. The windows draw
// again only when something is not what they show.
func (ic *icons) deliver() {
	ic.mu.Lock()
	results := ic.results
	ic.results = nil
	ic.posted = false
	ic.mu.Unlock()
	changed := false
	for _, r := range results {
		if r.req.typeOf {
			// Empty when the system has no name for it: typeName makes one.
			name := r.name
			if old, ok := ic.types[r.req.key]; !ok || old != name {
				ic.types[r.req.key] = name
				changed = true
				if ic.store != nil && (ok || len(ic.types) <= storedKeysMax) {
					ic.namesDirty = true
					ic.saveSoon()
				}
			}
			continue
		}
		for _, s := range ic.sets {
			switch {
			case s.px != r.req.size:
			case r.req.thumb:
				// A picture is its file's alone: no other key shares it.
				var b *ui.Bitmap
				if r.img != nil {
					b = ui.NewBitmap(r.img)
				}
				s.thumbs[r.req.key] = b
				changed = true
			default:
				changed = s.took(r.req.key, r.img) || changed
			}
		}
	}
	if changed && ic.changed != nil {
		ic.changed()
	}
}

// took keeps an icon that was fetched, and tells whether it is another
// than the one the key had.
func (s *iconSet) took(key string, img *image.RGBA) bool {
	ic := s.ic
	b := ic.bitmap(img)
	old, had := s.byKey[key]
	if had && old == b {
		return false
	}
	if !had && isOwn(key) {
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
	if had {
		// Rows may hold the one it replaces.
		ic.epoch++
		if tracing {
			fmt.Fprintf(os.Stderr, "trace icon of %q at %d px is another than it was\n", key, s.px)
		}
	}
	switch key {
	case dirClass:
		s.dir = b
	case "":
		s.file = b
	}
	if _, was := s.kept[key]; s.stored() && !isOwn(key) && (was || len(s.kept) < storedKeysMax) {
		s.kept[key], s.dirty = img, true
		ic.saveSoon()
	}
	return true
}

// stored tells whether the icons of types of the size are kept on disk.
func (s *iconSet) stored() bool {
	return s.ic.store != nil && s.px <= storedIconMax
}

// fit is a picture no larger than px pixels. The system's large icons
// are 256 pixels wide whatever is asked for, a quarter of a megabyte
// each, where a view shows them at a fraction of that: made as small as
// they show, they are sharper, lighter, and the same from run to run,
// which lets them be kept on disk. Workers call it.
func (ic *icons) fit(img *image.RGBA, px int) *image.RGBA {
	if img == nil || px <= 0 || img.Rect.Dx() <= px && img.Rect.Dy() <= px {
		return img
	}
	key := fitKey{img, px}
	ic.fitMu.Lock()
	out := ic.fits[key]
	ic.fitMu.Unlock()
	if out != nil {
		return out
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	scale := float64(px) / float64(max(w, h))
	out = image.NewRGBA(image.Rect(0, 0, max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))))
	draw.CatmullRom.Scale(out, out.Rect, img, img.Rect, draw.Src, nil)
	ic.fitMu.Lock()
	defer ic.fitMu.Unlock()
	if first := ic.fits[key]; first != nil {
		// Another worker made it meanwhile: its picture is the one.
		return first
	}
	ic.fits[key] = out
	return out
}

// fitKey names a picture made to fit a size.
type fitKey struct {
	img *image.RGBA
	px  int
}

// saveSoon writes what was learned of types to disk a moment from now.
func (ic *icons) saveSoon() {
	if ic.saving {
		return
	}
	ic.saving = true
	time.AfterFunc(saveDelay, func() { ic.post(ic.save) })
}

// save hands what is behind on disk to another thread to write.
func (ic *icons) save() {
	ic.saving = false
	var names map[string]string
	if ic.namesDirty {
		ic.namesDirty = false
		names = maps.Clone(ic.types)
	}
	sets := map[int]map[string]*image.RGBA{}
	for _, s := range ic.sets {
		if s.dirty {
			s.dirty = false
			sets[s.px] = maps.Clone(s.kept)
		}
	}
	if names == nil && len(sets) == 0 {
		return
	}
	go ic.store.save(names, sets)
}

// isOwn tells the key of an entry's own icon, a path, from a type's.
func isOwn(key string) bool {
	return len(key) > 1 && key[0] != '.'
}

// bitmap is the bitmap of a picture, nil for none: the same one for
// pictures of the same pixels.
func (ic *icons) bitmap(img *image.RGBA) *ui.Bitmap {
	if img == nil {
		return nil
	}
	if b, ok := ic.bitmaps[img]; ok {
		return b
	}
	key := pixelsKey{w: img.Rect.Dx(), h: img.Rect.Dy(), sum: maphash.Bytes(ic.seed, img.Pix)}
	b, ok := ic.same[key]
	if !ok {
		b = ui.NewBitmap(img)
		ic.same[key] = b
	}
	ic.bitmaps[img] = b
	return b
}

func (ic *icons) enqueue(req iconRequest) {
	ic.mu.Lock()
	if ic.gathering && !req.check {
		req.batch = ic.batch
		ic.awaited++
	}
	ic.queue = append(ic.queue, req)
	ic.mu.Unlock()
	ic.wake.Signal()
}

// ask asks the system for an icon, once. One known from disk is asked
// for all the same, to learn whether it is still the one.
func (s *iconSet) ask(req iconRequest) {
	if s.asked[req.key] {
		return
	}
	s.asked[req.key] = true
	req.size = s.px
	_, req.check = s.byKey[req.key]
	s.ic.enqueue(req)
}

// thumbnail is the picture of the content of an entry of dir, for the
// views whose icons are large enough to show one, and whether that is
// final: nil and true for an entry that has none, nil and false while it
// is being made.
func (s *iconSet) thumbnail(dir string, e *fsys.Entry) (*ui.Bitmap, bool) {
	if s.size < thumbMinSize || !real(dir) || e.IsDir() || e.Attr&fsys.AttrOffline != 0 || !pictured[e.Ext()] {
		return nil, true
	}
	path := fsys.Join(dir, e.Name)
	// A file written again is another picture.
	key := path + "|" + strconv.FormatInt(e.ModTime, 36)
	if b, ok := s.thumbs[key]; ok {
		return b, true
	}
	if !s.thumbAsked[key] {
		if len(s.thumbAsked) >= thumbsMax {
			// Enough kept: those in view are made again, from the
			// system's cache this time.
			clear(s.thumbs)
			clear(s.thumbAsked)
		}
		s.thumbAsked[key] = true
		ic := s.ic
		ic.mu.Lock()
		ic.thumbQueue = append(ic.thumbQueue, iconRequest{key: key, path: path, size: s.px, thumb: true})
		ic.mu.Unlock()
		ic.thumbWake.Signal()
	}
	return nil, false
}

// forEntry is the icon of an entry of dir, and whether it is the final
// one: a stand-in comes with false while the real icon is fetched, and
// the row asks again in a later frame.
func (s *iconSet) forEntry(dir string, e *fsys.Entry) (*ui.Bitmap, bool) {
	if dir == fsys.Computer {
		return s.forPath(e.Name, e.Attr)
	}
	if !real(dir) {
		// Of the Recycle Bin: its type's, by the extension it keeps.
		return s.forType(e)
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
	b, ok := s.byKey[ext]
	if !s.asked[ext] {
		s.ask(iconRequest{key: ext, path: "file" + ext, byType: true})
	}
	switch {
	case !ok:
		return s.file, false
	case b == nil:
		return s.file, s.file != nil
	}
	return b, true
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

// typeName is what the system calls an entry's type, and nothing until
// the system answered: a guess shown meanwhile would change under the
// reader's eyes. A name known from disk is asked for all the same, to
// learn whether it is still the one.
func (ic *icons) typeName(e *fsys.Entry) string {
	key := dirClass
	if !e.IsDir() {
		key = e.Ext()
	}
	if key == "" {
		// The system has no name for files without an extension.
		return locales.TypeFile()
	}
	name, ok := ic.types[key]
	if !ic.askedOf[key] {
		ic.askedOf[key] = true
		req := iconRequest{key: key, path: key, attr: e.Attr & fsys.AttrDir, typeOf: true, check: ok}
		if e.IsDir() {
			req.path = ""
		}
		ic.enqueue(req)
	}
	if ok && name == "" {
		return guessType(key)
	}
	return name
}

// guessType names the type of a key the system has no name for.
func guessType(key string) string {
	if key == dirClass {
		return locales.TypeFolder()
	}
	return locales.TypeOfExtension(strings.ToUpper(strings.TrimPrefix(key, ".")))
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
