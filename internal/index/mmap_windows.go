package index

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// rest has the system take the pages of mapped memory out of what the
// program holds: they stay in the system's cache while there is room, and
// come back as they are touched.
func rest(data []byte) {
	if len(data) > 0 {
		// Unlocking what is not locked does this, and reports that it was
		// not locked.
		_ = windows.VirtualUnlock(uintptr(unsafe.Pointer(unsafe.SliceData(data))), uintptr(len(data)))
	}
}

var prefetch = windows.NewLazySystemDLL("kernel32.dll").NewProc("PrefetchVirtualMemory")

// warm has the system bring stretches of mapped memory in at once, which
// costs less than having each page brought in as it is touched.
func warm(stretches ...[]byte) {
	type entry struct{ addr, size uintptr }
	var entries [8]entry
	n := 0
	for _, s := range stretches {
		if len(s) > 0 && n < len(entries) {
			entries[n] = entry{uintptr(unsafe.Pointer(unsafe.SliceData(s))), uintptr(len(s))}
			n++
		}
	}
	if n > 0 && prefetch.Find() == nil {
		prefetch.Call(uintptr(windows.CurrentProcess()), uintptr(n), uintptr(unsafe.Pointer(&entries[0])), 0)
	}
}

// mapFile maps a file to read, and returns what unmaps it. The file may
// be deleted while it is mapped: it goes when it is unmapped.
func mapFile(path string) ([]byte, func(), error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return nil, nil, err
	}
	size := int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow)
	if size == 0 {
		windows.CloseHandle(h)
		return nil, nil, errors.New("empty file")
	}
	m, err := windows.CreateFileMapping(h, nil, windows.PAGE_READONLY, 0, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return nil, nil, err
	}
	addr, err := windows.MapViewOfFile(m, windows.FILE_MAP_READ, 0, 0, 0)
	if err != nil {
		windows.CloseHandle(m)
		windows.CloseHandle(h)
		return nil, nil, err
	}
	// The address is the system's, not the collector's: it is taken as a
	// pointer where the checks of pointer arithmetic do not look.
	data := unsafe.Slice(*(**byte)(unsafe.Pointer(&addr)), size)
	release := func() {
		windows.UnmapViewOfFile(addr)
		windows.CloseHandle(m)
		windows.CloseHandle(h)
	}
	return data, release, nil
}
