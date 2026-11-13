package shell

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"myfiles/internal/fsys"
)

var (
	procShellExecuteExW                   = shell32.NewProc("ShellExecuteExW")
	procSHCreateShellItemArrayFromIDLists = shell32.NewProc("SHCreateShellItemArrayFromIDLists")
	procSHMultiFileProperties             = shell32.NewProc("SHMultiFileProperties")
)

// The flags of ShellExecuteExW.
const (
	// seeMaskInvokeIDList has the verb come from the item's context menu
	// where the registry has none, as "properties" does.
	seeMaskInvokeIDList = 0xC
	// seeMaskNoAsync has the call return only once it is done with the
	// thread, which may then end.
	seeMaskNoAsync = 0x100
	// seeMaskNoUI keeps the system from telling the user what went wrong
	// in a message box, with its sound: the error is returned, for the
	// caller to show its own way.
	seeMaskNoUI = 0x400

	swShowNormal = 1
)

// executeInfo is the system's SHELLEXECUTEINFOW.
type executeInfo struct {
	size       uint32
	mask       uint32
	hwnd       uintptr
	verb       *uint16
	file       *uint16
	parameters *uint16
	directory  *uint16
	show       int32
	instApp    uintptr
	idList     uintptr
	class      *uint16
	keyClass   uintptr
	hotKey     uint32
	monitor    uintptr
	process    uintptr
}

// execute does a verb of a file's, the default one for "", on the
// calling thread. With seeMaskNoUI what goes wrong is only returned;
// without it the system also tells the user itself, in a window of
// owner's.
func execute(owner Window, verb, path, dir string, mask uint32) error {
	return executeAs(owner, verb, path, dir, "", "", mask)
}

// executeAs is execute with the verbs of a class, as "Bandizip.rar", in
// place of those the system finds for the file, and with what the file,
// when it is a program, is started with: "" for neither.
func executeAs(owner Window, verb, path, dir, class, params string, mask uint32) error {
	info := executeInfo{mask: mask, hwnd: uintptr(owner), show: swShowNormal}
	info.size = uint32(unsafe.Sizeof(info))
	var err error
	if class != "" {
		const seeMaskClassName = 0x1
		if info.class, err = windows.UTF16PtrFromString(class); err != nil {
			return &fsys.PathError{Op: "open", Path: path, Err: err}
		}
		info.mask |= seeMaskClassName
	}
	if params != "" {
		if info.parameters, err = windows.UTF16PtrFromString(params); err != nil {
			return &fsys.PathError{Op: "open", Path: path, Err: err}
		}
	}
	if info.file, err = windows.UTF16PtrFromString(path); err != nil {
		return &fsys.PathError{Op: "open", Path: path, Err: err}
	}
	if verb != "" {
		info.verb, _ = windows.UTF16PtrFromString(verb)
	}
	if dir != "" {
		if info.directory, err = windows.UTF16PtrFromString(dir); err != nil {
			return &fsys.PathError{Op: "open", Path: dir, Err: err}
		}
	}
	if r, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if err == windows.ERROR_CANCELLED {
			return ErrCanceled
		}
		return &fsys.PathError{Op: "open", Path: path, Err: err}
	}
	return nil
}

// errNoProgram says, within open, that no program opens the file.
var errNoProgram = errors.New("no program opens the file")

func open(owner Window, path, dir string) error {
	err := openDefault(owner, path, dir)
	if err == errNoProgram {
		// The system's dialog that asks which program to use is never
		// started (see resolve_windows.go): the caller asks, with the
		// programs Apps lists.
		return ErrChoose
	}
	return err
}

// openDefault opens a file with the program set for its type, found here
// and not by the system: errNoProgram for a file nothing is set for.
func openDefault(owner Window, path, dir string) error {
	return openResolved(owner, path, dir, 0)
}

// openInSystem starts the system's file manager with a place to show.
func openInSystem(place string) error {
	return exec.Command("explorer.exe", place).Start()
}

