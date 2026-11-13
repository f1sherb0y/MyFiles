package shell

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"myfiles/internal/fsys"
)

// The programs a user starts, and the settings of the system, are not
// files: they are items of two folders of the shell's namespace, which
// lists them with the names the Start menu and the Control Panel show.

var (
	bhidEnumItems     = guid("94f60519-2850-4924-aa5a-d15e84868039")
	iidEnumShellItems = guid("70629033-e363-4a28-a567-0db78006e6d7")
)

const (
	// The folder of all that can be started, programs of the Store among
	// them, and the folder of every task of the Control Panel.
	appsFolder     = "shell:AppsFolder"
	settingsFolder = "shell:::{ED7BA470-8E54-465E-825C-99712043E01C}"

	// The methods of IShellItem and of IEnumShellItems, by their places.
	itemBindToHandler  = 3
	itemGetDisplayName = 5
	enumNext           = 3

	sigdnNormalDisplay          = 0
	sigdnDesktopAbsoluteParsing = 0x80028000
	sigdnParentRelativeParsing  = 0x80018001

	// The folder of programs as the namespace names it, which what
	// starts a program is told by, with the program's own name after.
	appsRoot = `::{4234d49b-0245-4df3-b780-3893943456e1}`
)

var (
	procSHGetIDListFromObject = shell32.NewProc("SHGetIDListFromObject")
	procILGetSize             = shell32.NewProc("ILGetSize")

	// programIDs are what the namespace knows the programs and settings
	// listed by: copies of its identifiers, by the items' paths. Not every
	// item's name is one the namespace reads back, so an item is drawn and
	// started by its identifier.
	programMu  sync.RWMutex
	programIDs = map[string][]byte{}
)

func programs() []Program {
	var out []Program
	_ = onSTA(func() error {
		out = append(out, itemsOf(appsFolder, false)...)
		out = append(out, itemsOf(settingsFolder, true)...)
		return nil
	})
	return out
}

// keepProgram notes the identifier of a program another process listed.
func keepProgram(path string, id []byte) {
	if len(id) == 0 {
		return
	}
	programMu.Lock()
	programIDs[path] = id
	programMu.Unlock()
}

// hideWindow keeps a process started to ask it something from showing a
// console.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /* no window */}
}

// programID is the identifier of a program or a setting that was listed,
// nil for a path that is none.
func programID(path string) []byte {
	programMu.RLock()
	defer programMu.RUnlock()
	return programIDs[path]
}

// idOf copies an item's identifier.
func idOf(item *object) []byte {
	var id pidl
	if hr, _, _ := procSHGetIDListFromObject.Call(uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(&id))); int32(hr) < 0 || id == 0 {
		return nil
	}
	defer id.free()
	n, _, _ := procILGetSize.Call(uintptr(id))
	if n == 0 || n > 1<<16 {
		return nil
	}
	// The system's memory, read once into the program's own.
	return append([]byte(nil), unsafe.Slice(*(**byte)(unsafe.Pointer(&id)), n)...)
}

// itemsOf lists the items of a folder of the namespace: each with the
// name it shows under, and the name the namespace finds it by.
func itemsOf(folder string, setting bool) []Program {
	item, err := shellItem(folder)
	if err != nil {
		return nil
	}
	defer item.release()
	var enum *object
	hr := item.call(itemBindToHandler, 0, uintptr(unsafe.Pointer(&bhidEnumItems)),
		uintptr(unsafe.Pointer(&iidEnumShellItems)), uintptr(unsafe.Pointer(&enum)))
	if hr.failed() || enum == nil {
		return nil
	}
	defer enum.release()
	var out []Program
	for {
		var child *object
		var got uint32
		// S_FALSE at the end.
		if hr := enum.call(enumNext, 1, uintptr(unsafe.Pointer(&child)), uintptr(unsafe.Pointer(&got))); hr != 0 || child == nil {
			break
		}
		name := itemName(child, sigdnNormalDisplay)
		path := itemName(child, sigdnDesktopAbsoluteParsing)
		if !setting {
			// A program is named within its folder: by what the system
			// starts it by, whether it is one of the Store or not.
			if own := itemName(child, sigdnParentRelativeParsing); own != "" {
				path = appsRoot + `\` + own
			}
		}
		id := idOf(child)
		child.release()
		// One that is a file is a file like the others, which a search
		// finds as one.
		if name == "" || id == nil || !strings.HasPrefix(path, "::") {
			continue
		}
		programMu.Lock()
		programIDs[path] = id
		programMu.Unlock()
		out = append(out, Program{Name: name, Path: path, Setting: setting})
	}
	return out
}

func itemName(item *object, form uint32) string {
	var p *uint16
	if item.call(itemGetDisplayName, uintptr(form), uintptr(unsafe.Pointer(&p))).failed() || p == nil {
		return ""
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return windows.UTF16PtrToString(p)
}

// launch starts an item of the namespace that is no file.
func launch(owner Window, path string) error {
	id := programID(path)
	if id == nil {
		return &fsys.PathError{Op: "open", Path: path, Err: windows.ERROR_FILE_NOT_FOUND}
	}
	return onSTA(func() error {
		const seeMaskIDList = 0x4
		info := executeInfo{mask: seeMaskIDList | seeMaskNoAsync, hwnd: uintptr(owner), show: swShowNormal}
		info.size = uint32(unsafe.Sizeof(info))
		info.idList = uintptr(unsafe.Pointer(&id[0]))
		if r, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
			if err == windows.ERROR_CANCELLED {
				return ErrCanceled
			}
			return &fsys.PathError{Op: "open", Path: path, Err: err}
		}
		runtime.KeepAlive(id)
		return nil
	})
}
