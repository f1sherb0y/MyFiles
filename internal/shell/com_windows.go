package shell

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The system libraries the package calls, loaded when first used.
var (
	ole32   = windows.NewLazySystemDLL("ole32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	user32  = windows.NewLazySystemDLL("user32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")

	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procOleInitialize    = ole32.NewProc("OleInitialize")
	procOleUninitialize  = ole32.NewProc("OleUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")

	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	procSHParseDisplayName          = shell32.NewProc("SHParseDisplayName")
)

// The classes and interfaces of the shell the package uses.
var (
	clsidFileOperation = guid("3ad05575-8857-4850-9277-11b85bdb8e09")
	iidFileOperation   = guid("947aab5f-0a5c-4c13-b4d6-4bf7836fc9f8")
	iidShellItem       = guid("43826d1e-e718-42ee-bc55-a1e261c37bfe")
	iidShellFolder     = guid("000214e6-0000-0000-c000-000000000046")
	iidContextMenu     = guid("000214e4-0000-0000-c000-000000000046")
	iidContextMenu2    = guid("000214f4-0000-0000-c000-000000000046")
	iidContextMenu3    = guid("bcfce0a0-ec17-11d0-8d10-00a0c90f2719")
	iidDataObject      = guid("0000010e-0000-0000-c000-000000000046")
	iidImageList       = guid("46eb5926-582e-4017-9fdf-e8998daa0950")
	bhidDataObject     = guid("b8c0bd9f-ed24-455c-83e6-d5390c4fe8c4")
)

// guid reads a GUID as it is written, without braces. It panics on what
// is none: the package's own are constants.
func guid(s string) windows.GUID {
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		panic("shell: bad GUID " + s)
	}
	g := windows.GUID{
		Data1: binary.BigEndian.Uint32(b),
		Data2: binary.BigEndian.Uint16(b[4:]),
		Data3: binary.BigEndian.Uint16(b[6:]),
	}
	copy(g.Data4[:], b[8:])
	return g
}

// object is a COM object as its users hold it: a pointer to the pointer
// to its table of methods. The memory is the system's, not Go's.
type object struct {
	vtbl *[64]uintptr
}

// call calls the method at an index of the object's table, the first
// three being IUnknown's, with the object before the arguments. Pointers
// among the arguments are kept alive until it returns.
//
//go:uintptrescapes
func (o *object) call(method int, args ...uintptr) hresult {
	var a [8]uintptr
	if len(args) >= len(a) {
		panic("shell: too many arguments to a COM method")
	}
	a[0] = uintptr(unsafe.Pointer(o))
	n := 1 + copy(a[1:], args)
	r, _, _ := syscall.SyscallN(o.vtbl[method], a[:n]...)
	return hresult(r)
}

// release gives up a reference to the object, which may be nil.
func (o *object) release() {
	if o != nil {
		o.call(2)
	}
}

// query is another interface of the object, to be released, or nil when
// it has none such.
func (o *object) query(iid *windows.GUID) *object {
	var out *object
	if o.call(0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))).failed() {
		return nil
	}
	return out
}

// hresult is what a COM call returns: negative for a failure.
type hresult uint32

const (
	// The answers of the shell's copy engine that are no errors of ours.
	hrUserCancelled = hresult(0x80270000) // COPYENGINE_E_USER_CANCELLED
	hrCancelled     = hresult(0x80270001) // COPYENGINE_E_CANCELLED
	// hrWin32Cancelled is ERROR_CANCELLED as an HRESULT.
	hrWin32Cancelled = hresult(0x800704C7)

	facilityWin32 = 7
)

func (hr hresult) failed() bool { return int32(hr) < 0 }

// canceled reports whether the result says the user gave up.
func (hr hresult) canceled() bool {
	return hr == hrUserCancelled || hr == hrCancelled || hr == hrWin32Cancelled
}

// err is nil for a call that worked, ErrCanceled for one the user gave
// up, and otherwise a *comError.
func (hr hresult) err() error {
	switch {
	case !hr.failed():
		return nil
	case hr.canceled():
		return ErrCanceled
	}
	return &comError{hr}
}

