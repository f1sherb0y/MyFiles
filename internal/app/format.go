package app

import (
	"strconv"
	"time"
)

// formatSize writes a number of bytes as file managers do, in binary
// units with three significant digits at most: "0 B", "812 B", "1.4 KB",
// "23.5 MB", "112 GB".
func formatSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := [...]string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(n) / 1024
	u := 0
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	var buf [16]byte
	b := buf[:0]
	switch {
	case v >= 100:
		b = strconv.AppendFloat(b, v, 'f', 0, 64)
	case v >= 10:
		b = strconv.AppendFloat(b, v, 'f', 1, 64)
	default:
		b = strconv.AppendFloat(b, v, 'f', 2, 64)
	}
	b = append(b, ' ')
	b = append(b, units[u]...)
	return string(b)
}

// formatTime writes a time as "2026-11-10 14:32", in the local zone: a
// form that sorts as it reads, and takes the same room on every row.
func formatTime(ns int64) string {
	if ns == 0 {
		return ""
	}
	t := time.Unix(0, ns)
	y, m, d := t.Date()
	h, min, _ := t.Clock()
	var buf [16]byte
	b := buf[:0]
	b = appendPadded(b, y, 4)
	b = append(b, '-')
	b = appendPadded(b, int(m), 2)
	b = append(b, '-')
	b = appendPadded(b, d, 2)
	b = append(b, ' ')
	b = appendPadded(b, h, 2)
	b = append(b, ':')
	b = appendPadded(b, min, 2)
	return string(b)
}

func appendPadded(b []byte, v, width int) []byte {
	var digits [8]byte
	i := len(digits)
	for v > 0 || i == len(digits) {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	for len(digits)-i < width {
		i--
		digits[i] = '0'
	}
	return append(b, digits[i:]...)
}

// plural is n with a noun, as "1 item" or "12 items".
func plural(n int, noun string) string {
	s := groupDigits(int64(n)) + " " + noun
	if n != 1 {
		s += "s"
	}
	return s
}

// groupDigits writes n with its thousands apart: "26,254".
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	b := make([]byte, 0, len(s)+len(s)/3)
	head := len(s) % 3
	if head > 0 {
		b = append(b, s[:head]...)
	}
	for i := head; i < len(s); i += 3 {
		if len(b) > 0 {
			b = append(b, ',')
		}
		b = append(b, s[i:i+3]...)
	}
	return string(b)
}

// containsFold reports whether s contains sub without regard to case.
// sub is in lower case already. ASCII is compared in place; names with
// other letters fall back to the slow way.
func containsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	first := sub[0]
	for i := 0; i+len(sub) <= len(s); i++ {
		c := s[i]
		if c >= 0x80 {
			return containsFoldSlow(s, sub)
		}
		if lower(c) != first {
			continue
		}
		j := 1
		for ; j < len(sub); j++ {
			d := s[i+j]
			if d >= 0x80 {
				return containsFoldSlow(s, sub)
			}
			if lower(d) != sub[j] {
				break
			}
		}
		if j == len(sub) {
			return true
		}
	}
	return false
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
