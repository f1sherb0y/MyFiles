package fsys

import (
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procLCMapStringEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("LCMapStringEx")

const (
	lcmapSortKey        = 0x00000400
	normIgnoreCase      = 0x00000001
	sortDigitsAsNumbers = 0x00000008
)

// keyer makes sort keys, reusing its buffers from name to name.
type keyer struct {
	wide []uint16
	buf  []byte
}

// key is the system's sort key of a name in the user's language, with
// digits as numbers and without case: what Explorer's order comes to.
// Keys compare as bytes.
func (k *keyer) key(name string) string {
	k.wide = k.wide[:0]
	ascii := true
	for i := 0; i < len(name); i++ {
		if name[i] >= 0x80 {
			ascii = false
			break
		}
		k.wide = append(k.wide, uint16(name[i]))
	}
	if !ascii {
		k.wide = utf16.AppendRune(k.wide[:0], 0)[:0]
		for _, r := range name {
			k.wide = utf16.AppendRune(k.wide, r)
		}
	}
	if len(k.wide) == 0 {
		return ""
	}
	if need := 8*len(k.wide) + 16; cap(k.buf) < need {
		k.buf = make([]byte, need)
	}
	k.buf = k.buf[:cap(k.buf)]
	n, _, _ := procLCMapStringEx.Call(0, lcmapSortKey|normIgnoreCase|sortDigitsAsNumbers,
		uintptr(unsafe.Pointer(&k.wide[0])), uintptr(len(k.wide)),
		uintptr(unsafe.Pointer(&k.buf[0])), uintptr(len(k.buf)), 0, 0, 0)
	if n == 0 {
		// The system gave no key, as for a name too long for the
		// buffer: order it by its bytes.
		return string(naturalKey(k.buf[:0], name))
	}
	return string(k.buf[:n])
}
