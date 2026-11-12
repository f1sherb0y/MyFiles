package app

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// storedIconMax is the side, in pixels, of the largest icons kept on disk:
// those of rows, lists, tiles and, on most screens, medium and large
// icons. Larger ones weigh a quarter of a megabyte each and more, and are
// asked of the system anew.
const storedIconMax = 128

// storedKeysMax is how many types a file keeps: a folder of files with
// extensions of their own each must not grow it without end.
const storedKeysMax = 4000

// iconStore keeps what the system said of types (their names and their
// small icons) on disk between runs, for the first frame of a folder to
// show them without asking. What it holds may be behind the system, which
// is asked all the same, off the main thread, the first time a type shows.
type iconStore struct {
	dir string
	// mu lets one write through at a time.
	mu sync.Mutex
}

// openIconStore is the store in the user's cache directory, or the one
// MYFILES_CACHE_DIR names; nil when there is no such place.
func openIconStore() *iconStore {
	dir := os.Getenv("MYFILES_CACHE_DIR")
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil
		}
		dir = filepath.Join(base, "MyFiles")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	return &iconStore{dir: dir}
}

// names are the names of types kept, by extension.
func (st *iconStore) names() map[string]string {
	names := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(st.dir, "types.json")); err == nil {
		_ = json.Unmarshal(data, &names)
	}
	return names
}

// icons are the icons of types kept for a size in pixels, by extension:
// nil for a type that has none of its own.
func (st *iconStore) icons(px int) map[string]*image.RGBA {
	data, err := os.ReadFile(st.iconFile(px))
	if err != nil {
		return nil
	}
	icons, err := decodeIcons(data)
	if err != nil {
		return nil
	}
	return icons
}

func (st *iconStore) iconFile(px int) string {
	return filepath.Join(st.dir, "icons-"+strconv.Itoa(px)+".bin")
}

// save writes the names, when given, and the icons of the sizes given.
func (st *iconStore) save(names map[string]string, icons map[int]map[string]*image.RGBA) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if names != nil {
		if data, err := json.Marshal(names); err == nil {
			_ = replaceFile(filepath.Join(st.dir, "types.json"), data)
		}
	}
	for px, set := range icons {
		_ = replaceFile(st.iconFile(px), encodeIcons(set))
	}
}

// replaceFile writes a file whole: a crash while writing leaves the old
// one.
func replaceFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// The file of a size's icons: a magic number, the pictures (width, height
// and pixels each), then the keys with the index of their picture, -1 for
// none. Many types share a picture, which is written once.
const iconMagic = 0x314e5243 // "CRN1"

func encodeIcons(icons map[string]*image.RGBA) []byte {
	le := binary.LittleEndian
	index := map[*image.RGBA]int32{}
	var pictures []*image.RGBA
	for _, img := range icons {
		if img == nil {
			continue
		}
		if _, ok := index[img]; !ok {
			index[img] = int32(len(pictures))
			pictures = append(pictures, img)
		}
	}
	out := le.AppendUint32(nil, iconMagic)
	out = le.AppendUint32(out, uint32(len(pictures)))
	for _, img := range pictures {
		w, h := img.Rect.Dx(), img.Rect.Dy()
		out = le.AppendUint16(out, uint16(w))
		out = le.AppendUint16(out, uint16(h))
		for y := range h {
			row := img.Pix[y*img.Stride:]
			out = append(out, row[:4*w]...)
		}
	}
	out = le.AppendUint32(out, uint32(len(icons)))
	for key, img := range icons {
		out = le.AppendUint16(out, uint16(len(key)))
		out = append(out, key...)
		at := int32(-1)
		if img != nil {
			at = index[img]
		}
		out = le.AppendUint32(out, uint32(at))
	}
	return out
}

var errIconFile = errors.New("not a file of icons")

func decodeIcons(data []byte) (map[string]*image.RGBA, error) {
	le := binary.LittleEndian
	take := func(n int) []byte {
		if n < 0 || n > len(data) {
			data = nil
			return nil
		}
		b := data[:n:n]
		data = data[n:]
		return b
	}
	head := take(8)
	if head == nil || le.Uint32(head) != iconMagic {
		return nil, errIconFile
	}
	pictures := make([]*image.RGBA, 0, min(int(le.Uint32(head[4:])), 1024))
	for range le.Uint32(head[4:]) {
		size := take(4)
		if size == nil {
			return nil, errIconFile
		}
		w, h := int(le.Uint16(size)), int(le.Uint16(size[2:]))
		if w == 0 || h == 0 || w > storedIconMax || h > storedIconMax {
			return nil, errIconFile
		}
		// The picture's pixels are the file's: nothing is copied.
		pix := take(4 * w * h)
		if pix == nil {
			return nil, errIconFile
		}
		pictures = append(pictures, &image.RGBA{Pix: pix, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)})
	}
	count := take(4)
	if count == nil {
		return nil, errIconFile
	}
	icons := make(map[string]*image.RGBA, min(int(le.Uint32(count)), storedKeysMax))
	for range le.Uint32(count) {
		n := take(2)
		if n == nil {
			return nil, errIconFile
		}
		key := take(int(le.Uint16(n)))
		at := take(4)
		if at == nil {
			return nil, errIconFile
		}
		switch i := int32(le.Uint32(at)); {
		case i == -1:
			icons[string(key)] = nil
		case i >= 0 && int(i) < len(pictures):
			icons[string(key)] = pictures[i]
		default:
			return nil, errIconFile
		}
	}
	return icons, nil
}
