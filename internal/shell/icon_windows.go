package shell

import (
	"errors"
	"image"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"cairn/internal/fsys"
)

var (
	procSHGetFileInfoW   = shell32.NewProc("SHGetFileInfoW")
	procSHGetImageList   = shell32.NewProc("SHGetImageList")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procGetIconInfo      = user32.NewProc("GetIconInfo")
	procDestroyIcon      = user32.NewProc("DestroyIcon")
	procGetDC            = user32.NewProc("GetDC")
	procReleaseDC        = user32.NewProc("ReleaseDC")
	procGetObjectW       = gdi32.NewProc("GetObjectW")
	procGetDIBits        = gdi32.NewProc("GetDIBits")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
)

// The flags of SHGetFileInfoW.
const (
	shgfiSmallIcon         = 0x1
	shgfiUseFileAttributes = 0x10
	shgfiAddOverlays       = 0x20
	shgfiOverlayIndex      = 0x40
	shgfiIcon              = 0x100
	shgfiTypeName          = 0x400
	shgfiSysIconIndex      = 0x4000
)

// fileInfo is the system's SHFILEINFOW.
type fileInfo struct {
	icon        uintptr
	index       int32
	attributes  uint32
	displayName [windows.MAX_PATH]uint16
	typeName    [80]uint16
}

// iconClass is one of the sizes the system keeps icons in.
type iconClass uint8

const (
	iconSmall      iconClass = iota // 16 pixels at 100%
	iconLarge                       // 32
	iconExtraLarge                  // 48
	iconJumbo                       // 256
	iconClasses
)

// imageLists are the system's image lists of the classes (SHIL_SMALL and
// the like).
var imageLists = [iconClasses]uintptr{1, 0, 2, 4}

// iconSizes is how wide the icons of each class are, in pixels: what the
// system's image lists say, which follows the display's scale as the
// process sees it.
var iconSizes = sync.OnceValue(func() [iconClasses]int {
	const (
		smCxIcon   = 11
		smCxSmIcon = 49
	)
	small, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	large, _, _ := procGetSystemMetrics.Call(smCxIcon)
	sizes := [iconClasses]int{int(small), int(large), int(large) * 3 / 2, 256}
	for c := range sizes {
		list, err := imageList(iconClass(c))
		if err != nil {
			continue
		}
		var w, h int32
		const getIconSize = 16
		if !list.call(getIconSize, uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h))).failed() && w > 0 {
			sizes[c] = int(w)
		}
		list.release()
	}
	return sizes
})

// imageList is the system's image list of a class (an IImageList), to be
// released.
func imageList(c iconClass) (*object, error) {
	var list *object
	hr, _, _ := procSHGetImageList.Call(imageLists[c], uintptr(unsafe.Pointer(&iidImageList)), uintptr(unsafe.Pointer(&list)))
	return list, hresult(hr).err()
}

// classFor is the smallest class whose icons are about px pixels wide
// or wider, the largest when none is: an icon scaled down stays sharp,
// and one scaled up does not.
func classFor(px int) iconClass {
	sizes := iconSizes()
	for c := iconSmall; c < iconClasses; c++ {
		if sizes[c]*10 >= px*9 {
			return c
		}
	}
	return iconClasses - 1
}

// iconKey names a picture: the index of an icon in the system's image
// list, with the index of the overlay drawn on it in the top byte, and
// the size.
type iconKey struct {
	index int32
	class iconClass
}

// typeKey names the icon of a type.
type typeKey struct {
	// ext is the extension, in lower case with its dot; dirExt for
	// directories.
	ext   string
	class iconClass
}

