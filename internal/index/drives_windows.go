package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// localDrives lists the drives with a letter that hold files.
func localDrives() []DriveInfo {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []DriveInfo
	for i := range 26 {
		if mask&(1<<i) == 0 {
			continue
		}
		if info, ok := driveInfo(string(rune('A'+i)) + `:\`); ok {
			out = append(out, info)
		}
	}
	return out
}

// driveInfo tells of the drive of a root, or of a folder to index as one.
func driveInfo(root string) (DriveInfo, bool) {
	if !strings.HasSuffix(root, `\`) {
		root += `\`
	}
	info := DriveInfo{Root: root}
	vol := filepath.VolumeName(root) + `\`
	p, err := windows.UTF16PtrFromString(vol)
	if err != nil {
		return info, false
	}
	kind := windows.GetDriveType(p)
	switch kind {
	case windows.DRIVE_NO_ROOT_DIR, windows.DRIVE_CDROM, windows.DRIVE_UNKNOWN:
		return info, false
	}
	var fs [64]uint16
	if windows.GetVolumeInformation(p, nil, 0, &info.Serial, nil, nil, &fs[0], uint32(len(fs))) != nil {
		// A drive with nothing in it, or one that does not answer.
		return info, false
	}
	info.FileSystem = windows.UTF16ToString(fs[:])
	whole := len(root) == 3
	info.Fixed = kind == windows.DRIVE_FIXED && whole
	// The journal names files of the whole volume: an index of a folder
	// alone has no use for it.
	info.Journal = info.FileSystem == "NTFS" && whole && kind != windows.DRIVE_REMOTE
	return info, true
}

// readDrive reads all a drive holds: from its file table where the file
// system has one and the program may read it, else folder by folder.
func readDrive(ctx context.Context, info DriveInfo, seen *atomic.Int64, sc *Scratch) (scans []*Scan, rootID uint64, table bool, err error) {
	if info.FileSystem == "NTFS" && len(info.Root) == 3 {
		scans, rootID, err = ScanVolume(ctx, info.Root, seen, sc)
		if err == nil {
			return scans, rootID, true, nil
		}
		if ctx.Err() != nil {
			return nil, 0, false, ctx.Err()
		}
		// Not an administrator, most often: the folders, then.
		seen.Store(0)
	}
	opts := WalkOptions{IDs: info.Journal, Progress: seen, Scratch: sc}
	if info.FileSystem != "NTFS" {
		// Other file systems' drivers are often slow, and slower under
		// load: a few folders at a time.
		opts.Workers = 4
	}
	scans, rootID, err = Walk(ctx, info.Root, opts)
	return scans, rootID, false, err
}

// usnFollower is a volume's journal as the manager follows it.
type usnFollower struct {
	j *journal
}

func openFollower(root string) (follower, error) {
	j, err := openJournal(root)
	if err != nil {
		return nil, err
	}
	return usnFollower{j}, nil
}

func (f usnFollower) Position() (uint64, int64)  { return f.j.ID, f.j.First }
func (f usnFollower) Position2() (uint64, int64) { return f.j.ID, f.j.Next }
func (f usnFollower) Refresh() error             { return f.j.query() }
func (f usnFollower) Close()                     { f.j.Close() }

func (f usnFollower) Read(from int64, wait bool) ([]change, int64, error) {
	return f.j.read(from, wait, nil)
}

func (f usnFollower) Stat(id uint64) (Change, bool) { return f.j.stat(id) }

func (f usnFollower) ParentOf(id uint64) (uint64, bool) { return f.j.parentOf(id) }

// CanReadTables reports whether the program may read the drives' file
// tables: whether it runs as administrator.
func CanReadTables() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// Helper runs the part of the program that reads drives' file tables as
// administrator, started by Elevate: it reads each root's table and
// writes its index into dir. It returns the process's exit code.
func Helper(dir string, roots []string) int {
	store := Store{Dir: dir}
	code := 0
	for _, root := range roots {
		info, ok := driveInfo(root)
		if !ok || info.FileSystem != "NTFS" {
			code = 2
			continue
		}
		var meta Meta
		if j, err := openJournal(info.Root); err == nil {
			meta.JournalID, meta.NextUSN = j.ID, j.Next
			j.Close()
		}
		start := time.Now()
		sc := NewScratch(dir)
		scans, rootID, err := ScanVolume(context.Background(), info.Root, nil, sc)
		if err != nil {
			sc.Close()
			code = 1
			continue
		}
		meta.Serial, meta.FileSystem, meta.Table = info.Serial, info.FileSystem, true
		meta.Built, meta.Took = start.Unix(), time.Since(start).Milliseconds()
		ix, err := store.Build(keyOf(info.Root), info.Root, rootID, scans, true, meta, sc)
		sc.Close()
		if err != nil {
			code = 1
			continue
		}
		ix.Close()
	}
	return code
}

// Elevate asks the system to run the program as administrator to read
// the file tables of the NTFS drives, which shows its prompt, and has the
// manager take the indexes it writes. It returns once the reading is done
// or was declined.
func (m *Manager) Elevate(exe string) error {
	var roots []string
	for _, s := range m.Drives() {
		if s.FileSystem == "NTFS" && len(s.Root) == 3 && s.State != StateOff {
			roots = append(roots, s.Root)
		}
	}
	if len(roots) == 0 {
		return errors.New("index: no NTFS drive to read")
	}
	// The drives stop following meanwhile, and write what they have:
	// what the helper writes is then the newest. They still answer.
	var drives []*drive
	for _, root := range roots {
		m.mu.Lock()
		d := m.drives[rootKey(root)]
		m.mu.Unlock()
		if d != nil {
			d.stop()
			drives = append(drives, d)
		}
	}
	read := false
	defer func() {
		for _, d := range drives {
			if read {
				// The index written for it, which catches up with the
				// journal from where the helper read.
				d.mu.Lock()
				if d.view != nil {
					d.view.ix.Close()
					d.view = nil
				}
				d.mu.Unlock()
			}
			m.mu.Lock()
			if !m.closed {
				m.startDrive(d)
			}
			m.mu.Unlock()
		}
	}()
	args := `--index-helper "` + m.cfg.Dir + `" ` + strings.Join(roots, " ")
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(args)
	info := shellExecuteInfo{Mask: seeMaskNoCloseProcess | seeMaskNoAsync, Verb: verb, File: file, Params: params, Show: 0}
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, err := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if err == syscall.Errno(1223) {
			return ErrDeclined
		}
		return err
	}
	if info.Process == 0 {
		return errors.New("index: the helper did not start")
	}
	defer windows.CloseHandle(info.Process)
	windows.WaitForSingleObject(info.Process, windows.INFINITE)
	var code uint32
	windows.GetExitCodeProcess(info.Process, &code)
	read = code != 1
	if code != 0 {
		return errors.New("index: some drives could not be read")
	}
	return nil
}

// ErrDeclined is returned when the user said no to the system's prompt.
var ErrDeclined = errors.New("index: declined")

const (
	seeMaskNoCloseProcess = 0x40
	seeMaskNoAsync        = 0x100
)

type shellExecuteInfo struct {
	Size     uint32
	Mask     uint32
	Hwnd     uintptr
	Verb     *uint16
	File     *uint16
	Params   *uint16
	Dir      *uint16
	Show     int32
	InstApp  uintptr
	IDList   uintptr
	Class    *uint16
	KeyClass uintptr
	HotKey   uint32
	Icon     uintptr
	Process  windows.Handle
}

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// DefaultDir is where the indexes are kept: beside what else the program
// caches.
func DefaultDir() string {
	if dir := os.Getenv("MYFILES_CACHE_DIR"); dir != "" {
		return filepath.Join(dir, "index")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "MyFiles", "index")
}