// comError is a failed COM call. Its message is the system's own, in the
// user's language, where the system has one; errors.Is finds the Windows
// error under it, so that errors.Is(err, fs.ErrNotExist) and the like
// work.
type comError struct {
	hr hresult
}

// copyErrors are the failures of the shell's copy engine, which the
// system has no messages for, with the Windows errors they amount to.
var copyErrors = map[hresult]struct {
	text  string
	errno windows.Errno
}{
	0x80270002: {"the operation needs administrator rights", windows.ERROR_ELEVATION_REQUIRED},
	0x80270003: {"the source and the destination are the same file", windows.ERROR_ALREADY_EXISTS},
	0x80270009: {"the destination is inside the source", windows.ERROR_INVALID_PARAMETER},
	0x8027000A: {"the destination is the folder the source is in", windows.ERROR_INVALID_PARAMETER},
	0x8027000B: {"a file with the folder's name is in the destination", windows.ERROR_ALREADY_EXISTS},
	0x8027000C: {"a folder with the file's name is in the destination", windows.ERROR_ALREADY_EXISTS},
	0x8027000D: {"the file is too large for the destination's file system", windows.ERROR_FILE_TOO_LARGE},
	0x8027000E: {"the destination disk is full", windows.ERROR_DISK_FULL},
	0x8027001B: {"the source path is not valid", windows.ERROR_INVALID_NAME},
	0x8027001C: {"the destination path is not valid", windows.ERROR_INVALID_NAME},
	0x8027001D: {"the source path is too long", windows.ERROR_FILENAME_EXCED_RANGE},
	0x8027001E: {"the destination path is too long", windows.ERROR_FILENAME_EXCED_RANGE},
	0x8027001F: {"the source is a drive, which cannot be moved or renamed", windows.ERROR_ACCESS_DENIED},
	0x80270020: {"the destination is a drive, which cannot be renamed", windows.ERROR_ACCESS_DENIED},
	0x80270021: {"access to the source is denied", windows.ERROR_ACCESS_DENIED},
	0x80270022: {"access to the destination is denied", windows.ERROR_ACCESS_DENIED},
	0x80270023: {"the source does not exist or cannot be reached", windows.ERROR_PATH_NOT_FOUND},
	0x80270024: {"the destination does not exist or cannot be reached", windows.ERROR_PATH_NOT_FOUND},
	0x80270025: {"the source is on a network location that is disconnected", windows.ERROR_NETNAME_DELETED},
	0x80270026: {"the destination is on a network location that is disconnected", windows.ERROR_NETNAME_DELETED},
	0x80270027: {"the source is in use by another program", windows.ERROR_SHARING_VIOLATION},
	0x80270028: {"the destination is in use by another program", windows.ERROR_SHARING_VIOLATION},
	0x80270029: {"the destination already exists", windows.ERROR_ALREADY_EXISTS},
	0x8027002A: {"the destination already exists and is read-only", windows.ERROR_ALREADY_EXISTS},
	0x8027002B: {"the destination already exists and is a system file", windows.ERROR_ALREADY_EXISTS},
	0x8027002C: {"the destination folder already exists", windows.ERROR_ALREADY_EXISTS},
	0x80270032: {"the destination disk is full", windows.ERROR_DISK_FULL},
	0x80270033: {"the destination disk is full", windows.ERROR_DISK_FULL},
	0x80270037: {"the item is too large for the Recycle Bin", windows.ERROR_FILE_TOO_LARGE},
	0x80270038: {"the path is too long for the Recycle Bin", windows.ERROR_FILENAME_EXCED_RANGE},
	0x8027003A: {"the Recycle Bin is not available", windows.ERROR_PATH_NOT_FOUND},
	0x8027003B: {"the name of the new file is too long", windows.ERROR_FILENAME_EXCED_RANGE},
	0x8027003C: {"the name of the new folder is too long", windows.ERROR_FILENAME_EXCED_RANGE},
	0x8027003D: {"the folder is not empty", windows.ERROR_DIR_NOT_EMPTY},
	0x8027003F: {"the item is read-only", windows.ERROR_ACCESS_DENIED},
}

