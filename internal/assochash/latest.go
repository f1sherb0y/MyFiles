// Package assochash implements Windows file-association checksums.
// These are compatibility checksums, not security primitives.
//
// The UserChoiceLatest codec is adapted from cssxn/UserChoiceLatestHash
// (MIT, commit 662070e5cdea4496bc474f4bdfd81f77d4cabab3).
// See LICENSE in this directory. Windows may change this undocumented format;
// callers must select the correct layout and verify the resulting association.
package assochash

import (
	"crypto/md5"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// License is the upstream notice, embedded for single-executable distributions.
//
//go:embed LICENSE
var License string

// Latest builds the machine-specific canonical input. The timestamp is the
// ProgId subkey's FILETIME, truncated to milliseconds (not minutes).
func Latest(ext, progID, sid, machine string, filetime uint64) string {
	machine = strings.Trim(machine, "{}")
	if machine == "" {
		return ""
	}
	stamp := fmt.Sprintf("%016x", filetime/10000*10000)
	var input string
	switch machine[len(machine)-1] % 3 {
	case 0:
		input = "Copyright (C) Microsoft. All rights reserved {3822B7CA-C2F4-4889-B8CC-4CE39A8FB81C}" + ext + stamp + machine + progID + sid
	case 1:
		input = "Copyright (C) Microsoft. All rights reserved {D185E0A1-E265-4724-AA21-3A17B038D72E}" + stamp + ext + sid + machine + progID
	case 2:
		input = sid + stamp + "Copyright (C) Microsoft. All rights reserved {97B6BCF4-C367-4577-95BE-73BD3053A5E0}" + ext + machine + progID
	}
	return latestCanonical(input)
}

const repeatSentinel uint64 = 0xfbffffffffffffff

type encoder struct {
	entries []uint64
	last    uint64
	repeat  uint32
	total   uint32
}

func (s *encoder) previous(v uint64) (uint64, bool) {
	start := max(0, len(s.entries)-0x400)
	for i := len(s.entries) - 1; i >= start; i-- {
		if s.entries[i] == v {
			return uint64(len(s.entries) - i), true
		}
	}
	return 0, false
}

func wordClass(ch uint16) int {
	if ch>>8 != 0 {
		return int(byteClassTable[ch>>8]) + 8
	}
	return int(byteClassTable[ch&255])
}

func token(input []uint16, second bool) (value uint64, code, length int, ok bool) {
	if len(input) == 0 {
		return
	}
	code = int(tokenRegion[wordClass(input[0])+256])
	length = int(tokenRegion[code])
	if length > len(input) {
		if !second {
			return
		}
		code = int(fallbackTokenCodeTable[len(input)])
		length = int(tokenRegion[code])
	}
	for {
		fits := true
		for i := 0; i < length && i < len(input); i++ {
			if uint64(input[i]) >= uint64(1)<<tokenBitWidthTable[168*code+i] {
				fits = false
				break
			}
		}
		if fits {
			break
		}
		code++
		length = int(tokenRegion[code])
		if length > len(input) {
			break
		}
	}
	value = uint64(byte(code)) << 56
	shift := 56
	for i := 0; i < length && i < len(input); i++ {
		shift -= int(tokenBitWidthTable[168*code+i])
		value |= uint64(input[i]) << shift
	}
	return value, code, length, true
}

func (s *encoder) emit(v uint64, kind int, input []uint16, cursor *int, first bool) {
	if v == s.last && s.repeat < 255 {
		s.repeat++
		return
	}
	if s.repeat != 0 {
		if distance, found := s.previous(s.last); found {
			var entry uint64
			if distance >= 0x800 {
				s.entries = append(s.entries, v>>17|uint64(s.repeat-1)<<48|0xfe00000000000000)
				entry = s.last
			} else {
				entry = v>>27 | uint64((uint32(distance-1)&0x3ff)|((s.repeat-1)<<11))<<37 | 0xfe00800000000000
			}
			s.entries = append(s.entries, entry)
			s.last, s.repeat = repeatSentinel, 0
			if distance >= 0x800 {
				*cursor -= int(longRewindTable[kind])
			} else {
				*cursor -= int(shortRewindTable[kind])
			}
			return
		}
	} else if first {
		if distance, found := s.previous(v); found && *cursor < len(input) {
			next := int(tokenRegion[wordClass(input[*cursor])+256])
			entry := v
			if distance < 0x800 {
				entry = v>>18 | (distance-1)<<46 | 0xff00000000000000
			}
			s.entries = append(s.entries, entry)
			if distance < 0x800 {
				*cursor -= int(shortRewindTable[next+256])
			}
			s.last = v
			return
		}
	}
	s.entries = append(s.entries, v)
	s.last = v
}

func latestCanonical(input string) string {
	chars := utf16.Encode([]rune(strings.ToLower(input)))
	// The reverse-engineered upstream first pass can fail to make progress
	// on long repetitive input. Bound both input size and work: an unknown
	// registry value must cause compatibility failure, not hang the UI job.
	if len(chars) > 4096 {
		return ""
	}
	budget := 8*len(chars) + 1024
	s := encoder{entries: []uint64{0}, last: repeatSentinel}
	quota := func(n int) uint64 {
		r := (2249744775 * uint64(uint32(n))) >> 32
		return (r + ((uint64(n) - r) >> 1)) >> 7
	}
	consumed, cursor := 0, 0
	for q := quota(len(chars)); q > 1; q = quota(len(chars) - cursor) {
		for i := uint64(0); i < q-1; i++ {
			budget--
			if budget <= 0 || cursor < 0 || cursor >= len(chars) {
				return ""
			}
			v, kind, n, ok := token(chars[cursor:], false)
			if !ok {
				break
			}
			consumed += n
			cursor += n
			s.emit(v, kind, chars, &cursor, true)
		}
	}
	if consumed > len(chars) {
		return ""
	}
	remaining := chars[consumed:]
	for cursor = 0; cursor < len(remaining); {
		budget--
		if budget <= 0 || cursor < 0 {
			return ""
		}
		v, kind, n, ok := token(remaining[cursor:], true)
		if !ok {
			break
		}
		s.total += uint32(n)
		cursor += n
		s.emit(v, kind, remaining, &cursor, false)
	}
	if s.repeat != 0 {
		var entry uint64
		if s.last == repeatSentinel {
			entry = uint64(s.repeat)<<48 | 0xfe007dffffffffff
		} else if distance, found := s.previous(s.last); found {
			entry = uint64((uint32(distance-1)&0x3ff)|((s.repeat-1)<<11))<<37 | 0xfe00801f7fffffff
		} else {
			s.entries = append(s.entries, uint64(s.repeat)<<48|0xfe007dffffffffff)
			entry = s.last
		}
		s.entries = append(s.entries, entry)
	}
	s.entries[0] = uint64(s.total) | uint64(len(s.entries))<<32
	var packed []byte
	for _, v := range s.entries {
		for shift := 0; shift < 64; shift += 16 {
			if part := uint16(v >> shift); part != 0 {
				packed = binary.LittleEndian.AppendUint16(packed, part)
			}
		}
	}
	packed = append(packed, 0, 0)
	return mix(packed)
}

func mix(packed []byte) string {
	digest := md5.Sum(packed)
	md0 := binary.LittleEndian.Uint32(digest[:4]) | 1
	md1 := binary.LittleEndian.Uint32(digest[4:8]) | 1
	var accA, sumA, accB, sumB uint32
	for p := 0; p+8 <= len(packed); p += 8 {
		w0, w1 := binary.LittleEndian.Uint32(packed[p:]), binary.LittleEndian.Uint32(packed[p+4:])
		x := accA + w0
		t := x*(md0+0x69fb0000) - (x>>16)*0x10fa9605
		s := t*0x79f8a395 + (t>>16)*0x689b6b9f
		part := s*0xea970001 - (s>>16)*0x3c101569
		w := w1 + part
		t2 := w*(md1+0x13db0000) - (w>>16)*0x3ce8ec25
		temp := t2*0x59c3af2d - (t2>>16)*0x2232e0f1
		accA = (temp>>16)*0x35bd1ec9 + temp*0x1ec90001
		sumA += part + accA

		t = (w0 + accB) * md0
		a := t*0xb1110000 - (t>>16)*0x30674eef
		b := a * 0x5b9f0000
		c := b - (a>>16)*0x78f7a461
		c = (c>>16)*0x12ceb96d - (a>>16)*0x164d0000 - b*0x46930000
		d := (c>>16)*0x257e1d83 + c*0x1d830000
		f := (w1 + d) * md1
		g := f*0x16f50000 - (f>>16)*0x5d8be90b
		h := (g >> 16) * 0x2c7c6901
		j := (g*0x69010000 + h) * 0x2b890000
		k := (-h - g*0x69010000) >> 16
		m := k*0x7c932b89 - j
		accB = k*0xf2310000 - (m>>16)*0x405b6097 + j*0x60970000
		sumB += d + accB
	}
	out := binary.LittleEndian.AppendUint32(nil, accA^accB)
	out = binary.LittleEndian.AppendUint32(out, sumA^sumB)
	return base64.StdEncoding.EncodeToString(out)
}
