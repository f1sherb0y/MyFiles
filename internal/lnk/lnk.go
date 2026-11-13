// Package lnk reads what a Windows shortcut points at from the shortcut's
// file, without asking the shell: a shortcut is told to be one to a
// program, to a document or to a folder by reading a few hundred bytes.
//
// The format is Microsoft's [MS-SHLLINK]. Of it this reads the header, the
// place the link keeps its target's path in (LinkInfo), and the block that
// holds a path with variables of the environment in it.
package lnk

import (
	"encoding/binary"
	"os"
	"strings"
	"unicode/utf16"
)

// Target is what a shortcut points at.
type Target struct {
	// Path is the target's path, "" when the shortcut names it in a way
	// that is no path, as those of programs from the Store do.
	Path string
	// Dir is set for a folder.
	Dir bool
}

// The bits of a link's flags.
const (
	hasIDList    = 1 << 0
	hasLinkInfo  = 1 << 1
	hasName      = 1 << 2
	hasRelPath   = 1 << 3
	hasWorkDir   = 1 << 4
	hasArguments = 1 << 5
	hasIcon      = 1 << 6
	isUnicode    = 1 << 7
	hasExpString = 1 << 9

	attrDirectory = 0x10
	envBlock      = 0xA0000001
)

var le = binary.LittleEndian

// maxSize is the most of a shortcut's file that is read: one larger is
// none of those the shell writes.
const maxSize = 1 << 20

// Read is what the shortcut in a file points at. It reports false for a
// file that is no shortcut.
func Read(path string) (Target, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Target{}, false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() > maxSize {
		return Target{}, false
	}
	buf := make([]byte, 16<<10)
	n, _ := f.Read(buf)
	data := buf[:n]
	if n == len(buf) {
		// Longer than most: the rest.
		rest, err := os.ReadFile(path)
		if err != nil {
			return Target{}, false
		}
		data = rest
	}
	return Parse(data)
}

// Parse is what a shortcut's bytes say it points at.
func Parse(data []byte) (Target, bool) {
	// The header: its size, and the class of shell links.
	if len(data) < 0x4C || le.Uint32(data) != 0x4C ||
		le.Uint32(data[4:]) != 0x00021401 || le.Uint64(data[12:]) != 0x46000000000000C0 {
		return Target{}, false
	}
	flags := le.Uint32(data[0x14:])
	t := Target{Dir: le.Uint32(data[0x18:])&attrDirectory != 0}
	at := 0x4C
	if flags&hasIDList != 0 {
		if at+2 > len(data) {
			return t, true
		}
		at += 2 + int(le.Uint16(data[at:]))
	}
	if flags&hasLinkInfo != 0 && at+4 <= len(data) {
		size := int(le.Uint32(data[at:]))
		if size >= 0x1C && at+size <= len(data) {
			t.Path = linkInfo(data[at : at+size])
		}
		at += size
	}
	// A path with variables in it is kept apart, after the strings: as it
	// was written, which is what the shell opens.
	if flags&hasExpString != 0 {
		for _, bit := range []uint32{hasName, hasRelPath, hasWorkDir, hasArguments, hasIcon} {
			if flags&bit == 0 {
				continue
			}
			if at+2 > len(data) {
				return t, true
			}
			n := int(le.Uint16(data[at:]))
			if flags&isUnicode != 0 {
				n *= 2
			}
			at += 2 + n
		}
		for at+8 <= len(data) {
			size := int(le.Uint32(data[at:]))
			if size < 8 || at+size > len(data) {
				break
			}
			if le.Uint32(data[at+4:]) == envBlock && size >= 8+260+520 {
				if p := wide(data[at+8+260 : at+8+260+520]); p != "" {
					t.Path = expand(p)
				}
				break
			}
			at += size
		}
	}
	return t, true
}

// linkInfo is the path a LinkInfo holds: of a file on a drive, or on a
// share.
func linkInfo(b []byte) string {
	header := int(le.Uint32(b[4:]))
	flags := le.Uint32(b[8:])
	str := func(off int) string {
		if off <= 0 || off >= len(b) {
			return ""
		}
		end := off
		for end < len(b) && b[end] != 0 {
			end++
		}
		return ansi(b[off:end])
	}
	wstr := func(off int) string {
		if off <= 0 || off+2 > len(b) {
			return ""
		}
		return wide(b[off:])
	}
	base, suffix := "", str(int(le.Uint32(b[0x18:])))
	if header >= 0x24 && len(b) >= 0x24 {
		// The same in UTF-16, which says what the other cannot.
		if s := wstr(int(le.Uint32(b[0x20:]))); s != "" {
			suffix = s
		}
	}
	switch {
	case flags&1 != 0:
		base = str(int(le.Uint32(b[0x10:])))
		if header >= 0x24 && len(b) >= 0x24 {
			if s := wstr(int(le.Uint32(b[0x1C:]))); s != "" {
				base = s
			}
		}
	case flags&2 != 0:
		// On a share: its name is in a part of its own.
		if off := int(le.Uint32(b[0x14:])); off > 0 && off+0x14 <= len(b) {
			net := b[off:]
			if name := int(le.Uint32(net[8:])); name > 0 && name < len(net) {
				end := name
				for end < len(net) && net[end] != 0 {
					end++
				}
				base = ansi(net[name:end])
			}
		}
	}
	if base == "" {
		return ""
	}
	if suffix != "" && !strings.HasSuffix(base, `\`) {
		base += `\`
	}
	return base + suffix
}

// wide is a string of UTF-16 that ends at a zero, or with the bytes.
func wide(b []byte) string {
	u := make([]uint16, 0, 64)
	for i := 0; i+2 <= len(b); i += 2 {
		c := le.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// expand puts the values of the environment's variables in a path, as
// %windir% has one.
func expand(p string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(p, '%')
		if i < 0 {
			break
		}
		j := strings.IndexByte(p[i+1:], '%')
		if j < 0 {
			break
		}
		b.WriteString(p[:i])
		name := p[i+1 : i+1+j]
		if v, ok := os.LookupEnv(name); ok {
			b.WriteString(v)
		} else {
			b.WriteString(p[i : i+j+2])
		}
		p = p[i+j+2:]
	}
	b.WriteString(p)
	return b.String()
}