// properties shows the property sheet on a host thread: the sheet runs
// on a thread the shell starts for it, but reaches the files through the
// thread that asked, which therefore has to be there, dispatching
// messages, for as long as the sheet is open.
func properties(owner Window, paths []string) error {
	switch len(paths) {
	case 0:
		return nil
	case 1:
		return onHost(func() error {
			return execute(owner, "properties", paths[0], "", seeMaskInvokeIDList)
		})
	}
	return onHost(func() error {
		data, err := dataObject(paths)
		if err != nil {
			return err
		}
		defer data.release()
		hr, _, _ := procSHMultiFileProperties.Call(uintptr(unsafe.Pointer(data)), 0)
		return hresult(hr).err()
	})
}

// itemArray is the shell's items for some files (an IShellItemArray), to
// be released.
func itemArray(paths []string) (*object, error) {
	ids := make([]pidl, 0, len(paths))
	defer func() { freePIDLs(ids) }()
	for _, path := range paths {
		id, err := parsePIDL(path)
		if err != nil {
			return nil, &fsys.PathError{Op: "open", Path: path, Err: err}
		}
		ids = append(ids, id)
	}
	var items *object
	hr, _, _ := procSHCreateShellItemArrayFromIDLists.Call(uintptr(len(ids)), uintptr(unsafe.Pointer(&ids[0])), uintptr(unsafe.Pointer(&items)))
	if err := hresult(hr).err(); err != nil {
		return nil, err
	}
	return items, nil
}

// dataObject is the shell's IDataObject of some files, as a drag of them
// or the clipboard would carry, to be released.
func dataObject(paths []string) (*object, error) {
	items, err := itemArray(paths)
	if err != nil {
		return nil, err
	}
	defer items.release()
	const bindToHandler = 3
	var data *object
	err = items.call(bindToHandler, 0, uintptr(unsafe.Pointer(&bhidDataObject)),
		uintptr(unsafe.Pointer(&iidDataObject)), uintptr(unsafe.Pointer(&data))).err()
	return data, err
}

var procAssocQueryStringW = windows.NewLazySystemDLL("shlwapi.dll").NewProc("AssocQueryStringW")

// needsChoice reports whether no program that works is set to open a
// file's type: what the system would start is its own dialog asking
// which, or an app from the Store that is no longer installed, whose
// name stays behind as the choice (opening such a file quietly did
// nothing, and otherwise said "the package could not be found").
func needsChoice(path string) bool {
	ext := filepath.Ext(path)
	if ext == "" {
		return false
	}
	const executable, progID = 2, 20 // ASSOCSTR_EXECUTABLE, ASSOCSTR_PROGID
	if exe, ok := assocString(ext, executable); ok {
		return strings.EqualFold(filepath.Base(exe), "OpenWith.exe")
	}
	// No executable to name: a shortcut, which the shell opens itself,
	// or an app from the Store, started by its package.
	id, ok := assocString(ext, progID)
	if !ok || !strings.HasPrefix(id, "AppX") {
		return false
	}
	k, err := registry.OpenKey(registry.CLASSES_ROOT, id+`\Shell\open`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	pkg, _, err := k.GetStringValue("PackageId")
	k.Close()
	if err != nil || pkg == "" {
		return false
	}
	// The packages installed for the user have a key each here.
	const packages = `Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\Repository\Packages\`
	k, err = registry.OpenKey(registry.CURRENT_USER, packages+pkg, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	k.Close()
	return false
}

// assocString asks the system something about what opens the files of
// an extension.
func assocString(ext string, what uintptr) (string, bool) {
	dot, err := windows.UTF16PtrFromString(ext)
	if err != nil {
		return "", false
	}
	verb, _ := windows.UTF16PtrFromString("open")
	var buf [1024]uint16
	n := uint32(len(buf))
	hr, _, _ := procAssocQueryStringW.Call(0, what, uintptr(unsafe.Pointer(dot)), uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if int32(hr) < 0 {
		return "", false
	}
	return windows.UTF16ToString(buf[:]), true
}
