package index

import (
	"context"
	"io"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

// volume reads a volume under its file system. Such reads start and end
// at sectors.
type volume struct {
	h windows.Handle
}

const volumeAlign = 4096

func (v volume) ReadAt(p []byte, off int64) (int, error) {
	if off%volumeAlign == 0 && len(p)%volumeAlign == 0 {
		return v.read(p, off)
	}
	// Whole sectors around what is asked for.
	start := off &^ (volumeAlign - 1)
	end := (off + int64(len(p)) + volumeAlign - 1) &^ (volumeAlign - 1)
	buf := make([]byte, end-start)
	n, err := v.read(buf, start)
	if int64(n) < off-start+int64(len(p)) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, err
	}
	return copy(p, buf[off-start:]), nil
}

func (v volume) read(p []byte, off int64) (int, error) {
	total := 0
	for total < len(p) {
		var o windows.Overlapped
		o.Offset, o.OffsetHigh = uint32(off), uint32(off>>32)
		var n uint32
		if err := windows.ReadFile(v.h, p[total:], &n, &o); err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrUnexpectedEOF
		}
		total += int(n)
		off += int64(n)
	}
	return total, nil
}

// ScanVolume reads the master file table of the drive of a root, as
// "C:\". It fails with an error of access for a program that does not
// run as administrator.
func ScanVolume(ctx context.Context, root string, progress *atomic.Int64, sc *Scratch) ([]*Scan, uint64, error) {
	p, err := windows.UTF16PtrFromString(`\\.\` + root[:2])
	if err != nil {
		return nil, 0, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, 0, err
	}
	defer windows.CloseHandle(h)
	return ScanTable(ctx, volume{h}, progress, sc)
}
