// Package content finds files by what is written in them: it reads the
// text of documents and looks for words there.
//
// It keeps no index. The files to read are those the index of names gives
// for a folder, which is fast to ask; they are read by many goroutines at
// once and told of as they are found. What reading a document took, as a
// PDF's text does, is kept beside the indexes of names, so the next search
// reads it from there.
//
// Lertaro's content search (its ContentSearch plugin) keeps an index of
// trigrams in SQLite for the folders the user names. This one searches any
// folder at once, without that index and its gigabytes, at the price of
// reading: seconds, where an index answers in milliseconds.
package content

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Options are how a search of contents reads files.
type Options struct {
	// MaxSize is the largest file read, in bytes: Lertaro's default is
	// 10 MB.
	MaxSize int64
	// CacheDir is where the text read of documents is kept, "" for
	// nowhere.
	CacheDir string
	// Workers is how many files are read at once, 0 for as many as suits
	// the machine.
	Workers int
}

// DefaultExts are the kinds of files whose contents are searched, as
// Lertaro has them.
const DefaultExts = "txt,md,pdf,docx,docm,pptx,pptm,xlsx,xlsm,csv"

// DefaultMaxSize is the largest file read.
const DefaultMaxSize = 10 << 20

// maxText is the most text kept of one document.
const maxText = 8 << 20

// File is a file to look in.
type File struct {
	Path     string
	Size     int64
	Modified uint32
}

// Hit is a file whose text has all the words asked for.
type Hit struct {
	File File
	// Snippet is the text around the first of the words.
	Snippet string
}

// Terms are the words of a query as they are looked for: without case.
func Terms(query string) []string {
	var out []string
	for _, w := range strings.Fields(query) {
		out = append(out, strings.ToLower(w))
	}
	return out
}

// Search reads files and tells of those whose text holds every term, as
// they are found, from several goroutines one at a time. read counts the
// files read so far. It returns when all are read, or ctx is done.
func Search(ctx context.Context, files []File, terms []string, opts Options, read *atomic.Int64, found func(Hit)) {
	if len(terms) == 0 || len(files) == 0 {
		return
	}
	if opts.MaxSize <= 0 {
		opts.MaxSize = DefaultMaxSize
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = min(max(runtime.GOMAXPROCS(0), 2), 16)
	}
	workers = min(workers, len(files))
	var next atomic.Int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			ex := newExtractor()
			defer ex.close()
			for ctx.Err() == nil {
				i := int(next.Add(1)) - 1
				if i >= len(files) {
					return
				}
				f := files[i]
				if f.Size <= opts.MaxSize {
					if text, ok := textOf(ex, f, opts); ok {
						if snippet, ok := match(text, terms); ok && ctx.Err() == nil {
							mu.Lock()
							found(Hit{f, snippet})
							mu.Unlock()
						}
					}
				}
				if read != nil {
					read.Add(1)
				}
			}
		})
	}
	wg.Wait()
}

// textOf is the text of a file: read as it is, or out of the document it
// is, or from where it was kept when last read.
func textOf(ex *extractor, f File, opts Options) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(f.Path), "."))
	if plain(ext) {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			return "", false
		}
		return decode(data), true
	}
	kept := cachePath(opts.CacheDir, f)
	if kept != "" {
		if data, err := os.ReadFile(kept); err == nil {
			return string(data), true
		}
	}
	var text string
	var ok bool
	switch ext {
	case "docx", "docm", "pptx", "pptm", "xlsx", "xlsm":
		text, ok = office(f.Path, ext)
	case "pdf":
		text, ok = pdfText(f.Path)
	}
	if !ok {
		// Whatever the system has a reader for, as it has for PDFs.
		text, ok = ex.text(f.Path)
	}
	if !ok {
		// Kept as nothing: not asked again until the file changes.
		text = ""
	}
	if len(text) > maxText {
		text = text[:maxText]
	}
	if kept != "" {
		if os.MkdirAll(filepath.Dir(kept), 0o755) == nil {
			tmp := kept + ".tmp"
			if os.WriteFile(tmp, []byte(text), 0o644) == nil {
				os.Rename(tmp, kept)
			}
		}
	}
	return text, text != ""
}

// plain reports the kinds of files that are their text.
func plain(ext string) bool {
	switch ext {
	case "txt", "md", "markdown", "csv", "tsv", "log", "json", "xml", "yaml", "yml", "toml", "ini", "conf", "cfg",
		"html", "htm", "css", "js", "ts", "tsx", "jsx", "go", "rs", "py", "java", "c", "h", "cpp", "hpp", "cs",
		"sh", "ps1", "bat", "cmd", "sql", "tex", "srt", "rst", "org", "":
		return true
	}
	return false
}

