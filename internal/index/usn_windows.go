package index

import (
	"errors"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NTFS notes every change to a volume's files in a journal. A program
// without an administrator's rights may read it too, in a form that names
// the files changed by what the file system calls them and leaves out
// their names: the name of each is then asked of the file itself. With
// it an index follows the drive as it changes, and catches up, when the
// program starts, with what changed while it did not run.

const (
	fsctlQueryUsnJournal     = 0x000900f4
	fsctlReadUnprivilegedUsn = 0x000903ab

	usnReasonFileDelete    = 0x00000200
	usnReasonRenameOldName = 0x00001000
	usnReasonClose         = 0x80000000

	errJournalDeleteInProgress = windows.Errno(1178)
	errJournalNotActive        = windows.Errno(1179)
	errJournalEntryDeleted     = windows.Errno(1181)
)

// errJournalGone is returned when the journal no longer holds what was
// asked of it: the index must be built again.
var errJournalGone = errors.New("index: the journal of changes no longer reaches back to where the index is")

type usnJournalData struct {
	ID              uint64
	FirstUsn        int64
	NextUsn         int64
	LowestValidUsn  int64
	MaxUsn          int64
	MaximumSize     uint64
	AllocationDelta uint64
	Min, Max        uint16
}

type readUsnJournalData struct {
	StartUsn          int64
	ReasonMask        uint32
	ReturnOnlyOnClose uint32
	Timeout           uint64
	BytesToWaitFor    uint64
	JournalID         uint64
	Min, Max          uint16
}

// journal is the journal of changes of a volume, open to read.
type journal struct {
	h    windows.Handle
	root string
	ID   uint64
	// First and Next are where the journal starts and where it will
	// write next, as last asked.
	First, Next int64

	mu     sync.Mutex
	closed bool
	buf    []uint64
}

// openJournal opens the journal of the drive of a root, as "C:\".
func openJournal(root string) (*journal, error) {
	p, err := windows.UTF16PtrFromString(`\\.\` + root[:2])
	if err != nil {
		return nil, err
	}
	// The right to traverse is all the journal asks of who reads it this
	// way, and every user has it.
	h, err := windows.CreateFile(p, 0x20,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	j := &journal{h: h, root: root, buf: make([]uint64, 1<<13)}
	if err := j.query(); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return j, nil
}

func (j *journal) query() error {
	var d usnJournalData
	var n uint32
	if err := windows.DeviceIoControl(j.h, fsctlQueryUsnJournal, nil, 0,
		(*byte)(unsafe.Pointer(&d)), uint32(unsafe.Sizeof(d)), &n, nil); err != nil {
		return err
	}
	j.ID, j.First, j.Next = d.ID, d.FirstUsn, d.NextUsn
	return nil
}

// Close stops a read under way and lets go of the volume.
func (j *journal) Close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.closed {
		j.closed = true
		windows.CancelIoEx(j.h, nil)
		windows.CloseHandle(j.h)
	}
}

// read returns the files changed from a place in the journal on, and
// where to read next. With wait it returns only once something changed.
// Each file is told once its last user closed it, and once at most.
func (j *journal) read(from int64, wait bool, out []change) ([]change, int64, error) {
	req := readUsnJournalData{
		StartUsn: from, ReasonMask: 0xFFFFFFFF, ReturnOnlyOnClose: 1,
		JournalID: j.ID, Min: 2, Max: 3,
	}
	if wait {
		req.BytesToWaitFor = 1
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&j.buf[0])), len(j.buf)*8)
	var n uint32
	err := windows.DeviceIoControl(j.h, fsctlReadUnprivilegedUsn,
		(*byte)(unsafe.Pointer(&req)), uint32(unsafe.Sizeof(req)), &buf[0], uint32(len(buf)), &n, nil)
	if err != nil {
		switch err {
		case errJournalEntryDeleted, errJournalNotActive, errJournalDeleteInProgress, windows.ERROR_INVALID_PARAMETER:
			return out, from, errJournalGone
		}
		return out, from, err
	}
	if n < 8 {
		return out, from, nil
	}
	next := int64(le.Uint64(buf))
	for off := uint32(8); off+60 <= n; {
		length := le.Uint32(buf[off:])
		if length < 60 || off+length > n {
			break
		}
		rec := buf[off : off+length]
		var c change
		var reason uint32
		switch le.Uint16(rec[4:]) {
		case 2:
			c.id, c.parent = le.Uint64(rec[8:]), le.Uint64(rec[16:])
			reason = le.Uint32(rec[40:])
		case 3:
			// The first half of the 128 bits is what NTFS calls the file.
			c.id, c.parent = le.Uint64(rec[8:]), le.Uint64(rec[24:])
			reason = le.Uint32(rec[56:])
		default:
			off += length
			continue
		}
		off += length
		if reason&usnReasonRenameOldName != 0 && reason&usnReasonClose == 0 {
			continue
		}
		c.gone = reason&usnReasonFileDelete != 0
		out = append(out, c)
	}
	return out, next, nil
}

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procOpenFileByID = kernel32.NewProc("OpenFileById")
)

type fileIDDescriptor struct {
	Size uint32
	Type uint32
	ID   [16]byte
}

// stat asks a file, by what the file system calls it, for what an index
// keeps of it. It reports false for a file that is gone, or that the
// user may not look at.
func (j *journal) stat(id uint64) (Change, bool) {
	d := fileIDDescriptor{Size: 24}
	le.PutUint64(d.ID[:], id)
	r, _, _ := procOpenFileByID.Call(uintptr(j.h), uintptr(unsafe.Pointer(&d)),
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, 0,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT)
	h := windows.Handle(r)
	if h == windows.InvalidHandle {
		return Change{}, false
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil {
		return Change{}, false
	}
	// The name comes as the path from the volume's root.
	var nameBuf [600]uint16
	if windows.GetFileInformationByHandleEx(h, windows.FileNameInfo,
		(*byte)(unsafe.Pointer(&nameBuf[0])), uint32(len(nameBuf)*2)) != nil {
		return Change{}, false
	}
	length := *(*uint32)(unsafe.Pointer(&nameBuf[0])) / 2
	if int(length) > len(nameBuf)-2 {
		return Change{}, false
	}
	path := windows.UTF16ToString(nameBuf[2 : 2+length])
	name := path[strings.LastIndexByte(path, '\\')+1:]
	if name == "" {
		return Change{}, false
	}
	var tag uint32
	if info.FileAttributes&attrReparse != 0 {
		var t struct{ Attrs, Tag uint32 }
		if windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo,
			(*byte)(unsafe.Pointer(&t)), uint32(unsafe.Sizeof(t))) == nil {
			tag = t.Tag
		}
	}
	c := Change{Name: name}
	c.ID = id
	c.Flags = flagsOf(info.FileAttributes, tag)
	if c.Flags&FlagDir == 0 {
		c.Size = uint64(info.FileSizeHigh)<<32 | uint64(info.FileSizeLow)
	}
	ft := func(t windows.Filetime) uint32 { return unixSeconds(int64(t.HighDateTime)<<32 | int64(t.LowDateTime)) }
	c.Modified, c.Created, c.Accessed = ft(info.LastWriteTime), ft(info.CreationTime), ft(info.LastAccessTime)
	return c, true
}

// parentOf is the folder a file is in, asked of the file itself.
func (j *journal) parentOf(id uint64) (uint64, bool) {
	d := fileIDDescriptor{Size: 24}
	le.PutUint64(d.ID[:], id)
	r, _, _ := procOpenFileByID.Call(uintptr(j.h), uintptr(unsafe.Pointer(&d)),
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, 0,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT)
	h := windows.Handle(r)
	if h == windows.InvalidHandle {
		return 0, false
	}
	defer windows.CloseHandle(h)
	// The path of the folder it is in, and that folder asked what the
	// file system calls it.
	var buf [1024]uint16
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil || int(n) >= len(buf) {
		return 0, false
	}
	path := windows.UTF16ToString(buf[:n])
	cut := strings.LastIndexByte(path, '\\')
	if cut < 0 {
		return 0, false
	}
	dir := path[:cut+1]
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	dh, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(dh)
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(dh, &info) != nil {
		return 0, false
	}
	return uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), true
}
