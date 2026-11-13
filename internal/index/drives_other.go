//go:build !windows

package index

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// Other systems have no drives to list here yet: folders are indexed as
// they are searched.

func localDrives() []DriveInfo { return nil }

func driveInfo(root string) (DriveInfo, bool) {
	if _, err := os.Stat(root); err != nil {
		return DriveInfo{}, false
	}
	return DriveInfo{Root: root}, true
}

func readDrive(ctx context.Context, info DriveInfo, seen *atomic.Int64, sc *Scratch) ([]*Scan, uint64, bool, error) {
	scans, rootID, err := Walk(ctx, info.Root, WalkOptions{Progress: seen, Scratch: sc})
	return scans, rootID, false, err
}

func openFollower(string) (follower, error) { return nil, errors.ErrUnsupported }

func openWatcher(string) (pathWatcher, error) { return nil, errors.ErrUnsupported }

type ejectGuard struct{}

func watchEject(string, func()) *ejectGuard { return nil }

func (*ejectGuard) Close() {}

func drivesPresent() uint32 { return 0 }

// statPath is a file as it is now, by its path.
func statPath(path string) (Change, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return Change{}, false
	}
	c := Change{Name: info.Name()}
	c.Modified = uint32(max(info.ModTime().Unix(), 0))
	if info.IsDir() {
		c.Flags |= FlagDir
	} else {
		c.Size = uint64(info.Size())
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		c.Flags |= FlagLink
	}
	if strings.HasPrefix(c.Name, ".") {
		c.Flags |= FlagHidden
	}
	return c, true
}

// CanReadTables reports whether the program may read drives' file tables.
func CanReadTables() bool { return false }

// Helper is the part of the program that reads file tables.
func Helper(string, []string) int { return 2 }

// Elevate asks for an administrator's rights to read file tables.
func (m *Manager) Elevate(string) error { return errors.ErrUnsupported }

// ErrDeclined is returned when the user said no to the system's prompt.
var ErrDeclined = errors.New("index: declined")

// ScanVolume reads a volume's file table.
func ScanVolume(context.Context, string, *atomic.Int64, *Scratch) ([]*Scan, uint64, error) {
	return nil, 0, errors.ErrUnsupported
}

// WalkOptions are how a walk reads a tree.
type WalkOptions struct {
	IDs      bool
	Workers  int
	Progress *atomic.Int64
	Scratch  *Scratch
}

// Walk reads the folders under a root.
func Walk(ctx context.Context, root string, opts WalkOptions) ([]*Scan, uint64, error) {
	const rootID = 1
	s := NewScan(opts.Scratch)
	ids := map[string]uint64{filepath.Clean(root): rootID}
	next := uint64(rootID)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || filepath.Clean(path) == filepath.Clean(root) {
			return nil
		}
		next++
		e := Entry{ID: next, Parent: ids[filepath.Dir(path)]}
		if info, err := d.Info(); err == nil {
			e.Modified = uint32(max(info.ModTime().Unix(), 0))
			if !d.IsDir() {
				e.Size = uint64(info.Size())
			}
		}
		if strings.HasPrefix(d.Name(), ".") {
			e.Flags |= FlagHidden
		}
		if d.IsDir() {
			e.Flags |= FlagDir
			ids[path] = next
		}
		s.AddString(e, d.Name())
		if opts.Progress != nil {
			opts.Progress.Add(1)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return []*Scan{s}, rootID, nil
}

// DefaultDir is where the indexes are kept.
func DefaultDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "MyFiles", "index")
}
