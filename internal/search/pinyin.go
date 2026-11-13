package search

import (
	_ "embed"
	"encoding/binary"
	"sync"
)

// pinyinData is the table of how Chinese characters are read, from
// Lertaro's PinyinAlias plugin (MIT), which builds it of mozillazg's
// pinyin-data and Unihan: readings without tones, ü written v, the common
// ones first, at most three a character.
//
//	u16 syllables | i32 multi | i32 chars | 6 bytes of zero
//	syllables: [u8 length][letters], in order
//	multi:     [u16 n][n × u16 syllable]
//	chars:     a u16 for each of U+3007 and the 28646 after it: 0xFFFF
//	           for none, a syllable under 0x8000, else 0x8000 + the offset
//	           of its readings in multi, in u16s
//
//go:embed pinyin.bin
var pinyinData []byte

const (
	pinyinFirst = 0x3007
	pinyinCount = 28647
)

var pinyin struct {
	once      sync.Once
	syllables []string
	multi     int
	chars     int
	// letters are the letters each character's readings are written
	// with, a bit a letter.
	letters []uint32
}

func loadPinyin() {
	d := pinyinData
	n := int(binary.LittleEndian.Uint16(d))
	pinyin.multi = int(binary.LittleEndian.Uint32(d[2:]))
	pinyin.chars = int(binary.LittleEndian.Uint32(d[6:]))
	pinyin.syllables = make([]string, n)
	off := 16
	for i := range n {
		l := int(d[off])
		pinyin.syllables[i] = string(d[off+1 : off+1+l])
		off += 1 + l
	}
	pinyin.letters = make([]uint32, pinyinCount)
	var buf [4]string
	for i := range pinyinCount {
		var m uint32
		for _, reading := range readingsAt(i, buf[:0]) {
			for j := 0; j < len(reading); j++ {
				m |= 1 << (reading[j] - 'a')
				if reading[j] == 'v' {
					m |= 1 << ('u' - 'a')
				}
			}
		}
		pinyin.letters[i] = m
	}
}

func readingsAt(index int, buf []string) []string {
	d := pinyinData
	v := int(binary.LittleEndian.Uint16(d[pinyin.chars+index*2:]))
	switch {
	case v == 0xFFFF:
		return buf
	case v < 0x8000:
		return append(buf, pinyin.syllables[v])
	}
	at := pinyin.multi + (v-0x8000)*2
	n := int(binary.LittleEndian.Uint16(d[at:]))
	for i := range n {
		buf = append(buf, pinyin.syllables[binary.LittleEndian.Uint16(d[at+2+i*2:])])
	}
	return buf
}

// Readings appends to buf the ways a Chinese character is read, the
// common ones first; none for a character that is not one.
func Readings(c rune, buf []string) []string {
	if c < pinyinFirst || c >= pinyinFirst+pinyinCount {
		return buf
	}
	pinyin.once.Do(loadPinyin)
	return readingsAt(int(c-pinyinFirst), buf)
}

// readingLetters are the letters a character's readings are written
// with, as bits of a mask.
func readingLetters(c rune) uint32 {
	if c < pinyinFirst || c >= pinyinFirst+pinyinCount {
		return 0
	}
	pinyin.once.Do(loadPinyin)
	return pinyin.letters[c-pinyinFirst]
}

// reader matches a word typed in Latin letters against the readings of a
// name's characters. It keeps the set of how many letters of the word may
// have been read so far, a bit a count, and reads a character at a time:
// a Chinese one takes the start of one of its readings, at least a letter
// of it, and another character takes itself.
type reader struct {
	length int
	first  rune
	// common keeps to each character's two commonest readings.
	common bool
	// at tells, for each ASCII character, the places of the word it is at.
	at [128]uint64
	// wide are the characters of the word that are not ASCII, each with
	// its places: a name's must be the same.
	wide map[rune]uint64
}

const (
	readerMax = 63
	// readerText is how many of a name's characters are looked at.
	readerText = 512
)

// isWord reports a term pinyin can read: 2 to 63 lower case letters and
// digits, a letter among them.
func isWord(term []rune) bool {
	if len(term) < 2 || len(term) > readerMax {
		return false
	}
	letter := false
	for _, c := range term {
		switch {
		case c >= 'a' && c <= 'z':
			letter = true
		case c < '0' || c > '9':
			return false
		}
	}
	return letter
}

// isMixed reports a term that mixes Chinese characters with letters that
// may be the readings of others, as 电nao: read as a word, with its own
// characters taken as they are.
func isMixed(term []rune) bool {
	if len(term) < 2 || len(term) > readerMax {
		return false
	}
	letter, wide := false, false
	for _, c := range term {
		switch {
		case c >= 'a' && c <= 'z':
			letter = true
		case c >= '0' && c <= '9':
		case c >= 0x80 && readingLetters(c) != 0:
			wide = true
		default:
			return false
		}
	}
	return letter && wide
}

func newReader(term []rune, common bool) *reader {
	r := &reader{length: len(term), first: term[0], common: common}
	for p, c := range term {
		if c < 0x80 {
			r.at[c] |= 1 << p
			continue
		}
		if r.wide == nil {
			r.wide = map[rune]uint64{}
		}
		r.wide[c] |= 1 << p
	}
	return r
}

// find is where the word reads in a name: the first place it does, and
// where the reading ends.
func (r *reader) find(chars []rune) (start, end int, ok bool) {
	done := uint64(1) << r.length
	limit := min(len(chars), readerText)
	var buf [4]string
	for begin := range limit {
		if !r.canStart(chars[begin], buf[:0]) {
			continue
		}
		states := uint64(1)
		for j := begin; j < len(chars); j++ {
			states = r.step(chars[j], states, buf[:0])
			if states&done != 0 {
				return begin, j + 1, true
			}
			if states == 0 {
				break
			}
		}
	}
	return 0, 0, false
}

func (r *reader) step(c rune, states uint64, buf []string) uint64 {
	if c < 0x80 {
		return (states & r.at[c]) << 1
	}
	var next uint64
	if r.wide != nil {
		next = (states & r.wide[c]) << 1
	}
	readings := Readings(c, buf)
	if r.common && len(readings) > 2 {
		readings = readings[:2]
	}
	for _, reading := range readings {
		t := states
		for i := 0; i < len(reading); i++ {
			l := reading[i]
			at := r.at[l]
			if l == 'v' {
				at |= r.at['u']
			}
			t = (t & at) << 1
			if t == 0 {
				break
			}
			next |= t
		}
	}
	return next
}

func (r *reader) canStart(c rune, buf []string) bool {
	if c < 0x80 || r.first >= 0x80 {
		return c == r.first
	}
	for _, reading := range Readings(c, buf) {
		if rune(reading[0]) == r.first || reading[0] == 'v' && r.first == 'u' {
			return true
		}
	}
	return false
}
