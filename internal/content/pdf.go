package content

import (
	_ "embed"
	"io"
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"
)

// LicensePDF is the license of the reader of PDFs
// (github.com/ledongthuc/pdf, after rsc.io/pdf).
//
//go:embed LICENSE-pdf
var LicensePDF string

// pdfText is the text of a PDF: what its pages draw as text, in the order
// they draw it. A PDF that is pictures of pages has none.
func pdfText(path string) (text string, ok bool) {
	// The reader gives up on what it does not understand by panicking.
	defer func() {
		if recover() != nil {
			text, ok = "", false
		}
	}()
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	plain, err := r.GetPlainText()
	if err != nil {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(plain, maxText))
	if err != nil && len(data) == 0 {
		return "", false
	}
	return joinLines(string(data)), len(data) > 0
}

// joinLines undoes the breaks a PDF has where a line of the page ends:
// between two Chinese characters nothing was there, and elsewhere a
// space, so that a word broken over lines is found.
func joinLines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var last rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r != '\n' && r != '\r' {
			b.WriteRune(r)
			last = r
			continue
		}
		j := i
		for j < len(runes) && (runes[j] == '\n' || runes[j] == '\r') {
			j++
		}
		if j-i > 1 || j == len(runes) {
			// An empty line between: a paragraph's end.
			b.WriteByte('\n')
		} else if !(wide(last) && wide(runes[j])) {
			b.WriteByte(' ')
		}
		last = '\n'
		i = j - 1
	}
	return b.String()
}

// wide reports a character of the scripts written without spaces.
func wide(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		r >= 0x3000 && r <= 0x303F || r >= 0xFF00 && r <= 0xFFEF
}
