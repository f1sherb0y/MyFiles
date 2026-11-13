package search

import (
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// foldWidth is a character in its narrow form: the wide forms of ASCII,
// and the wide space, as their ASCII ones.
func foldWidth(c rune) rune {
	switch {
	case c < hanLow:
		if c == 0x3000 {
			return ' '
		}
	case c <= hanHigh:
		// A Traditional character as its Simplified one: a name in the
		// one script is found by a query in the other.
		if s := simplified()[c-hanLow]; s != 0 {
			return rune(s)
		}
	case c >= 0xFF01 && c <= 0xFF5E:
		return c - 0xFEE0
	}
	return c
}

// The range of the Chinese characters that have Simplified forms, and
// the table of them, made when the first is met.
const (
	hanLow  = 0x3400
	hanHigh = 0x9FFF
)

var simplified = sync.OnceValue(func() []uint16 {
	if t := simplifiedTable(); len(t) == hanHigh-hanLow+1 {
		return t
	}
	return make([]uint16, hanHigh-hanLow+1)
})

// isCombining reports a character that joins the one before it, as an
// accent written apart.
func isCombining(c rune) bool {
	if c < 0x300 || isHan(c) {
		return false
	}
	if c <= 0x36F || c >= 0x1AB0 && c <= 0x1AFF || c >= 0x1DC0 && c <= 0x1DFF ||
		c >= 0x20D0 && c <= 0x20FF || c >= 0xFE20 && c <= 0xFE2F {
		return true
	}
	return unicode.In(c, unicode.Mn, unicode.Mc, unicode.Me)
}

// isHan reports a Chinese character, of those that are most of what is
// not ASCII in names: known without the tables of Unicode.
func isHan(c rune) bool { return c >= 0x3400 && c <= 0x9FFF }

// composes reports a character that only reads right once composed with
// its neighbours: a combining mark, or a Hangul jamo.
func composes(c rune) bool {
	return c >= 0x300 && (isCombining(c) || c >= 0x1100 && c <= 0x11FF)
}

// Normalize is text with wide forms folded and separate accents composed,
// as names are matched: what is typed is matched the same way.
func Normalize(s string) string {
	plain := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	runes, _ := normalize(s, false)
	return string(runes)
}

// normalize is the characters of s as they are matched, and, when asked,
// for each the index of the character of s it came from.
func normalize(s string, mapped bool) (out []rune, from []int) {
	out = make([]rune, 0, len(s))
	compose := false
	for _, r := range s {
		r = foldWidth(r)
		if composes(r) {
			compose = true
		}
		out = append(out, r)
	}
	if !compose {
		if mapped {
			from = make([]int, len(out))
			for i := range from {
				from[i] = i
			}
		}
		return out, from
	}
	// Each character with the marks after it, composed: what it becomes
	// comes from where it started.
	src := out
	out = make([]rune, 0, len(src))
	for i := 0; i < len(src); {
		j := i + 1
		for j < len(src) && composes(src[j]) {
			j++
		}
		if j == i+1 {
			out = append(out, src[i])
			if mapped {
				from = append(from, i)
			}
		} else {
			for _, r := range norm.NFC.String(string(src[i:j])) {
				out = append(out, r)
				if mapped {
					from = append(from, i)
				}
			}
		}
		i = j
	}
	return out, from
}

// Set makes t the name given, ready to match, and reports whether the
// name is ASCII: its characters are then its bytes, in the same places.
func (t *Text) Set(name []byte) (ascii bool) {
	t.Chars, t.Cls = t.Chars[:0], t.Cls[:0]
	for _, b := range name {
		if b >= 0x80 {
			t.setWide(name)
			return false
		}
		t.Chars = append(t.Chars, rune(asciiLower[b]))
		t.Cls = append(t.Cls, asciiClass[b])
	}
	return true
}

// SetString is Set for a string.
func (t *Text) SetString(name string) bool {
	return t.Set(unsafeBytes(name))
}

func (t *Text) setWide(name []byte) {
	t.Chars, t.Cls = t.Chars[:0], t.Cls[:0]
	compose := false
	for len(name) > 0 {
		r, size := rune(name[0]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRune(name)
		}
		name = name[size:]
		r = foldWidth(r)
		if composes(r) {
			compose = true
		}
		t.Chars = append(t.Chars, r)
	}
	if compose {
		t.Chars, _ = normalize(string(t.Chars), false)
	}
	for i, r := range t.Chars {
		t.Cls = append(t.Cls, classOf(r))
		t.Chars[i] = lowerOf(r)
	}
}

// The bits of a name's mask: a letter and a digit have one each, and the
// other characters share the rest.
func maskBit(lower rune) uint {
	switch {
	case lower >= 'a' && lower <= 'z':
		return uint(lower - 'a')
	case lower >= '0' && lower <= '9':
		return 26 + uint(lower-'0')
	}
	return 36 + uint(lower%28)
}

func maskOf(text []rune) uint64 {
	var m uint64
	for _, c := range text {
		m |= 1 << maskBit(c)
	}
	return m
}

// Mask tells which characters a name can answer to: a bit for each it
// holds, for the letters a Chinese character is read with, and for the
// letter an accented one is written over. A name whose mask lacks a bit
// of a term's cannot match it, which is told without reading the name.
func Mask(name []byte) uint64 {
	var m uint64
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b >= 0x80 {
			return maskWide(name)
		}
		m |= 1 << maskBit(rune(asciiLower[b]))
	}
	return m
}

func maskWide(name []byte) uint64 {
	var t Text
	t.setWide(name)
	m := maskOf(t.Chars)
	for _, c := range t.Chars {
		if c < 0x80 {
			continue
		}
		if base := baseLetter(c); base != 0 {
			m |= 1 << maskBit(base)
		}
		m |= uint64(readingLetters(c))
	}
	return m
}

// accents are the letters the accented ones of U+00C0 to U+024F are
// written over, 0 for those written over none.
var accents [0x250 - 0xC0]byte

func init() {
	for c := rune(0xC0); c < 0x250; c++ {
		d := norm.NFD.String(string(c))
		if len(d) > 1 && d[0] < 0x80 && asciiClass[d[0]] >= clsLower && asciiClass[d[0]] <= clsUpper {
			accents[c-0xC0] = asciiLower[d[0]]
		}
	}
}

// baseLetter is the letter an accented one is written over, in lower
// case, 0 for a character that is none.
func baseLetter(c rune) rune {
	if c < 0xC0 || c >= 0x250 {
		return 0
	}
	return rune(accents[c-0xC0])
}
