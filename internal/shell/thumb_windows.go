package shell

import (
	"image"
	"unsafe"
)

var iidShellItemImageFactory = guid("bcc18b79-ba16-442f-80c4-8a59c30c463b")

// thumbnail asks the shell for the picture of a file's content, as its
// file manager shows pictures and videos: from the system's thumbnail
// cache where it is there, else made by the handler of the file's type.
// Files of types without one have none, and an error says so.
func thumbnail(path string, px int) (*image.RGBA, error) {
	item, err := shellItem(path)
	if err != nil {
		return nil, err
	}
	defer item.release()
	factory := item.query(&iidShellItemImageFactory)
	if factory == nil {
		return nil, errNoIcon
	}
	defer factory.release()

	// The size is passed by value: two 32-bit numbers in one argument.
	size := uintptr(uint32(px)) | uintptr(uint32(px))<<32
	// Thumbnails only: never the icon, which the caller has already.
	const thumbnailOnly = 0x8
	var bitmap uintptr
	const getImage = 3
	if err := factory.call(getImage, size, thumbnailOnly, uintptr(unsafe.Pointer(&bitmap))).err(); err != nil {
		return nil, err
	}
	defer procDeleteObject.Call(bitmap)
	dc, _, callErr := procGetDC.Call(0)
	if dc == 0 {
		return nil, callErr
	}
	defer procReleaseDC.Call(0, dc)
	img, err := bitmapPixels(dc, bitmap)
	if err != nil {
		return nil, err
	}
	// The bitmap is blue, green, red and alpha, the colors multiplied by
	// the alpha already; pictures without transparency may leave the
	// alpha at zero throughout.
	pix := img.Pix
	noAlpha := true
	for i := 3; i < len(pix); i += 4 {
		if pix[i] != 0 {
			noAlpha = false
			break
		}
	}
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+2] = pix[i+2], pix[i]
		if noAlpha {
			pix[i+3] = 255
		}
	}
	return img, nil
}
