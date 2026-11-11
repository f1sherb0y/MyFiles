package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// What the shell says of a folder, asked the way Explorer++ asks, so that
// what Explorer++ shows can be checked against it: Explorer++ lists a
// folder through the shell (IShellFolder::EnumObjects), not through the
// file system, and the two do not always agree.

var (
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	procSHParseDisplayName  = shell32.NewProc("SHParseDisplayName")
	procSHGetNameFromIDList = shell32.NewProc("SHGetNameFromIDList")
	procSHBindToObject      = shell32.NewProc("SHBindToObject")
)

// iidShellFolder is IID_IShellFolder.
var iidShellFolder = windows.GUID{Data1: 0x000214E6, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}

// comObject is a COM interface: a pointer to its table of methods.
type comObject struct {
	methods *[8]uintptr
}

// The methods used, by their place in their interfaces' tables.
const (
	unknownRelease         = 2
	shellFolderEnumObjects = 4
	enumIDListNext         = 3
)

func (o *comObject) call(method int, args ...uintptr) (hresult int32) {
	r, _, _ := syscall.SyscallN(o.methods[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return int32(r)
}

// itemIDList is the shell's name for a folder: the item ID list its path
// parses to. The caller frees it with CoTaskMemFree.
func itemIDList(path string) (unsafe.Pointer, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var pidl unsafe.Pointer
	if hr, _, _ := procSHParseDisplayName.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&pidl)), 0, 0); int32(hr) < 0 {
		return nil, fmt.Errorf("the shell cannot parse %s: %#x", path, uint32(hr))
	}
	return pidl, nil
}

// displayName is the name the shell shows for a folder: "Windows" for
// C:\Windows, "Local Disk (C:)" for C:\, the user's own name for their
// profile folder. It is what Explorer++ puts in its window's title
// (SHGDN_NORMAL).
func displayName(path string) string {
	pidl, err := itemIDList(path)
	if err != nil {
		return ""
	}
	defer windows.CoTaskMemFree(pidl)
	var name *uint16
	const sigdnNormalDisplay = 0
	if hr, _, _ := procSHGetNameFromIDList.Call(uintptr(pidl), sigdnNormalDisplay,
		uintptr(unsafe.Pointer(&name))); int32(hr) < 0 {
		return ""
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(name))
	return windows.UTF16PtrToString(name)
}

// shellEntries counts what the shell enumerates in a folder, hidden and
// protected system items included: what Explorer++ lists there with its
// default settings (ShellBrowser::EnumerateFolder).
func shellEntries(path string) (int, error) {
	pidl, err := itemIDList(path)
	if err != nil {
		return 0, err
	}
	defer windows.CoTaskMemFree(pidl)

	var folder *comObject
	if hr, _, _ := procSHBindToObject.Call(0, uintptr(pidl), 0,
		uintptr(unsafe.Pointer(&iidShellFolder)), uintptr(unsafe.Pointer(&folder))); int32(hr) < 0 {
		return 0, fmt.Errorf("the shell cannot open %s: %#x", path, uint32(hr))
	}
	defer folder.call(unknownRelease)

	const (
		folders            = 0x20    // SHCONTF_FOLDERS
		nonFolders         = 0x40    // SHCONTF_NONFOLDERS
		includeHidden      = 0x80    // SHCONTF_INCLUDEHIDDEN
		includeSuperHidden = 0x10000 // SHCONTF_INCLUDESUPERHIDDEN
	)
	var list *comObject
	hr := folder.call(shellFolderEnumObjects, 0,
		folders|nonFolders|includeHidden|includeSuperHidden, uintptr(unsafe.Pointer(&list)))
	if hr < 0 {
		return 0, fmt.Errorf("the shell cannot list %s: %#x", path, uint32(hr))
	}
	if list == nil { // S_FALSE: nothing in it
		return 0, nil
	}
	defer list.call(unknownRelease)

	n := 0
	for {
		var item unsafe.Pointer
		var fetched uint32
		if hr := list.call(enumIDListNext, 1, uintptr(unsafe.Pointer(&item)), uintptr(unsafe.Pointer(&fetched))); hr != 0 || fetched != 1 {
			return n, nil
		}
		windows.CoTaskMemFree(item)
		n++
	}
}
