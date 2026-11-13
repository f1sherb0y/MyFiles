package content

import (
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The text of documents that are not their text, as PDFs, is read by the
// readers the system has for them (IFilter): the ones Windows Search
// reads documents with, one for each kind of file that has one installed.

var (
	query           = windows.NewLazySystemDLL("query.dll")
	procLoadIFilter = query.NewProc("LoadIFilter")
	ole32           = windows.NewLazySystemDLL("ole32.dll")
	procCoInit      = ole32.NewProc("CoInitializeEx")
	procCoUninit    = ole32.NewProc("CoUninitialize")
)

// The methods of IFilter, by their places in its table.
const (
	filterRelease  = 2
	filterInit     = 3
	filterGetChunk = 4
	filterGetText  = 5

	// What Init is asked: paragraphs, line breaks, hyphens and spaces in
	// their canonical forms, and the attributes an index would take.
	filterInitFlags = 0x1 | 0x2 | 0x4 | 0x8 | 0x10 | 0x40

	chunkText = 0x1

	filterEndOfChunks  = 0x80041700
	filterNoMoreText   = 0x80041701
	filterLastText     = 0x00041709
	filterNoEmbedding  = 0x80041707
	filterNoLink       = 0x80041708
	filterAccessDenied = 0x80041703
)

// statChunk is STAT_CHUNK.
type statChunk struct {
	id        uint32
	breakType uint32
	flags     uint32
	locale    uint32
	propSet   windows.GUID
	propKind  uint32
	_         uint32
	propID    uintptr
	source    uint32
	start     uint32
	length    uint32
	_         uint32
}

// extractor reads documents on a thread of its own, which the readers
// are made on and used from.
type extractor struct {
	locked bool
	com    bool
	buf    []uint16
}

func newExtractor() *extractor {
	e := &extractor{}
	if procLoadIFilter.Find() != nil {
		return e
	}
	runtime.LockOSThread()
	e.locked = true
	// S_OK, or S_FALSE where the thread has an apartment already.
	r, _, _ := procCoInit.Call(0, 2 /* an apartment of its own */)
	e.com = int32(r) >= 0
	e.buf = make([]uint16, 16<<10)
	return e
}

func (e *extractor) close() {
	if e.com {
		procCoUninit.Call()
	}
	if e.locked {
		runtime.UnlockOSThread()
	}
}

// reader is an IFilter: an object of the system's, which starts with
// where its methods are.
type reader struct {
	methods *[8]uintptr
}

func (f *reader) call(method int, args ...uintptr) uint32 {
	all := append([]uintptr{uintptr(unsafe.Pointer(f))}, args...)
	r, _, _ := syscall.SyscallN(f.methods[method], all...)
	return uint32(r)
}

// text is a document's text, by the system's reader for its kind: false
// when there is none, or it does not read the file.
func (e *extractor) text(path string) (string, bool) {
	if !e.com {
		return "", false
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	var filter *reader
	if r, _, _ := procLoadIFilter.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&filter))); int32(r) < 0 || filter == nil {
		return "", false
	}
	defer filter.call(filterRelease)
	var flags uint32
	if int32(filter.call(filterInit, filterInitFlags, 0, 0, uintptr(unsafe.Pointer(&flags)))) < 0 {
		return "", false
	}
	var b strings.Builder
	for b.Len() < maxText {
		var chunk statChunk
		hr := filter.call(filterGetChunk, uintptr(unsafe.Pointer(&chunk)))
		if hr == filterNoEmbedding || hr == filterNoLink || hr == filterAccessDenied {
			continue
		}
		if int32(hr) < 0 {
			// The end, or what the reader cannot go on from.
			break
		}
		if chunk.flags&chunkText == 0 {
			continue
		}
		if chunk.breakType != 0 && b.Len() > 0 {
			b.WriteByte('\n')
		}
		for b.Len() < maxText {
			n := uint32(len(e.buf))
			hr := filter.call(filterGetText, uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&e.buf[0])))
			if int32(hr) < 0 {
				break
			}
			b.WriteString(windows.UTF16ToString(e.buf[:min(int(n), len(e.buf))]))
			if hr == filterLastText {
				break
			}
		}
	}
	return b.String(), b.Len() > 0
}

// ansi reads bytes in the system's code page.
func ansi(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	n, err := windows.MultiByteToWideChar(0, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return string(b)
	}
	u := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(0, 0, &b[0], int32(len(b)), &u[0], n); err != nil {
		return string(b)
	}
	return windows.UTF16ToString(u)
}
