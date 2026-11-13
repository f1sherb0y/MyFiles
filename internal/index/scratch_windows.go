package index

import (
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// scratchFile is a scratch's temporary file: it goes when it is closed,
// or when the program ends however it ends.
type scratchFile struct {
	h     windows.Handle
	size  int64
	views []uintptr
}

var scratchCount atomic.Int64

// grow makes the file n bytes longer and maps what was added.
func (f *scratchFile) grow(dir string, n int) ([]byte, error) {
	if f.h == 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		name := "scratch-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(scratchCount.Add(1), 10) + ".tmp"
		p, err := windows.UTF16PtrFromString(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_DELETE, nil, windows.CREATE_ALWAYS,
			windows.FILE_ATTRIBUTE_TEMPORARY|windows.FILE_FLAG_DELETE_ON_CLOSE, 0)
		if err != nil {
			return nil, err
		}
		f.h = h
	}
	end := f.size + int64(n)
	m, err := windows.CreateFileMapping(f.h, nil, windows.PAGE_READWRITE, uint32(end>>32), uint32(end), nil)
	if err != nil {
		return nil, err
	}
	addr, err := windows.MapViewOfFile(m, windows.FILE_MAP_WRITE, uint32(f.size>>32), uint32(f.size), uintptr(n))
	// The view keeps what it maps: the mapping's handle is not needed.
	windows.CloseHandle(m)
	if err != nil {
		return nil, err
	}
	f.size = end
	f.views = append(f.views, addr)
	return unsafe.Slice(*(**byte)(unsafe.Pointer(&addr)), n), nil
}

func (f *scratchFile) close() {
	for _, addr := range f.views {
		windows.UnmapViewOfFile(addr)
	}
	f.views = nil
	if f.h != 0 {
		windows.CloseHandle(f.h)
		f.h = 0
	}
	f.size = 0
}

// createMapped makes a file of a size and maps it to write. done unmaps
// it and, with keep, has what was written be on the disk; without, the
// file is removed.
func createMapped(path string, size int64) (data []byte, done func(keep bool) error, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.CREATE_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, nil, err
	}
	m, err := windows.CreateFileMapping(h, nil, windows.PAGE_READWRITE, uint32(size>>32), uint32(size), nil)
	if err != nil {
		windows.CloseHandle(h)
		os.Remove(path)
		return nil, nil, err
	}
	addr, err := windows.MapViewOfFile(m, windows.FILE_MAP_WRITE, 0, 0, uintptr(size))
	windows.CloseHandle(m)
	if err != nil {
		windows.CloseHandle(h)
		os.Remove(path)
		return nil, nil, err
	}
	data = unsafe.Slice(*(**byte)(unsafe.Pointer(&addr)), size)
	done = func(keep bool) error {
		var err error
		if keep {
			err = windows.FlushViewOfFile(addr, 0)
		}
		windows.UnmapViewOfFile(addr)
		if keep && err == nil {
			err = windows.FlushFileBuffers(h)
		}
		windows.CloseHandle(h)
		if !keep || err != nil {
			os.Remove(path)
		}
		return err
	}
	return data, done, nil
}
