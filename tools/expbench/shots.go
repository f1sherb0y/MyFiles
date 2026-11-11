package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Pictures of what a window shows, for -shots: taken the moment a
// measurement ends, they let the method be checked by eye. If the clock
// stopped too early, the picture is of the folder before.

var (
	gdi32 = windows.NewLazySystemDLL("gdi32.dll")

	procGetDC         = user32.NewProc("GetDC")
	procReleaseDC     = user32.NewProc("ReleaseDC")
	procGetClientRect = user32.NewProc("GetClientRect")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
)

// capture copies what a window has drawn: the content of its surface,
// whether or not other windows cover it on screen. It asks nothing of the
// window's thread.
func capture(h windows.HWND) *image.RGBA {
	var rc windows.Rect
	procGetClientRect.Call(uintptr(h), uintptr(unsafe.Pointer(&rc)))
	width, height := int(rc.Right), int(rc.Bottom)
	if width <= 0 || height <= 0 {
		return nil
	}
	dc, _, _ := procGetDC.Call(uintptr(h))
	if dc == 0 {
		return nil
	}
	defer procReleaseDC.Call(uintptr(h), dc)
	mem, _, _ := procCreateCompatibleDC.Call(dc)
	defer procDeleteDC.Call(mem)
	bitmap, _, _ := procCreateCompatibleBitmap.Call(dc, uintptr(width), uintptr(height))
	defer procDeleteObject.Call(bitmap)

	const srcCopy = 0x00CC0020
	old, _, _ := procSelectObject.Call(mem, bitmap)
	procBitBlt.Call(mem, 0, 0, uintptr(width), uintptr(height), dc, 0, 0, srcCopy)
	procSelectObject.Call(mem, old)

	// BITMAPINFOHEADER: 32 bits a pixel, top row first.
	header := struct {
		size          uint32
		width, height int32
		planes, bits  uint16
		compression   uint32
		imageSize     uint32
		xPerMeter     int32
		yPerMeter     int32
		colorsUsed    uint32
		important     uint32
	}{width: int32(width), height: -int32(height), planes: 1, bits: 32}
	header.size = uint32(unsafe.Sizeof(header))

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	procGetDIBits.Call(mem, bitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&img.Pix[0])), uintptr(unsafe.Pointer(&header)), 0)
	for i := 0; i < len(img.Pix); i += 4 { // BGRx to RGBA
		img.Pix[i], img.Pix[i+2], img.Pix[i+3] = img.Pix[i+2], img.Pix[i], 0xFF
	}
	return img
}

// savePicture writes a picture into a folder, named for what it is of.
func savePicture(dir, name string, img *image.RGBA) error {
	if img == nil {
		return fmt.Errorf("no picture of %s", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name = strings.NewReplacer(`\`, "_", ":", "", " ", "_").Replace(name)
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
