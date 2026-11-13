package content

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

func zipped(t *testing.T, path string, parts map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z := zip.NewWriter(f)
	for name, body := range parts {
		w, _ := z.Create(name)
		w.Write([]byte(body))
	}
	z.Close()
}

func TestSearch(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) { os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644) }
	write("plain.txt", "The quarterly Budget is due.\nMarmalade for breakfast.")
	write("other.md", "# Notes\nnothing of the kind here")
	write("wide.txt", "\xff\xfem\x00a\x00r\x00m\x00a\x00l\x00a\x00d\x00e\x00 \x00w\x00i\x00d\x00e\x00")
	write("中文.txt", "年度预算报告，橘子酱 marmalade 的配方")
	write("big.log", strings.Repeat("x", 4000)+" marmalade at the end")
	zipped(t, filepath.Join(dir, "letter.docx"), map[string]string{
		"word/document.xml": `<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>Dear Sir,</w:t></w:r></w:p><w:p><w:r><w:t>the marma</w:t></w:r><w:r><w:t>lade arrived &amp; was fine.</w:t></w:r></w:p></w:body></w:document>`,
		"word/styles.xml":   `<w:styles xmlns:w="w"><w:t>marmalade in a part that holds no words</w:t></w:styles>`,
	})
	zipped(t, filepath.Join(dir, "slides.pptx"), map[string]string{
		"ppt/slides/slide1.xml": `<p:sld xmlns:a="a" xmlns:p="p"><a:p><a:r><a:t>Marmalade roadmap</a:t></a:r></a:p></p:sld>`,
	})
	zipped(t, filepath.Join(dir, "sheet.xlsx"), map[string]string{
		"xl/sharedStrings.xml":     `<sst><si><t>marmalade stock</t></si><si><t>jars</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c t="s"><v>0</v></c><c><v>1234</v></c></row></sheetData></worksheet>`,
	})
	zipped(t, filepath.Join(dir, "numbers.xlsx"), map[string]string{
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c><v>77</v></c></row></sheetData></worksheet>`,
	})
	entries, _ := os.ReadDir(dir)
	var files []File
	for _, e := range entries {
		info, _ := e.Info()
		files = append(files, File{Path: filepath.Join(dir, e.Name()), Size: info.Size(), Modified: uint32(info.ModTime().Unix())})
	}
	cache := t.TempDir()
	find := func(query string) map[string]string {
		got := map[string]string{}
		var read atomic.Int64
		Search(context.Background(), files, Terms(query), Options{CacheDir: cache}, &read, func(h Hit) {
			got[filepath.Base(h.File.Path)] = h.Snippet
		})
		if int(read.Load()) != len(files) {
			t.Errorf("%d of %d files read", read.Load(), len(files))
		}
		return got
	}
	names := func(m map[string]string) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	got := find("MARMALADE")
	want := []string{"big.log", "letter.docx", "plain.txt", "sheet.xlsx", "slides.pptx", "wide.txt", "中文.txt"}
	if strings.Join(names(got), " ") != strings.Join(want, " ") {
		t.Fatalf("marmalade is found in %v, want %v", names(got), want)
	}
	if s := got["plain.txt"]; !strings.Contains(s, "Budget is due. Marmalade for breakfast") {
		t.Errorf("the snippet of plain.txt is %q", s)
	}
	if s := got["big.log"]; !strings.HasPrefix(s, "…") || !strings.Contains(s, "marmalade at the end") || len(s) > 200 {
		t.Errorf("the snippet of big.log is %q", s)
	}
	if s := got["letter.docx"]; !strings.Contains(s, "the marmalade arrived & was fine") {
		t.Errorf("the snippet of letter.docx is %q", s)
	}
	// Every word, in any order, anywhere in the file.
	if got := names(find("breakfast budget")); len(got) != 1 || got[0] != "plain.txt" {
		t.Errorf("two words are found in %v", got)
	}
	if got := names(find("预算 marmalade")); len(got) != 1 || got[0] != "中文.txt" {
		t.Errorf("Chinese words are found in %v", got)
	}
	if got := find("zebra"); len(got) != 0 {
		t.Errorf("zebra is found in %v", got)
	}
	// The documents' text was kept, and is read from there the next time.
	kept, _ := filepath.Glob(filepath.Join(cache, "*", "*.txt"))
	if len(kept) < 3 {
		t.Errorf("%d texts kept", len(kept))
	}
	// A file over the size is not read.
	hits := 0
	Search(context.Background(), files, Terms("marmalade"), Options{MaxSize: 100}, nil, func(Hit) { hits++ })
	if hits == 0 || hits >= len(want) {
		t.Errorf("with a limit of 100 bytes %d files match", hits)
	}
}

// A document that is not its text is read by the system's reader for its
// kind, where it has one: RTF, which every Windows reads, and a PDF if
// one is at hand (MYFILES_TEST_PDF names it).
func TestSystemReader(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the system's readers are Windows's")
	}
	dir := t.TempDir()
	rtf := filepath.Join(dir, "note.rtf")
	os.WriteFile(rtf, []byte(`{\rtf1\ansi Hello {\b marmalade} world.\par Second line.}`), 0o644)
	ex := newExtractor()
	defer ex.close()
	text, ok := ex.text(rtf)
	if !ok {
		t.Skip("no reader for RTF here")
	}
	if !strings.Contains(text, "marmalade") || strings.Contains(text, `\rtf`) {
		t.Errorf("the RTF reads as %q", text)
	}
	// A PDF, of which the system has no reader of its own, by the reader
	// built in.
	if pdf := os.Getenv("MYFILES_TEST_PDF"); pdf != "" {
		text, ok := pdfText(pdf)
		t.Logf("%s: read %v, %d bytes of text", filepath.Base(pdf), ok, len(text))
		if !ok || len(text) < 100 {
			t.Error("the PDF was not read")
		}
		info, _ := os.Stat(pdf)
		hits := 0
		Search(context.Background(), []File{{Path: pdf, Size: info.Size()}}, Terms(os.Getenv("MYFILES_TEST_PDF_WORD")), Options{MaxSize: 1 << 30}, nil, func(h Hit) {
			hits++
			t.Logf("found: %q", h.Snippet)
		})
		if word := os.Getenv("MYFILES_TEST_PDF_WORD"); word != "" && hits != 1 {
			t.Errorf("%q is not found in the PDF", word)
		}
	}
}
