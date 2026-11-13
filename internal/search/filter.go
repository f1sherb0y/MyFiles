package search

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Category is a kind of file, by its extension.
type Category struct {
	// Key is the keyword a query names it by, as in ":@doc".
	Key  string
	Exts []string
}

// Categories are the kinds of files a query can ask for, as Lertaro has
// them: one table for the tokens and for the buttons that narrow results.
var Categories = []Category{
	{"doc", strings.Fields("txt md log doc docx rtf odt pdf xls xlsx csv ods ppt pptx odp epub mobi one vsdx vsd wps et dps odf odg odb eqp mmx tex ini conf")},
	{"img", strings.Fields("png jpg jpeg jfif gif bmp webp svg ico tif tiff heic heif avif raw psd ai jxl")},
	{"video", strings.Fields("mp4 mkv avi mov wmv flv webm m4v mpg mpeg ts m2ts mts 3gp rmvb rm vob ogv")},
	{"audio", strings.Fields("mp3 wav flac aac ogg oga m4a wma ape opus aiff aif mid midi dsf dff")},
	{"zip", strings.Fields("zip rar 7z tar gz bz2 xz iso wim esd")},
	// A shortcut is none of these by itself: it is of the kind of what it
	// points at (see Filter.Links).
	{"app", strings.Fields("exe url appref-ms msi msix appx bat cmd com")},
}

// ruleOf is the rule of a keyword: the user's own, else a category's.
func ruleOf(keyword string, filters map[string]string) (string, bool) {
	for kw, rule := range filters {
		if strings.EqualFold(kw, keyword) {
			return rule, true
		}
	}
	switch strings.ToLower(keyword) {
	case "folder", "dir":
		return ":f", true
	case "file":
		return ":-f", true
	}
	for _, c := range Categories {
		if strings.EqualFold(c.Key, keyword) {
			return "*." + strings.Join(c.Exts, ";*."), true
		}
	}
	return "", false
}

// Filter is what the tokens of a query let through, by name and by kind.
type Filter struct {
	DirsOnly, FilesOnly bool
	// key tells filters made of rules apart.
	key string
	// groups must each admit a name.
	groups []group
}

// group admits a name by any of its ways.
type group struct {
	exts  map[string]bool
	globs []string
	// dirs and files admit every folder and every file.
	dirs, files bool
	// dirGlobs lets the globs admit folders too, which otherwise only
	// dirs does.
	dirGlobs bool
	// noDirs is unused beyond telling an extension filter apart.
	noDirs bool
	// links lets shortcuts through, whatever they are called: a kind of
	// file is asked for, and a shortcut is of the kind of what it points
	// at, which whoever shows the results reads.
	links bool
}

// rule adds to the group what a rule admits: extensions and patterns
// apart by commas, semicolons or spaces, ":f" for folders, ":-f" for
// files, and "@keyword" for another rule.
func (g *group) rule(rule string, filters map[string]string, depth int) {
	for _, tok := range strings.FieldsFunc(rule, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		low := strings.ToLower(tok)
		switch {
		case low == ":f" || low == "folder" || low == "dir":
			g.dirs = true
		case low == ":-f" || low == "file":
			g.files = true
		case strings.HasPrefix(low, "@"):
			if sub, ok := ruleOf(low[1:], filters); ok && depth < 8 {
				g.rule(sub, filters, depth+1)
			}
		case strings.HasPrefix(low, "*.") && !strings.ContainsAny(low[2:], "*?."):
			g.ext(low[2:])
		case !strings.ContainsAny(low, "*?"):
			// An extension; one of several parts, as min.js, is the end
			// of a name.
			if ext := strings.TrimPrefix(strings.TrimPrefix(low, "*"), "."); strings.Contains(ext, ".") {
				g.globs = append(g.globs, "*."+ext)
			} else {
				g.ext(ext)
			}
		default:
			g.globs = append(g.globs, low)
		}
	}
}

func (g *group) ext(ext string) {
	if g.exts == nil {
		g.exts = map[string]bool{}
	}
	g.exts[ext] = true
}

// NewFilter is a filter that admits what one of the rules does, as the
// buttons that narrow results to a kind ask for.
func NewFilter(rule string, filters map[string]string) Filter {
	g := group{links: true}
	g.rule(rule, filters, 0)
	return Filter{groups: []group{g}, key: fmt.Sprintf("%s\x01%x", rule, hashOf(filters))}
}

