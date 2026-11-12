package shell

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"myfiles/internal/fsys"
)

var (
	procSHEmptyRecycleBinW           = shell32.NewProc("SHEmptyRecycleBinW")
	procSHUpdateRecycleBinIcon       = shell32.NewProc("SHUpdateRecycleBinIcon")
	errTrashRecord                   = errors.New("not a record of the Recycle Bin")
	errTrashGone                     = errors.New("it is no longer in the Recycle Bin")
	errTrashOutside                  = errors.New("it is not in the Recycle Bin")
	trashFolderName                  = `$Recycle.Bin`
	trashRecordMark, trashMark       = "$I", "$R"
	trashRecordMaxSize         int64 = 64 << 10
)

// The Recycle Bin is, on each drive, a folder of the user's in
// $Recycle.Bin, named after their security identifier. What is deleted
// there is renamed $R and a few letters, with its extension, beside a
// record named $I and the same letters: the size, when it was deleted,
// and where it was. The system's file manager shows the records; so does
// this, read as files, with no need of the shell's namespace, which lists
// the same a few items a millisecond.

// trashFolders are the user's folders of the Recycle Bin, one a drive
// that has one.
func trashFolders() []string {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil
	}
	sid := user.User.Sid.String()
	var out []string
	for _, v := range fsys.Volumes() {
		if !v.Ready {
			continue
		}
		dir := v.Root + trashFolderName + `\` + sid
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out = append(out, dir)
		}
	}
	return out
}

func trash() ([]TrashItem, error) { return trashIn(trashFolders()) }

// trashIn lists what the folders of a Recycle Bin hold.
func trashIn(folders []string) ([]TrashItem, error) {
	var out []TrashItem
	var failed error
	for _, dir := range folders {
		entries, err := os.ReadDir(dir)
		if err != nil {
			failed = err
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, trashRecordMark) || e.IsDir() {
				continue
			}
			item, err := readTrashRecord(filepath.Join(dir, name))
			if err != nil {
				continue // a record half written, or of no use
			}
			// The record without what it is of is a leftover.
			info, err := os.Lstat(item.Path)
			if err != nil {
				continue
			}
			item.Dir = info.IsDir()
			out = append(out, item)
		}
	}
	if len(out) == 0 && failed != nil {
		return nil, failed
	}
	return out, nil
}

// readTrashRecord reads a record of the Recycle Bin: 8 bytes of version,
// the size, the time of deletion as the file system counts time, then
// the path it was at, as 260 characters in version 1 and with its length
// before it in version 2.
func readTrashRecord(record string) (TrashItem, error) {
	info, err := os.Stat(record)
	if err != nil {
		return TrashItem{}, err
	}
	if info.Size() > trashRecordMaxSize {
		return TrashItem{}, errTrashRecord
	}
	data, err := os.ReadFile(record)
	if err != nil {
		return TrashItem{}, err
	}
	if len(data) < 28 {
		return TrashItem{}, errTrashRecord
	}
	le := binary.LittleEndian
	version := le.Uint64(data)
	item := TrashItem{Size: int64(le.Uint64(data[8:]))}
	// From 1601 in hundreds of nanoseconds, to 1970 in nanoseconds.
	item.Deleted = (int64(le.Uint64(data[16:])) - 116444736000000000) * 100
	var path []byte
	switch version {
	case 1:
		path = data[24:]
	case 2:
		n := int(le.Uint32(data[24:])) * 2
		if n <= 0 || 28+n > len(data) {
			return TrashItem{}, errTrashRecord
		}
		path = data[28 : 28+n]
	default:
		return TrashItem{}, errTrashRecord
	}
	units := make([]uint16, 0, len(path)/2)
	for i := 0; i+1 < len(path); i += 2 {
		u := le.Uint16(path[i:])
		if u == 0 {
			break
		}
		units = append(units, u)
	}
	original := string(utf16.Decode(units))
	if original == "" {
		return TrashItem{}, errTrashRecord
	}
	item.Name, item.From = filepath.Base(original), filepath.Dir(original)
	dir, name := filepath.Split(record)
	item.Path = dir + trashMark + strings.TrimPrefix(name, trashRecordMark)
	return item, nil
}

// trashRecord is the record of something in the Recycle Bin, and an error
// for a path that is not of something there.
func trashRecord(path string) (string, error) {
	dir, name := filepath.Split(path)
	if !strings.HasPrefix(name, trashMark) || !strings.Contains(strings.ToLower(dir), strings.ToLower(`\`+trashFolderName+`\`)) {
		return "", &fsys.PathError{Op: "restore", Path: path, Err: errTrashOutside}
	}
	return dir + trashRecordMark + strings.TrimPrefix(name, trashMark), nil
}

func restoreTrash(paths []string) ([]string, error) {
	var restored []string
	var failed error
	for _, path := range paths {
		to, err := restoreOne(path)
		if err != nil {
			failed = err
			continue
		}
		restored = append(restored, to)
	}
	procSHUpdateRecycleBinIcon.Call()
	return restored, failed
}

// restoreOne puts something of the Recycle Bin back where it was, and
// returns where that is: under another name, "name (2)", when something
// took its place meanwhile, which is never replaced.
func restoreOne(path string) (string, error) {
	record, err := trashRecord(path)
	if err != nil {
		return "", err
	}
	item, err := readTrashRecord(record)
	if err != nil {
		return "", &fsys.PathError{Op: "restore", Path: path, Err: errTrashGone}
	}
	if err := os.MkdirAll(item.From, 0o755); err != nil {
		return "", &fsys.PathError{Op: "restore", Path: item.From, Err: err}
	}
	from, err := windows.UTF16PtrFromString(fsys.LongPath(path))
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(item.Name)
	stem := strings.TrimSuffix(item.Name, ext)
	for n := 1; n < 1000; n++ {
		name := item.Name
		if n > 1 {
			name = stem + " (" + strconv.Itoa(n) + ")" + ext
		}
		target := filepath.Join(item.From, name)
		to, err := windows.UTF16PtrFromString(fsys.LongPath(target))
		if err != nil {
			return "", err
		}
		// A move within the drive, which fails rather than replace.
		err = windows.MoveFileEx(from, to, 0)
		if err == nil {
			_ = os.Remove(record)
			return target, nil
		}
		if err != windows.ERROR_ALREADY_EXISTS && err != windows.ERROR_FILE_EXISTS {
			return "", &fsys.PathError{Op: "restore", Path: target, Err: err}
		}
	}
	return "", &fsys.PathError{Op: "restore", Path: item.From, Err: windows.ERROR_ALREADY_EXISTS}
}

func deleteTrash(paths []string) error {
	var failed error
	for _, path := range paths {
		record, err := trashRecord(path)
		if err != nil {
			failed = err
			continue
		}
		if err := os.RemoveAll(fsys.LongPath(path)); err != nil {
			failed = &fsys.PathError{Op: "delete", Path: path, Err: err}
			continue
		}
		_ = os.Remove(record)
	}
	procSHUpdateRecycleBinIcon.Call()
	return failed
}

func emptyTrash(owner Window) error {
	const (
		noConfirmation = 0x1
		noProgress     = 0x2
		noSound        = 0x4
	)
	hr, _, _ := procSHEmptyRecycleBinW.Call(uintptr(owner), 0, noConfirmation|noProgress|noSound)
	// Empty already is no failure.
	if h := hresult(hr); h.failed() && h != 0x8000FFFF {
		return h.err()
	}
	return nil
}
