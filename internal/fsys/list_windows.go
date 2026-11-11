package fsys

import (
	"context"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The directory information classes of GetFileInformationByHandleEx.
const (
	fileFullDirectoryInfo        = 14
	fileFullDirectoryRestartInfo = 15
)

const (
	tagMountPoint = 0xA0000003
	tagSymlink    = 0xA000000C
)

// fullDirInfo is FILE_FULL_DIR_INFO up to its name, which follows it.
type fullDirInfo struct {
	NextEntryOffset uint32
	FileIndex       uint32
	CreationTime    int64
	LastAccessTime  int64
	LastWriteTime   int64
	ChangeTime      int64
	EndOfFile       int64
	AllocationSize  int64
	FileAttributes  uint32
	FileNameLength  uint32
	EaSize          uint32
}

const fullDirInfoName = 68 // the offset of FileName, past EaSize

// listBuffer is how much the system fills per call: large enough that most
// directories come in one call, and the largest in a few dozen.
const listBuffer = 256 << 10

// epochDiff is the 100 ns intervals from 1601 to 1970.
const epochDiff = 116444736000000000

func unixNanos(ft int64) int64 {
	if ft <= 0 {
		return 0
	}
	return (ft - epochDiff) * 100
}

// LongPath is path as the system takes paths of any length: with the \\?\
// prefix, which also turns off the parsing of trailing dots and spaces.
func LongPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}

// List reads a directory whole, unsorted. It asks the system for every
// entry's name, size, times and attributes in one pass
// (FILE_FULL_DIR_INFO), which is what the directory's index holds: no file
// is opened. ctx stops a listing nobody waits for any more.
func List(ctx context.Context, path string) (*Listing, error) {
	start := time.Now()
	dir := path
	if !strings.HasSuffix(dir, `\`) {
		dir += `\`
	}
	p, err := windows.UTF16PtrFromString(LongPath(dir))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &PathError{Op: "open", Path: path, Err: err}
	}
	defer windows.CloseHandle(h)

	buf := make([]byte, listBuffer)
	l := &Listing{Path: path}
	class := uint32(fileFullDirectoryRestartInfo)
	var scratch []byte
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		err := windows.GetFileInformationByHandleEx(h, class, &buf[0], uint32(len(buf)))
		class = fileFullDirectoryInfo
		if err != nil {
			if err == windows.ERROR_NO_MORE_FILES || err == windows.ERROR_FILE_NOT_FOUND {
				break
			}
			return nil, &PathError{Op: "read", Path: path, Err: err}
		}
		// The names of one buffer share one allocation: a pass to size
		// it, and a pass to fill it.
		n, bytes := 0, 0
		for off := uint32(0); ; {
			info := (*fullDirInfo)(unsafe.Pointer(&buf[off]))
			n++
			bytes += int(info.FileNameLength/2) * 3
			if info.NextEntryOffset == 0 {
				break
			}
			off += info.NextEntryOffset
		}
		if cap(l.Entries)-len(l.Entries) < n {
			grown := make([]Entry, len(l.Entries), max(2*cap(l.Entries), len(l.Entries)+n))
			copy(grown, l.Entries)
			l.Entries = grown
		}
		scratch = make([]byte, 0, bytes)
		for off := uint32(0); ; {
			info := (*fullDirInfo)(unsafe.Pointer(&buf[off]))
			name := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[off+fullDirInfoName])), info.FileNameLength/2)
			if !isDots(name) {
				at := len(scratch)
				scratch = appendUTF16(scratch, name)
				e := Entry{
					Name:    unsafe.String(unsafe.SliceData(scratch[at:]), len(scratch)-at),
					ModTime: unixNanos(info.LastWriteTime),
					Created: unixNanos(info.CreationTime),
					Attr:    attrOf(info.FileAttributes, info.EaSize),
				}
				if e.Attr&AttrDir == 0 {
					e.Size = info.EndOfFile
				}
				l.Entries = append(l.Entries, e)
			}
			if info.NextEntryOffset == 0 {
				break
			}
			off += info.NextEntryOffset
		}
	}
	l.Took = time.Since(start)
	return l, nil
}

func isDots(name []uint16) bool {
	return len(name) == 1 && name[0] == '.' || len(name) == 2 && name[0] == '.' && name[1] == '.'
}

func attrOf(fa, tag uint32) Attr {
	var a Attr
	if fa&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		a |= AttrDir
		if fa&(windows.FILE_ATTRIBUTE_READONLY|windows.FILE_ATTRIBUTE_SYSTEM) != 0 {
			a |= AttrCustomIcon
		}
	}
	if fa&windows.FILE_ATTRIBUTE_HIDDEN != 0 {
		a |= AttrHidden
	}
	if fa&windows.FILE_ATTRIBUTE_SYSTEM != 0 {
		a |= AttrSystem
	}
	if fa&windows.FILE_ATTRIBUTE_READONLY != 0 {
		a |= AttrReadOnly
	}
	if fa&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && (tag == tagMountPoint || tag == tagSymlink) {
		a |= AttrLink
	}
	const recall = 0x00040000 | 0x00400000 // RECALL_ON_OPEN, RECALL_ON_DATA_ACCESS
	if fa&(windows.FILE_ATTRIBUTE_OFFLINE|recall) != 0 {
		a |= AttrOffline
	}
	return a
}

// appendUTF16 appends s to b as UTF-8. Most names are ASCII, which takes a
// byte each.
func appendUTF16(b []byte, s []uint16) []byte {
	for i := 0; i < len(s); i++ {
		u := s[i]
		switch {
		case u < 0x80:
			b = append(b, byte(u))
		case utf16.IsSurrogate(rune(u)):
			r := rune(utf8.RuneError)
			if i+1 < len(s) {
				if d := utf16.DecodeRune(rune(u), rune(s[i+1])); d != utf8.RuneError {
					r = d
					i++
				}
			}
			b = utf8.AppendRune(b, r)
		default:
			b = utf8.AppendRune(b, rune(u))
		}
	}
	return b
}

// Stat describes one entry by its path.
func Stat(path string) (Entry, error) {
	p, err := windows.UTF16PtrFromString(LongPath(path))
	if err != nil {
		return Entry{}, err
	}
	var d windows.Win32FileAttributeData
	if err := windows.GetFileAttributesEx(p, windows.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&d))); err != nil {
		return Entry{}, &PathError{Op: "stat", Path: path, Err: err}
	}
	e := Entry{
		Name:    Base(path),
		ModTime: unixNanos(int64(d.LastWriteTime.HighDateTime)<<32 | int64(d.LastWriteTime.LowDateTime)),
		Created: unixNanos(int64(d.CreationTime.HighDateTime)<<32 | int64(d.CreationTime.LowDateTime)),
		Attr:    attrOf(d.FileAttributes, 0),
	}
	if e.Attr&AttrDir == 0 {
		e.Size = int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow)
	}
	return e, nil
}
