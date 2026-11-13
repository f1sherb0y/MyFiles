package index

import (
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A drive whose file system keeps no journal of its changes, as a USB
// stick's FAT or a share, is followed by asking the system to tell of
// what changes under its root as it changes. Unlike the journal, that
// tells nothing of what happened while nobody listened.

// watcher listens to what changes under a folder.
type watcher struct {
	h    windows.Handle
	buf  []byte
	once sync.Once
}

func openWatcher(root string) (pathWatcher, error) {
	p, err := windows.UTF16PtrFromString(longPath(root))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, err
	}
	// 64 KB is the most a share's server takes.
	return &watcher{h: h, buf: make([]byte, 64<<10)}, nil
}

// read waits for changes and returns them: errLost when more happened
// than the system kept for it.
func (w *watcher) read() ([]pathEvent, error) {
	const mask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
		windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE | windows.FILE_NOTIFY_CHANGE_ATTRIBUTES
	var n uint32
	err := windows.ReadDirectoryChanges(w.h, &w.buf[0], uint32(len(w.buf)), true, mask, &n, nil, 0)
	switch {
	case errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR):
		return nil, errLost
	case err != nil:
		return nil, err
	case n == 0:
		return nil, errLost
	}
	var out []pathEvent
	for off := uint32(0); off+12 <= n; {
		next := *(*uint32)(unsafe.Pointer(&w.buf[off]))
		action := *(*uint32)(unsafe.Pointer(&w.buf[off+4]))
		size := *(*uint32)(unsafe.Pointer(&w.buf[off+8]))
		if off+12+size > n {
			break
		}
		name := unsafe.Slice((*uint16)(unsafe.Pointer(&w.buf[off+12])), size/2)
		out = append(out, pathEvent{rel: windows.UTF16ToString(name), action: action})
		if next == 0 {
			break
		}
		off += next
	}
	return out, nil
}

func (w *watcher) Close() {
	w.once.Do(func() {
		// What waits in read comes back.
		windows.CancelIoEx(w.h, nil)
		windows.CloseHandle(w.h)
	})
}

// statPath is a file as it is now, by its path: false for one that is
// not there.
func statPath(path string) (Change, bool) {
	p, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return Change{}, false
	}
	var d windows.Win32finddata
	h, err := windows.FindFirstFile(p, &d)
	if err != nil {
		return Change{}, false
	}
	windows.FindClose(h)
	c := Change{Name: windows.UTF16ToString(d.FileName[:])}
	c.Flags = flagsOf(d.FileAttributes, d.Reserved0)
	c.Modified = unixSeconds(int64(d.LastWriteTime.HighDateTime)<<32 | int64(d.LastWriteTime.LowDateTime))
	c.Created = unixSeconds(int64(d.CreationTime.HighDateTime)<<32 | int64(d.CreationTime.LowDateTime))
	c.Accessed = unixSeconds(int64(d.LastAccessTime.HighDateTime)<<32 | int64(d.LastAccessTime.LowDateTime))
	if c.Flags&FlagDir == 0 {
		c.Size = uint64(d.FileSizeHigh)<<32 | uint64(d.FileSizeLow)
	}
	return c, true
}