// ExtFilter is a filter that admits files of the extensions given, and
// nothing else: not the shortcuts a kind lets through.
func ExtFilter(exts []string) Filter {
	g := group{exts: map[string]bool{}}
	for _, ext := range exts {
		g.exts[strings.ToLower(ext)] = true
	}
	return Filter{groups: []group{g}, key: "\x03" + strings.Join(exts, ",")}
}

// Key tells a filter made by NewFilter from the others so made: two of
// one key let the same through.
func (f Filter) Key() string { return f.key }

// And is a filter that admits what both do.
func (f Filter) And(o Filter) Filter {
	out := Filter{DirsOnly: f.DirsOnly || o.DirsOnly, FilesOnly: f.FilesOnly || o.FilesOnly, key: f.key + "\x02" + o.key}
	out.groups = append(append(out.groups, f.groups...), o.groups...)
	return out
}

// Any reports a filter that leaves something out.
func (f *Filter) Any() bool { return f.DirsOnly || f.FilesOnly || len(f.groups) > 0 }

// NeedsName reports a filter that must read names, not only kinds.
func (f *Filter) NeedsName() bool { return len(f.groups) > 0 }

// Admits reports whether a name of a file, or of a folder, passes.
func (f *Filter) Admits(name []byte, dir bool) bool {
	if dir && f.FilesOnly || !dir && f.DirsOnly {
		return false
	}
	for i := range f.groups {
		if !f.groups[i].admits(name, dir, true) {
			return false
		}
	}
	return true
}

// Links reports a filter that lets shortcuts through for what they point
// at: its results that are shortcuts are to be asked what that is, and
// kept when AdmitsTarget says so.
func (f *Filter) Links() bool {
	for i := range f.groups {
		if f.groups[i].links {
			return true
		}
	}
	return false
}

// AdmitsTarget reports whether what a shortcut points at passes: by the
// name of a file, or of a folder. A shortcut that names no path, as those
// of programs from the Store, is a program: AdmitsProgram.
func (f *Filter) AdmitsTarget(name []byte, dir bool) bool {
	for i := range f.groups {
		if !f.groups[i].admits(name, dir, false) {
			return false
		}
	}
	return true
}

// AdmitsProgram reports whether a program passes.
func (f *Filter) AdmitsProgram() bool { return f.AdmitsTarget([]byte("program.exe"), false) }

// IsLink reports whether a name is a shortcut's.
func IsLink(name []byte) bool {
	n := len(name)
	return n > 4 && name[n-4] == '.' && name[n-3]|0x20 == 'l' && name[n-2]|0x20 == 'n' && name[n-1]|0x20 == 'k'
}

func (g *group) admits(name []byte, dir, links bool) bool {
	if dir {
		if g.dirs {
			return true
		}
		return g.dirGlobs && g.glob(name)
	}
	if links && g.links && IsLink(name) {
		return true
	}
	if g.files {
		return true
	}
	if len(g.exts) > 0 {
		var buf [24]byte
		if ext, ok := extOf(name, buf[:0]); ok && g.exts[string(ext)] {
			return true
		}
	}
	return g.glob(name)
}

func (g *group) glob(name []byte) bool {
	for _, pat := range g.globs {
		if GlobMatch(pat, name) {
			return true
		}
	}
	return false
}

// extOf is a name's extension in lower case, without its dot, written to
// buf.
func extOf(name []byte, buf []byte) ([]byte, bool) {
	dot := -1
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 || dot == len(name)-1 {
		return nil, false
	}
	for _, b := range name[dot+1:] {
		if b >= 0x80 {
			return []byte(strings.ToLower(string(name[dot+1:]))), true
		}
		buf = append(buf, asciiLower[b])
	}
	return buf, true
}

// GlobMatch matches a name against a pattern in lower case, where *
// stands for any characters and ? for one.
func GlobMatch(pat string, name []byte) bool {
	p, n := 0, 0
	star, mark := -1, 0
	for n < len(name) {
		c, size := rune(name[n]), 1
		if c >= 0x80 {
			c, size = utf8.DecodeRune(name[n:])
		}
		c = lowerOf(c)
		if p < len(pat) {
			pc, psize := rune(pat[p]), 1
			if pc >= 0x80 {
				pc, psize = utf8.DecodeRuneInString(pat[p:])
			}
			if pc == '*' {
				star, mark = p, n
				p++
				continue
			}
			if pc == '?' || pc == c {
				p += psize
				n += size
				continue
			}
		}
		if star < 0 {
			return false
		}
		// The star takes one character more.
		_, size = utf8.DecodeRune(name[mark:])
		mark += size
		p, n = star+1, mark
	}
	for p < len(pat) && pat[p] == '*' {
		p++
	}
	return p == len(pat)
}
