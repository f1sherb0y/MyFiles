package index

import (
	"regexp"
	"runtime"
	"strings"
	"sync"

	"myfiles/internal/search"
)

// Effect is what a rule does to the files it covers.
type Effect uint8

const (
	// Exclude leaves them out of results; Low and High order them after
	// and before the others; Normal undoes a Low or a High set on a
	// folder above.
	Exclude Effect = iota
	Low
	Normal
	High
)

// Rule covers files by a path, a pattern or a regular expression, and
// says what becomes of them. How Pattern is written says which it is:
//
//	D:\Cache, %ProgramData%   a folder and all it holds
//	node_modules, *.tmp       a name, at any depth
//	build\out, **\obj\**      a path from the drive's root, * within one
//	                          folder and ** across several
//	/^\./                     a regular expression, of the name or of the
//	                          path from the drive's root
type Rule struct {
	Pattern string `json:"pattern"`
	Effect  Effect `json:"effect"`
	// Top puts the files of a High rule before all others.
	Top bool `json:"top,omitempty"`
}

// DefaultRules are those of a first run, as Lertaro's: what package
// managers and builds fill, and the system's own folders, come after the
// user's files.
func DefaultRules() []Rule {
	low := func(p string) Rule { return Rule{Pattern: p, Effect: Low} }
	return []Rule{
		{Pattern: "node_modules", Effect: Exclude},
		low(`%SystemDrive%\Windows.old`),
		low(`%SystemDrive%\$WINDOWS.~BT`),
		low(`%SystemDrive%\$Windows.~WS`),
		low(`%ProgramData%`),
		low(`%SystemRoot%`),
		low(`%USERPROFILE%\AppData`),
		low(`%ProgramW6432%`),
		low(`%ProgramFiles(x86)%`),
		low(`build\`),
		low(`debug\`),
		low(`target\`),
		low(`/^\./`),
		low(`/^~/`),
		low(`/^\$/`),
	}
}

// Rules are the user's rules, made ready to mark the rows of indexes.
type Rules struct {
	paths []pathRule
	names []nameRule
	// deep are the patterns and expressions that read a file's path from
	// the drive's root: only folders are asked, what they hold follows.
	deep []deepRule
}

type verdict struct {
	effect Effect
	top    bool
}

type pathRule struct {
	path string
	verdict
}

// nameRule covers names: one name, names starting with a text, names
// matching a pattern, or an expression.
type nameRule struct {
	exact  string
	prefix string
	glob   string
	re     *regexp.Regexp
	verdict
}

type deepRule struct {
	re *regexp.Regexp
	verdict
}

// NewRules reads the rules. expand turns what a path names by an
// environment variable into the path, nil for none.
func NewRules(rules []Rule, expand func(string) string) *Rules {
	rs := &Rules{}
	for _, r := range rules {
		v := verdict{r.Effect, r.Top && r.Effect == High}
		p := clean(r.Pattern)
		if p == "" {
			continue
		}
		if len(p) > 2 && p[0] == '/' && p[len(p)-1] == '/' {
			body := p[1 : len(p)-1]
			if prefix, ok := literalPrefix(body); ok {
				rs.names = append(rs.names, nameRule{prefix: strings.ToLower(prefix), verdict: v})
				continue
			}
			re, err := regexp.Compile("(?i)" + body)
			if err != nil {
				continue
			}
			rs.names = append(rs.names, nameRule{re: re, verdict: v})
			rs.deep = append(rs.deep, deepRule{re, v})
			continue
		}
		p = strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)
		if len(p) == 2 && p[1] == ':' {
			p += `\`
		}
		wild := strings.ContainsAny(strings.TrimPrefix(p, `\\?\`), "*?")
		switch {
		case !wild && isFullPath(p):
			if expand != nil {
				p = expand(p)
			}
			if strings.Contains(p, "%") {
				// A variable that is not set names no folder.
				continue
			}
			rs.paths = append(rs.paths, pathRule{strings.TrimRight(p, `\`), v})
		case !strings.Contains(p, `\`):
			if wild {
				rs.names = append(rs.names, nameRule{glob: strings.ToLower(p), verdict: v})
			} else {
				rs.names = append(rs.names, nameRule{exact: p, verdict: v})
			}
		default:
			if re, err := regexp.Compile(globRegexp(p)); err == nil {
				rs.deep = append(rs.deep, deepRule{re, v})
			}
		}
	}
	return rs
}

// clean takes the spaces and the quotes off a rule as typed.
func clean(p string) string {
	p = strings.TrimSpace(p)
	for range 3 {
		if len(p) >= 2 && (p[0] == '"' || p[0] == '\'') && p[len(p)-1] == p[0] {
			p = strings.TrimSpace(p[1 : len(p)-1])
		}
	}
	return p
}

func isFullPath(p string) bool {
	switch {
	case len(p) >= 2 && p[1] == ':' && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z'):
		return true
	case strings.HasPrefix(p, `\\`), strings.HasPrefix(p, "%"):
		return true
	}
	return false
}

// literalPrefix reads an expression that only asks for names starting
// with a text, as ^\. does, which is told without the expression.
func literalPrefix(body string) (string, bool) {
	if !strings.HasPrefix(body, "^") {
		return "", false
	}
	var b strings.Builder
	for i := 1; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '\\' && i+1 < len(body) && strings.IndexByte(`.$^*+?()[]{}|\~-`, body[i+1]) >= 0:
			i++
			b.WriteByte(body[i])
		case strings.IndexByte(`.$^*+?()[]{}|\`, c) >= 0:
			return "", false
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), b.Len() > 0
}

// globRegexp is the expression of a pattern of a path from the drive's
// root: * within one folder, ** across several.
func globRegexp(p string) string {
	var b strings.Builder
	b.WriteString("(?i)^")
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '*' && i+1 < len(p) && p[i+1] == '*':
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString(`[^\\]*`)
		case c == '?':
			b.WriteString(`[^\\]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	// A folder covers what it holds.
	b.WriteString(`(\\.*)?$`)
	return b.String()
}

// strength orders what patterns say: a High beats a Low, which beats
// saying nothing.
func strength(e Effect) uint8 {
	switch e {
	case High:
		return 2
	case Low:
		return 1
	}
	return 0
}

// The parts of what is known of a row while rules are applied: what the
// nearest path rule above says, the strongest of what the patterns above
// say, and whether something above leaves it out or hides it.
const (
	compPathMask = 3 // 0 none, else the Effect
	compPathTop  = 1 << 2
	compPatShift = 3 // 2 bits of strength
	compPatTop   = 1 << 5
	compExcluded = 1 << 6
	compHidden   = 1 << 7
)

// own is what the patterns say of a name by itself, as a row's marks:
// what a file made after the index was built takes on top of its
// folder's.
func (rs *Rules) own(name string, flags uint16) uint8 {
	if rs == nil {
		return 0
	}
	c := rs.ownComp([]byte(name))
	return marksOfComp(c) &^ markHidden
}

// ownComp is what the name rules say of a name.
func (rs *Rules) ownComp(name []byte) uint8 {
	var c uint8
	for i := range rs.names {
		r := &rs.names[i]
		switch {
		case r.exact != "":
			if !equalFold(name, r.exact) {
				continue
			}
		case r.prefix != "":
			if len(name) < len(r.prefix) || !equalFold(name[:len(r.prefix)], r.prefix) {
				continue
			}
		case r.glob != "":
			if !search.GlobMatch(r.glob, name) {
				continue
			}
		default:
			if !r.re.Match(name) {
				continue
			}
		}
		c = addPattern(c, r.verdict)
	}
	return c
}

func addPattern(c uint8, v verdict) uint8 {
	if v.effect == Exclude {
		return c | compExcluded
	}
	cur, s := c>>compPatShift&3, strength(v.effect)
	switch {
	case s > cur:
		c = c&^(3<<compPatShift|compPatTop) | s<<compPatShift
		if v.top {
			c |= compPatTop
		}
	case s == cur && v.top:
		c |= compPatTop
	}
	return c
}

// marksOfComp is what a row is marked with, of what is known of it: the
// path rule's word, unless the patterns say something stronger.
func marksOfComp(c uint8) uint8 {
	var m uint8
	if c&compExcluded != 0 {
		m |= markExcluded
	}
	if c&compHidden != 0 {
		m |= markHidden
	}
	effect, top := Normal, false
	if pe := Effect(c & compPathMask); pe != 0 {
		effect, top = pe, c&compPathTop != 0
	}
	ps := c >> compPatShift & 3
	if ps > strength(effect) || c&compPathMask == 0 && ps > 0 {
		top = c&compPatTop != 0
		if ps == 2 {
			effect = High
		} else {
			effect = Low
		}
	}
	switch effect {
	case Low:
		m |= markLow
	case High:
		m |= markHigh
		if top {
			m |= markPinned
		}
	}
	return m
}

// mark works out the marks of every row: hidden, left out, ordered after
// or before the others, each taking what the folders above it say.
func (v *View) mark(rs *Rules) {
	ix := v.ix
	n := ix.n
	// What each row's rules come to, for as long as the marks are worked
	// out: beside an index in a file, in a file.
	tmp := NewScratch(ix.dir)
	defer tmp.Close()
	comps := tmp.Bytes(n)[:n]
	if rs == nil {
		rs = &Rules{}
	}
	// What the names say, a stretch of rows a goroutine.
	if len(rs.names) > 0 {
		workers := runtime.GOMAXPROCS(0)
		chunk := (n + workers - 1) / workers
		var wg sync.WaitGroup
		for lo := 1; lo < n; lo += chunk {
			wg.Go(func() {
				for r := lo; r < min(lo+chunk, n); r++ {
					comps[r] = rs.ownComp(ix.nameBytes(Row(r)))
				}
			})
		}
		wg.Wait()
	}
	// What the paths say, at the folders they name.
	type at struct {
		row Row
		verdict
	}
	var paths []at
	for _, p := range rs.paths {
		if rest, ok := ix.rel(p.path); ok {
			if ref, ok := v.resolveBase(rest); ok {
				paths = append(paths, at{ref, p.verdict})
			}
		}
	}
	apply := func(r Row, c uint8) uint8 {
		for _, p := range paths {
			if p.row != r {
				continue
			}
			if p.effect == Exclude {
				c |= compExcluded
				continue
			}
			c = c&^(compPathMask|compPathTop) | uint8(p.effect)
			if p.top {
				c |= compPathTop
			}
		}
		return c
	}
	var buf []byte
	// A folder comes before what it holds: each row takes its folder's.
	for r := 1; r < n; r++ {
		own := comps[r]
		up := comps[ix.parent[r]]
		c := up & (compPathMask | compPathTop | compExcluded | compHidden)
		// Of the patterns, the strongest on the way down.
		c |= up & (3<<compPatShift | compPatTop)
		if os := own >> compPatShift & 3; os > c>>compPatShift&3 {
			c = c&^(3<<compPatShift|compPatTop) | own&(3<<compPatShift|compPatTop)
		} else if os == c>>compPatShift&3 {
			c |= own & compPatTop
		}
		c |= own & compExcluded
		if ix.flags[r]&FlagHidden != 0 {
			c |= compHidden
		}
		if len(paths) > 0 {
			c = apply(Row(r), c)
		}
		if len(rs.deep) > 0 && ix.flags[r]&FlagDir != 0 && c&compExcluded == 0 {
			buf = ix.appendPath(buf[:0], Row(r))
			rel := buf[len(ix.Root):]
			for i := range rs.deep {
				if rs.deep[i].re.Match(rel) {
					c = addPattern(c, rs.deep[i].verdict)
				}
			}
		}
		comps[r] = c
		ix.marks[r] = ix.marks[r]&(markGone|markOver) | marksOfComp(c)
	}
	// The delta's rows take their folders', as when they were added.
	for i := range v.d.rows {
		d := &v.d.rows[i]
		m := v.marksOf(d.parent)&inherit | rs.own(d.name, d.flags)
		if d.flags&FlagHidden != 0 {
			m |= markHidden
		}
		d.marks = d.marks&markGone | m
	}
}

// resolveBase is the row of a path under the root, among the index's own
// rows.
func (v *View) resolveBase(rest string) (Row, bool) {
	cur := Row(0)
	for rest != "" {
		seg := rest
		if i := strings.IndexByte(rest, '\\'); i >= 0 {
			seg, rest = rest[:i], strings.TrimLeft(rest[i:], `\`)
		} else {
			rest = ""
		}
		next, ok := v.ix.child(cur, seg)
		if !ok {
			return 0, false
		}
		cur = next
	}
	return cur, true
}