// cachePath is where the text of a document is kept: named by the file's
// path, size and time, so that a file changed is read again.
func cachePath(dir string, f File) string {
	if dir == "" {
		return ""
	}
	sum := sha1.Sum([]byte(strings.ToLower(f.Path) + "\x00" + itoa(f.Size) + "\x00" + itoa(int64(f.Modified))))
	name := hex.EncodeToString(sum[:12])
	return filepath.Join(dir, name[:2], name+".txt")
}

func itoa(n int64) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		if n /= 10; n == 0 {
			break
		}
	}
	return string(b[i:])
}

// decode reads a text file's bytes: UTF-8, or UTF-16 with its mark, or
// else whatever bytes there are, taken one a character.
func decode(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return string(data[3:])
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		big := data[0] == 0xFE
		u := make([]uint16, 0, len(data)/2)
		for i := 2; i+1 < len(data); i += 2 {
			if big {
				u = append(u, uint16(data[i])<<8|uint16(data[i+1]))
			} else {
				u = append(u, uint16(data[i+1])<<8|uint16(data[i]))
			}
		}
		return string(utf16.Decode(u))
	}
	if utf8.Valid(data) {
		return string(data)
	}
	return ansi(data)
}

// office is the text of a document of Office's zipped kinds: what is
// written in the parts that hold the words.
func office(path, ext string) (string, bool) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return "", false
	}
	defer z.Close()
	var b strings.Builder
	for _, f := range z.File {
		name := strings.ToLower(f.Name)
		var wanted bool
		switch ext[:3] {
		case "doc":
			wanted = strings.HasPrefix(name, "word/") && strings.HasSuffix(name, ".xml") &&
				(strings.Contains(name, "document") || strings.Contains(name, "header") || strings.Contains(name, "footer") ||
					strings.Contains(name, "footnotes") || strings.Contains(name, "endnotes") || strings.Contains(name, "comments"))
		case "ppt":
			wanted = (strings.HasPrefix(name, "ppt/slides/") || strings.HasPrefix(name, "ppt/notesslides/")) && strings.HasSuffix(name, ".xml")
		case "xls":
			wanted = name == "xl/sharedstrings.xml" || strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml")
		}
		// A part that unpacks to more than this is no text to read.
		if !wanted || f.UncompressedSize64 > 128<<20 {
			continue
		}
		r, err := f.Open()
		if err != nil {
			continue
		}
		xmlText(&b, r, ext[:3] == "xls" && name != "xl/sharedstrings.xml")
		r.Close()
		if b.Len() > maxText {
			break
		}
	}
	return b.String(), b.Len() > 0
}

// xmlText writes the words of a part: the text of its elements, a line a
// paragraph. Of a sheet only the cells that hold their own text are read:
// the others are numbers, or point at the strings read elsewhere.
func xmlText(b *strings.Builder, r io.Reader, sheet bool) {
	d := xml.NewDecoder(r)
	d.Strict = false
	inText := 0
	for {
		tok, err := d.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText++
			case "is":
				if sheet {
					inText++
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText--
				if sheet {
					b.WriteByte(' ')
				}
			case "is":
				if sheet {
					inText--
				}
			case "p", "si", "row":
				b.WriteByte('\n')
			case "tab", "br", "c":
				b.WriteByte(' ')
			}
		case xml.CharData:
			if inText > 0 && (!sheet || inText > 1) {
				b.Write(t)
			}
		}
		if b.Len() > maxText {
			return
		}
	}
}

// match reports whether a text holds every term, without case, and gives
// the text around the first of them.
func match(text string, terms []string) (string, bool) {
	lower := strings.ToLower(text)
	first := -1
	for _, term := range terms {
		i := strings.Index(lower, term)
		if i < 0 {
			return "", false
		}
		if first < 0 || i < first {
			first = i
		}
	}
	// Lower case may be longer or shorter than what it came from: the
	// snippet is cut of the lower-cased text where the lengths differ.
	src := text
	if len(lower) != len(text) {
		src = lower
	}
	return snippet(src, first), true
}

// snippetLength is how much text a snippet shows, as Lertaro's.
const snippetLength = 120

// snippet is the text around a place, its white space one space each.
func snippet(text string, at int) string {
	start := max(at-snippetLength/3, 0)
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	end := min(start+snippetLength*3, len(text))
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	space, n := false, 0
	for _, r := range text[start:end] {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		if n++; n >= snippetLength {
			b.WriteString("…")
			break
		}
	}
	return b.String()
}
