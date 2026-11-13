package search

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// simplifiedTable asks the system for the Simplified form of every
// character of the range, as Lertaro does of each name: character for
// character, without the words that OpenCC would tell apart.
func simplifiedTable() []uint16 {
	const lcmapSimplifiedChinese = 0x02000000
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("LCMapStringEx")
	if proc.Find() != nil {
		return nil
	}
	src := make([]uint16, hanHigh-hanLow+1)
	for i := range src {
		src[i] = uint16(hanLow + i)
	}
	dst := make([]uint16, len(src))
	locale, _ := windows.UTF16PtrFromString("zh-CN")
	n, _, _ := proc.Call(uintptr(unsafe.Pointer(locale)), lcmapSimplifiedChinese,
		uintptr(unsafe.Pointer(&src[0])), uintptr(len(src)),
		uintptr(unsafe.Pointer(&dst[0])), uintptr(len(dst)), 0, 0, 0)
	if int(n) != len(src) {
		return nil
	}
	for i := range dst {
		if dst[i] == src[i] {
			// 0 for a character that is its own Simplified form.
			dst[i] = 0
		}
	}
	return dst
}
