package shell

import (
	"image"
	_ "image/png" // the icons of programs from the Store
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/image/draw"
	"golang.org/x/sys/windows"

	"myfiles/internal/fsys"
)

var (
	shlwapi = windows.NewLazySystemDLL("shlwapi.dll")

	procSHAssocEnumHandlers      = shell32.NewProc("SHAssocEnumHandlers")
	procSHDefExtractIconW        = shell32.NewProc("SHDefExtractIconW")
	procSHLoadIndirectString     = shlwapi.NewProc("SHLoadIndirectString")
	procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
)

// The methods of IEnumAssocHandlers and IAssocHandler, by their place in
// their tables.
const (
	enumHandlersNext = 3

	handlerGetName         = 3
	handlerGetUIName       = 4
	handlerGetIconLocation = 5
	handlerInvoke          = 8
)

// handlers calls each with the programs registered to open files with an
// extension, the ones the system recommends, in its order, until each
// returns false. The objects are released after the call.
func handlers(ext string, each func(h *object) bool) error {
	p, err := windows.UTF16PtrFromString(ext)
	if err != nil {
		return err
	}
	const recommended = 1 // ASSOC_FILTER_RECOMMENDED
	var enum *object
	hr, _, _ := procSHAssocEnumHandlers.Call(uintptr(unsafe.Pointer(p)), recommended, uintptr(unsafe.Pointer(&enum)))
	if err := hresult(hr).err(); err != nil {
		return err
	}
	defer enum.release()
	for {
		var h *object
		var fetched uint32
		hr := enum.call(enumHandlersNext, 1, uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&fetched)))
		if hr.failed() || fetched == 0 || h == nil {
			return nil
		}
		more := each(h)
		h.release()
		if !more {
			return nil
		}
	}
}

// handlerString is a string a handler gives through one of its methods,
// which the caller is to free.
func handlerString(h *object, method int) string {
	var p *uint16
	if h.call(method, uintptr(unsafe.Pointer(&p))).failed() || p == nil {
		return ""
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return windows.UTF16PtrToString(p)
}

// appIconSize is the side of a program's icon, in pixels: what a menu
// shows at 16 DIPs on a screen of twice the pixels.
const appIconSize = 32

func apps(path string) []App {
	ext := fsys.ExtOf(fsys.Base(path))
	if ext == "" {
		return nil
	}
	var out []App
	_ = onSTA(func() error {
		return handlers(ext, func(h *object) bool {
			app := App{ID: handlerString(h, handlerGetName), Name: handlerString(h, handlerGetUIName)}
			if app.ID != "" && app.Name != "" {
				app.Icon = handlerIcon(h, app.ID)
				out = append(out, app)
			}
			return len(out) < 24
		})
	})
	return out
}

// handlerIcon is the icon of a program that opens files, nil when it has
// none to be found. A program says where its icon is: in a file of icons,
// at an index, or, for one from the Store, in a picture named by a
// resource of its package.
func handlerIcon(h *object, id string) *image.RGBA {
	var p *uint16
	var index int32
	if !h.call(handlerGetIconLocation, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&index))).failed() && p != nil {
		where := windows.UTF16PtrToString(p)
		procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
		if strings.HasPrefix(where, "@") {
			if img := pictureIcon(indirectString(where)); img != nil {
				return img
			}
		} else if img := extractIcon(where, index, appIconSize); img != nil {
			return img
		}
	}
	// The program's own, when the handler is named by its file.
	if img, err := icon(id, 0, appIconSize, false); err == nil && filepath.IsAbs(id) {
		return img
	}
	return nil
}

// indirectString is what a string naming a resource ("@{package?
// ms-resource://…}", "@file.dll,-12") stands for, "" when nothing.
func indirectString(s string) string {
	in, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return ""
	}
	var out [1024]uint16
	if hr, _, _ := procSHLoadIndirectString.Call(uintptr(unsafe.Pointer(in)), uintptr(unsafe.Pointer(&out[0])), uintptr(len(out)), 0); hresult(hr).failed() {
		return ""
	}
	return windows.UTF16ToString(out[:])
}

// pictureIcon is the picture in a file, as programs from the Store have
// for icons, made the size of an icon.
func pictureIcon(path string) *image.RGBA {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, appIconSize, appIconSize))
	draw.CatmullRom.Scale(out, out.Rect, src, src.Bounds(), draw.Src, nil)
	return out
}

// extractIcon is the icon at an index of a file of icons.
func extractIcon(path string, index int32, px int) *image.RGBA {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	var hicon uintptr
	hr, _, _ := procSHDefExtractIconW.Call(uintptr(unsafe.Pointer(p)), uintptr(index), 0, uintptr(unsafe.Pointer(&hicon)), 0, uintptr(px))
	if hresult(hr) != 0 || hicon == 0 {
		return nil
	}
	defer procDestroyIcon.Call(hicon)
	img, err := iconPixels(hicon)
	if err != nil {
		return nil
	}
	return img
}

// openIn runs on a host thread: a program started may talk to the thread
// that started it for a while.
func openIn(owner Window, path string, app App) error {
	ext := fsys.ExtOf(fsys.Base(path))
	return onHost(func() error {
		data, err := dataObject([]string{path})
		if err != nil {
			return err
		}
		defer data.release()
		found := false
		var invoked error
		err = handlers(ext, func(h *object) bool {
			if handlerString(h, handlerGetName) != app.ID {
				return true
			}
			found = true
			invoked = h.call(handlerInvoke, uintptr(unsafe.Pointer(data))).err()
			return false
		})
		switch {
		case err != nil:
			return &fsys.PathError{Op: "open", Path: path, Err: err}
		case !found:
			return &fsys.PathError{Op: "open", Path: path, Err: windows.ERROR_NO_ASSOCIATION}
		case invoked != nil:
			return &fsys.PathError{Op: "open", Path: path, Err: invoked}
		}
		dispatchMessages()
		return nil
	})
}

// openWith starts the system's program that asks which program opens a
// file, and opens it with the one chosen. The ways a program is meant to
// ask for it do not work on current systems (Windows 11 26300, 2026-11):
// the verb "openas" returns at once having shown nothing, and
// SHOpenWithDialog fails with "the process has no package identity".
func openWith(owner Window, path string) error {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return &fsys.PathError{Op: "open", Path: path, Err: err}
	}
	cmd := exec.Command(filepath.Join(dir, "OpenWith.exe"), path)
	if err := cmd.Start(); err != nil {
		return &fsys.PathError{Op: "open", Path: path, Err: err}
	}
	// Its window closes itself unless it is the one in front, which only
	// the program in front can let it be.
	procAllowSetForegroundWindow.Call(uintptr(cmd.Process.Pid))
	go cmd.Wait()
	return nil
}
