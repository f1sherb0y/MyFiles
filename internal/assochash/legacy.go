package assochash

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Legacy is the Windows 10/11 UserChoice checksum. Unlike UserChoiceLatest,
// its key timestamp is rounded down to a whole minute.
func Legacy(ext, progID, sid string, filetime uint64) string {
	stamp := fmt.Sprintf("%016x", filetime/600000000*600000000)
	input := strings.ToLower(ext + sid + progID + stamp + "User Choice set via Windows User Experience {D18B6DD5-6124-4341-9318-804003BAFA0B}")
	var data []byte
	for _, c := range utf16.Encode([]rune(input)) {
		data = binary.LittleEndian.AppendUint16(data, c)
	}
	return mix(append(data, 0, 0))
}
