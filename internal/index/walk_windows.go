package index

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The classes of GetFileInformationByHandleEx that list a directory with
// what the file system calls each entry.
const (
	fileIdBothDirectoryInfo        = 10
	fileIdBothDirectoryRestartInfo = 11
)

// idBothDirInfo is FILE_ID_BOTH_DIR_INFO up to its name.
type idBothDirInfo struct {
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
	ShortNameLength int8
	ShortName       [12]uint16
	FileID          uint64
}

const idBothDirName = 104

// longPath is a path as the system takes paths of any length.
func longPath(path string) string {
	switch {
	case strings.HasPrefix(path, `\\?\`), strings.HasPrefix(path, `\\.\`):
		return path
	case strings.HasPrefix(path, `\\`):
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}

// WalkOptions are how a walk reads a tree.
type WalkOptions struct {
	// IDs keeps what the file system calls each file, which the journal
	// of changes names files by. Without it the files are numbered as
	// they are met.
	IDs bool
	// Workers is how many folders are read at once, 0 for as many as
	// suits the machine.
	Workers int
	// Progress, when set, counts the files met so far.
	Progress *atomic.Int64
	// Scratch is where what is found is kept, nil for the program's own
	// memory.
	Scratch *Scratch
}

// numbers gives the files of a file system that does not name them
// numbers of the walk's own, a goroutine a stretch at a time: from 1 on
// with few left out, so that a table finds a file by its number as it
// finds one of NTFS by its record.
type numbers struct {
	from      *atomic.Uint64
	at, until uint64
}

const numbersAtATime = 4096

func (n *numbers) next() uint64 {
	if n.at == n.until {
		n.until = n.from.Add(numbersAtATime)
		n.at = n.until - numbersAtATime
	}
	n.at++
	return n.at
}

type walkJob struct {
	// path is the folder, with its trailing separator, and id what its
	// entries name it by. A folder of a file system that names its files
	// has no path here: it is opened by what it is called, and the
	// hundred thousand folders of one folder wait as eight bytes each.
	path []uint16
	id   uint64
	// flags are how the folder is opened: all but the root as what they
	// are, so that a link the listing of its folder did not tell of, as
	// happens, is found empty and not walked through.
	flags uint32
}

// Walk reads the folders under a root, several at once, and returns what
// it found with what the file system calls the root. It asks the system
// for each folder's entries with their sizes and times in one call, and
// opens no file. Folders that may not be read are left out, links to
// other folders are not followed, and a cloud's folders that are not on
// the disk are not fetched.
func Walk(ctx context.Context, root string, opts WalkOptions) ([]*Scan, uint64, error) {
	if !strings.HasSuffix(root, `\`) {
		root += `\`
	}
	p, err := windows.UTF16FromString(longPath(root))
	if err != nil {
		return nil, 0, err
	}
	p = p[:len(p)-1]
	rootID := uint64(1) << 56
	h, err := openDir(p, 0)
	if err != nil {
		return nil, 0, err
	}
	// vol is the root, kept open to open folders by what they are called.
	vol := windows.InvalidHandle
	if opts.IDs {
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err == nil {
			rootID = uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
			vol = h
		}
	}
	if vol == windows.InvalidHandle {
		windows.CloseHandle(h)
	} else {
		defer windows.CloseHandle(vol)
	}

	workers := opts.Workers
	if workers <= 0 {
		// More than the cores of a machine do not read a disk faster,
		// and some drivers read slower the more is asked of them at once.
		workers = min(max(runtime.GOMAXPROCS(0)/2, 4), 12)
	}
	var (
		mu      sync.Mutex
		cond    = sync.NewCond(&mu)
		stack   = []walkJob{{path: p, id: rootID}}
		pending = 1
	)
	// A folder's files are met together, and wanted together when the
	// index is made.
	opts.Scratch.Near()
	scans := make([]*Scan, workers)
	var numbered atomic.Uint64
	var wg sync.WaitGroup
	for wi := range workers {
		s := NewScan(opts.Scratch)
		scans[wi] = s
		wg.Go(func() {
			buf := make([]byte, 128<<10)
			ids := numbers{from: &numbered}
			var kids []walkJob
			for {
				mu.Lock()
				for len(stack) == 0 && pending > 0 {
					cond.Wait()
				}
				if pending == 0 {
					mu.Unlock()
					cond.Broadcast()
					return
				}
				job := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				mu.Unlock()

				kids = kids[:0]
				if ctx.Err() == nil {
					before := s.n
					kids = listDir(vol, job, s, buf, opts.IDs, &ids, kids)
					if opts.Progress != nil {
						opts.Progress.Add(int64(s.n - before))
					}
				}
				mu.Lock()
				stack = append(stack, kids...)
				pending += len(kids) - 1
				mu.Unlock()
				if len(kids) > 1 || pending == 0 {
					cond.Broadcast()
				} else if len(kids) == 1 {
					cond.Signal()
				}
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return scans, rootID, nil
}

func openDir(path []uint16, flags uint32) (windows.Handle, error) {
	p := append(path[:len(path):len(path)], 0)
	return windows.CreateFile(&p[0], windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|flags, 0)
}

// listDir reads one folder into a scan, and appends the folders it holds
// to kids.
func listDir(vol windows.Handle, job walkJob, s *Scan, buf []byte, ids bool, own *numbers, kids []walkJob) []walkJob {
	var h windows.Handle
	if job.path == nil {
		d := fileIDDescriptor{Size: 24}
		le.PutUint64(d.ID[:], job.id)
		r, _, _ := procOpenFileByID.Call(uintptr(vol), uintptr(unsafe.Pointer(&d)),
			windows.FILE_LIST_DIRECTORY,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, 0,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT)
		if h = windows.Handle(r); h == windows.InvalidHandle {
			return kids
		}
	} else {
		var err error
		if h, err = openDir(job.path, job.flags); err != nil {
			return kids
		}
	}
	defer windows.CloseHandle(h)
	class := uint32(fileIdBothDirectoryRestartInfo)
	for {
		if windows.GetFileInformationByHandleEx(h, class, &buf[0], uint32(len(buf))) != nil {
			return kids
		}
		class = fileIdBothDirectoryInfo
		for off := uint32(0); ; {
			info := (*idBothDirInfo)(unsafe.Pointer(&buf[off]))
			name := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[off+idBothDirName])), info.FileNameLength/2)
			if !(len(name) == 1 && name[0] == '.' || len(name) == 2 && name[0] == '.' && name[1] == '.') {
				e := Entry{
					ID:       info.FileID,
					Parent:   job.id,
					Flags:    flagsOf(info.FileAttributes, info.EaSize),
					Modified: unixSeconds(info.LastWriteTime),
					Created:  unixSeconds(info.CreationTime),
					Accessed: unixSeconds(info.LastAccessTime),
				}
				if !ids {
					e.ID = own.next()
				}
				if e.Flags&FlagDir == 0 {
					e.Size = uint64(info.EndOfFile)
				} else if e.Flags&FlagLink != 0 || info.FileAttributes&attrRecallOnOpen != 0 {
					// Not walked into.
				} else if vol != windows.InvalidHandle {
					kids = append(kids, walkJob{id: e.ID})
				} else {
					child := make([]uint16, 0, len(job.path)+len(name)+1)
					child = append(append(append(child, job.path...), name...), '\\')
					kids = append(kids, walkJob{child, e.ID, windows.FILE_FLAG_OPEN_REPARSE_POINT})
				}
				s.Add(e, name)
			}
			if info.NextEntryOffset == 0 {
				break
			}
			off += info.NextEntryOffset
		}
	}
}