func (e *comError) Error() string {
	if c, ok := copyErrors[e.hr]; ok {
		return c.text
	}
	// The system has messages for most HRESULTs, and for the Windows
	// errors some of them wrap.
	if s := systemMessage(uint32(e.hr)); s != "" {
		return s
	}
	if n := e.errno(); n != 0 {
		if s := systemMessage(uint32(n)); s != "" {
			return s
		}
	}
	return fmt.Sprintf("the system reported error 0x%08X", uint32(e.hr))
}

// errno is the Windows error the result wraps, or 0.
func (e *comError) errno() windows.Errno {
	if c, ok := copyErrors[e.hr]; ok {
		return c.errno
	}
	if e.hr>>16&0x1FFF == facilityWin32 {
		return windows.Errno(e.hr & 0xFFFF)
	}
	return 0
}

func (e *comError) Unwrap() error {
	if n := e.errno(); n != 0 {
		return n
	}
	return nil
}

// systemMessage is the system's text for an error, without the line end
// and the full stop it comes with, or "". It is in English where the
// system has that, as the texts of Go's own errors are, and else in the
// system's language.
func systemMessage(id uint32) string {
	const (
		flags     = windows.FORMAT_MESSAGE_FROM_SYSTEM | windows.FORMAT_MESSAGE_IGNORE_INSERTS
		englishUS = 0x0409
	)
	var buf [512]uint16
	for _, language := range []uint32{englishUS, 0} {
		n, err := windows.FormatMessage(flags, 0, id, language, buf[:], nil)
		if err == nil && n > 0 {
			return strings.TrimRight(windows.UTF16ToString(buf[:n]), ".。 \r\n")
		}
	}
	return ""
}

const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
)

// enterApartment makes the calling thread, which must be locked to its
// goroutine, a single-threaded COM apartment; with ole, one that can also
// own the clipboard and take part in drag and drop. It reports whether
// leaveApartment is owed.
func enterApartment(ole bool) bool {
	var hr uintptr
	if ole {
		hr, _, _ = procOleInitialize.Call(0)
	} else {
		hr, _, _ = procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	}
	// S_FALSE, for a thread that is one already, is owed a leave too.
	return !hresult(hr).failed()
}

func leaveApartment(ole bool) {
	if ole {
		procOleUninitialize.Call()
	} else {
		procCoUninitialize.Call()
	}
}

// onSTA runs fn on a thread of its own, made a single-threaded apartment
// for the call, and returns what it returns. The thread ends with the
// call: it suits what is over when it returns. What leaves windows or
// objects behind runs on a host thread instead (onHost).
func onSTA(fn func() error) error {
	done := make(chan error, 1)
	go func() {
		// Never unlocked: the thread ends with the goroutine, and takes
		// with it whatever the system tied to it.
		runtime.LockOSThread()
		if !enterApartment(false) {
			done <- fmt.Errorf("shell: cannot set up a thread for COM")
			return
		}
		err := fn()
		leaveApartment(false)
		done <- err
	}()
	return <-done
}

// coCreate makes an object of a class, in this process.
func coCreate(clsid, iid *windows.GUID) (*object, error) {
	const clsctxInprocServer = 0x1
	var o *object
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(clsid)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&o)))
	return o, hresult(hr).err()
}

// shellItem is the shell's item (an IShellItem) for a path, to be
// released.
func shellItem(path string) (*object, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var item *object
	hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
	return item, hresult(hr).err()
}

// pidl is an item's identifier in the shell's namespace (an ITEMIDLIST),
// which the system allocated: an address the package passes on and never
// reads.
type pidl uintptr

// parsePIDL is the absolute identifier of a path, to be freed.
func parsePIDL(path string) (pidl, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var id pidl
	hr, _, _ := procSHParseDisplayName.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&id)), 0, 0)
	return id, hresult(hr).err()
}

func (p pidl) free() {
	if p != 0 {
		procCoTaskMemFree.Call(uintptr(p))
	}
}

// freePIDLs frees every identifier of a list.
func freePIDLs(ids []pidl) {
	for _, id := range ids {
		id.free()
	}
}