// dirExt stands for directories where extensions name types; no name has
// it for an extension.
const dirExt = `\`

// icons are the pictures made so far. There is one per iconKey, ever, so
// that callers may tell pictures apart by their pointers; byType
// remembers which of them a type has, to answer without asking the
// system.
var icons struct {
	mu     sync.RWMutex
	byKey  map[iconKey]*image.RGBA
	byType map[typeKey]*image.RGBA
}

func icon(path string, attr fsys.Attr, px int, byType bool) (*image.RGBA, error) {
	class := classFor(px)
	dir := attr&fsys.AttrDir != 0
	if !byType {
		if img, err := lookUpIcon(path, dir, class, false); err == nil {
			return img, nil
		}
		// A file the shell cannot ask, as one whose path is too long for
		// it, has its type's icon at least.
	}
	tk := typeKey{ext: typeExt(path, dir), class: class}
	icons.mu.RLock()
	img := icons.byType[tk]
	icons.mu.RUnlock()
	if img != nil {
		return img, nil
	}
	img, err := lookUpIcon(path, dir, class, true)
	if err != nil {
		return nil, err
	}
	icons.mu.Lock()
	if icons.byType == nil {
		icons.byType = make(map[typeKey]*image.RGBA)
	}
	icons.byType[tk] = img
	icons.mu.Unlock()
	return img, nil
}

// typeExt is what names the type of a file at path: its extension, or
// dirExt.
func typeExt(path string, dir bool) string {
	if dir {
		return dirExt
	}
	return fsys.ExtOf(path[strings.LastIndexByte(path, fsys.Sep)+1:])
}

// lookUpIcon asks the system which icon a file has, and returns its
// picture, made now if it is the first time the icon is seen.
func lookUpIcon(path string, dir bool, class iconClass, byType bool) (*image.RGBA, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// The lists of the sizes above large have the same icons at the
	// same indexes, so the index is asked for as of a large one.
	var flags, fileAttr uintptr
	if class == iconSmall {
		flags = shgfiSmallIcon
	}
	if byType {
		// A type has no overlay, and its index costs a tenth of what
		// an icon does.
		flags |= shgfiSysIconIndex | shgfiUseFileAttributes
		fileAttr = windows.FILE_ATTRIBUTE_NORMAL
		if dir {
			fileAttr = windows.FILE_ATTRIBUTE_DIRECTORY
		}
	} else {
		// The index of the overlay comes only with an icon made, which
		// is of no other use: the picture is the image list's.
		flags |= shgfiIcon | shgfiAddOverlays | shgfiOverlayIndex
	}
	var info fileInfo
	r, _, _ := procSHGetFileInfoW.Call(uintptr(unsafe.Pointer(p)), fileAttr,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), flags)
	if info.icon != 0 {
		procDestroyIcon.Call(info.icon)
	}
	if r == 0 {
		return nil, &fsys.PathError{Op: "icon", Path: path, Err: errNoIcon}
	}

	key := iconKey{index: info.index, class: class}
	icons.mu.RLock()
	img := icons.byKey[key]
	icons.mu.RUnlock()
	if img != nil {
		return img, nil
	}
	if img, err = listedIcon(key); err != nil {
		return nil, &fsys.PathError{Op: "icon", Path: path, Err: err}
	}
	icons.mu.Lock()
	defer icons.mu.Unlock()
	if first := icons.byKey[key]; first != nil {
		// Another worker made it meanwhile: its picture is the one.
		return first, nil
	}
	if icons.byKey == nil {
		icons.byKey = make(map[iconKey]*image.RGBA)
	}
	icons.byKey[key] = img
	return img, nil
}

var errNoIcon = errors.New("the system has no icon for it")

// listedIcon is the picture of an icon of the system's image list of the
// key's class, with its overlay.
func listedIcon(key iconKey) (*image.RGBA, error) {
	list, err := imageList(key.class)
	if err != nil {
		return nil, err
	}
	defer list.release()
	const (
		getIcon        = 10
		ildTransparent = 0x1
	)
	// The overlay goes from the top byte of the index to where the
	// drawing flags have it (INDEXTOOVERLAYMASK).
	index := uintptr(key.index & 0xFFFFFF)
	overlay := uintptr(uint32(key.index)>>24) << 8
	var hicon uintptr
	if err := list.call(getIcon, index, ildTransparent|overlay, uintptr(unsafe.Pointer(&hicon))).err(); err != nil {
		return nil, err
	}
	if hicon == 0 {
		return nil, errNoIcon
	}
	defer procDestroyIcon.Call(hicon)
	img, err := iconPixels(hicon)
	if err != nil {
		return nil, err
	}
	if key.class == iconJumbo {
		img = cropCorner(img, iconSizes()[iconExtraLarge])
	}
	return img, nil
}

// cropCorner cuts a picture down to its top left square of size pixels
// when nothing is drawn outside of it: the system draws icons that have
// no jumbo picture so, small in the corner of an empty one.
func cropCorner(img *image.RGBA, size int) *image.RGBA {
	b := img.Bounds()
	if size >= b.Dx() || size >= b.Dy() {
		return img
	}
	for y := 0; y < b.Dy(); y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+b.Dx()*4]
		from := size
		if y >= size {
			from = 0
		}
		for x := from; x < b.Dx(); x++ {
			if row[x*4+3] != 0 {
				return img
			}
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		copy(out.Pix[y*out.Stride:(y+1)*out.Stride], img.Pix[y*img.Stride:])
	}
	return out
}

// iconInfo is the system's ICONINFO.
type iconInfo struct {
	isIcon   int32
	xHotspot uint32
	yHotspot uint32
	mask     uintptr
	color    uintptr
}

// bitmapHeader is the system's BITMAP.
type bitmapHeader struct {
	typ        int32
	width      int32
	height     int32
	widthBytes int32
	planes     uint16
	bitsPixel  uint16
	bits       uintptr
}

// bitmapInfo is the system's BITMAPINFO, with the room for the three
// color masks that some drivers write whatever the format asked for.
type bitmapInfo struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
	masks         [3]uint32
}

// iconPixels is the picture of an icon, premultiplied.
func iconPixels(hicon uintptr) (*image.RGBA, error) {
	var info iconInfo
	if r, _, err := procGetIconInfo.Call(hicon, uintptr(unsafe.Pointer(&info))); r == 0 {
		return nil, err
	}
	// GetIconInfo makes copies of the icon's bitmaps, which are ours to
	// delete.
	defer procDeleteObject.Call(info.mask)
	if info.color == 0 {
		// An icon of two colors, as some cursors are: the image lists,
		// which are in full color, have none.
		return nil, errNoIcon
	}
	defer procDeleteObject.Call(info.color)
	dc, _, err := procGetDC.Call(0)
	if dc == 0 {
		return nil, err
	}
	defer procReleaseDC.Call(0, dc)

	img, err := bitmapPixels(dc, info.color)
	if err != nil {
		return nil, err
	}
	pix := img.Pix
	hasAlpha := false
	for i := 3; i < len(pix); i += 4 {
		if pix[i] != 0 {
			hasAlpha = true
			break
		}
	}
	if !hasAlpha {
		// An icon of the old kind: its bitmap has colors alone, and its
		// mask is white where the background shows through.
		mask, err := bitmapPixels(dc, info.mask)
		if err != nil || mask.Rect != img.Rect {
			return nil, errors.Join(errNoIcon, err)
		}
		for i := 0; i < len(pix); i += 4 {
			if mask.Pix[i] != 0 {
				pix[i], pix[i+1], pix[i+2], pix[i+3] = 0, 0, 0, 0
			} else {
				pix[i], pix[i+2], pix[i+3] = pix[i+2], pix[i], 255
			}
		}
		return img, nil
	}
	// The bitmap of an icon is blue, green, red and alpha, the colors
	// not multiplied by the alpha: so the system keeps every icon,
	// whatever it was made from (those of the image lists show it,
	// with colors above their alpha at every size). The image wants red
	// first, and the colors multiplied.
	for i := 0; i < len(pix); i += 4 {
		a := uint32(pix[i+3])
		b, g, r := uint32(pix[i]), uint32(pix[i+1]), uint32(pix[i+2])
		pix[i] = byte((r*a + 127) / 255)
		pix[i+1] = byte((g*a + 127) / 255)
		pix[i+2] = byte((b*a + 127) / 255)
	}
	return img, nil
}

// bitmapPixels reads a bitmap into an image of its size, top row first,
// as the system gives 32 bits a pixel: blue, green, red and what the
// bitmap has for alpha, not yet in the image's order.
func bitmapPixels(dc, bitmap uintptr) (*image.RGBA, error) {
	var header bitmapHeader
	if r, _, _ := procGetObjectW.Call(bitmap, unsafe.Sizeof(header), uintptr(unsafe.Pointer(&header))); r == 0 {
		return nil, errNoIcon
	}
	w, h := int(header.width), int(header.height)
	if w <= 0 || h <= 0 || w > 1024 || h > 2048 {
		return nil, errNoIcon
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	info := bitmapInfo{
		size:     40,
		width:    int32(w),
		height:   int32(-h), // top row first
		planes:   1,
		bitCount: 32,
	}
	const dibRGBColors = 0
	r, _, _ := procGetDIBits.Call(dc, bitmap, 0, uintptr(h), uintptr(unsafe.Pointer(&img.Pix[0])),
		uintptr(unsafe.Pointer(&info)), dibRGBColors)
	if int(r) != h {
		return nil, errNoIcon
	}
	return img, nil
}

// ownIconExts are the extensions of files that have icons of their own.
var ownIconExts = map[string]bool{
	".exe": true, ".ico": true, ".lnk": true, ".url": true, ".cur": true, ".ani": true,
	".scr": true, ".msi": true, ".msc": true, ".cpl": true, ".appref-ms": true, ".library-ms": true,
}

func ownIcon(name string, attr fsys.Attr) bool {
	if attr&fsys.AttrDir != 0 {
		return attr&fsys.AttrCustomIcon != 0
	}
	return ownIconExts[fsys.ExtOf(name)]
}

// typeNames are the names of types asked for so far, by extension, or
// dirExt.
var typeNames struct {
	mu sync.Mutex
	m  map[string]string
}

func typeName(ext string, dir bool) string {
	fileAttr := uintptr(windows.FILE_ATTRIBUTE_NORMAL)
	if dir {
		ext, fileAttr = dirExt, windows.FILE_ATTRIBUTE_DIRECTORY
	}
	typeNames.mu.Lock()
	name, ok := typeNames.m[ext]
	typeNames.mu.Unlock()
	if ok {
		return name
	}
	// Any name with the extension does, as the file is not looked at.
	p, err := windows.UTF16PtrFromString("x" + strings.TrimPrefix(ext, dirExt))
	if err != nil {
		return ""
	}
	var info fileInfo
	r, _, _ := procSHGetFileInfoW.Call(uintptr(unsafe.Pointer(p)), fileAttr,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), shgfiTypeName|shgfiUseFileAttributes)
	if r != 0 {
		name = windows.UTF16ToString(info.typeName[:])
	}
	typeNames.mu.Lock()
	if typeNames.m == nil {
		typeNames.m = make(map[string]string)
	}
	typeNames.m[ext] = name
	typeNames.mu.Unlock()
	return name
}
