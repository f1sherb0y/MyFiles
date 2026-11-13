package search

import (
	"fmt"
	"strings"
	"unicode"
)

// SortBy is what a token of a query orders results by, in place of how
// well they match.
type SortBy uint8

const (
	SortRelevance SortBy = iota
	SortSize
	SortCreated
	SortModified
	SortAccessed
)

// Query is what the user typed, read: the terms, and what the tokens
// after them ask for.
type Query struct {
	// Text is the query without its tokens and its leading *, as names
	// are compared with it for identity.
	Text    string
	Pattern *Pattern
	// Bypass is set by a leading *: the rules that leave folders out do
	// not, for this once.
	Bypass bool

	// Path is set for a query with a path separator in it.
	Path *PathQuery

	// Filter is what the tokens let through, and Segments the patterns
	// of which a folder above each result, or its own name, must match
	// one each.
	Filter   Filter
	Segments []*Pattern
	Sort     SortBy
	// Descending orders by Sort from the largest or the newest.
	Descending bool
	// Unknown is set by a token nothing understands: nothing matches.
	Unknown bool
	// Key tells queries apart: two of one key ask for the same.
	Key string
}

// hashOf tells one set of named rules from another.
func hashOf(m map[string]string) uint64 {
	var sum uint64
	for k, v := range m {
		h := uint64(14695981039346656037)
		for _, s := range [2]string{k, v} {
			for i := 0; i < len(s); i++ {
				h = (h ^ uint64(s[i])) * 1099511628211
			}
			h = (h ^ 0xFF) * 1099511628211
		}
		// In whatever order a map gives them.
		sum += h
	}
	return sum
}

// PathQuery is a query that names folders: "src\main" asks for names
// matching "main" under a folder matching "src".
type PathQuery struct {
	// Drive is the letter the path starts with, in lower case, 0 for none.
	Drive rune
	// Exact is the path as typed, in lower case, without the separators
	// that end it, when it starts at a drive: a folder of that path lists
	// what it holds, narrowed by Child when the path does not end with a
	// separator.
	Exact   string
	Listing bool
	// Dirs are the patterns the folders above a result must match, the
	// outermost first, and File the pattern of its name, nil for any.
	Dirs []*Pattern
	File *Pattern
}

// Empty reports a query that asks for nothing.
func (q *Query) Empty() bool {
	return q.Path == nil && q.Pattern.Empty() && q.Pattern.Drive == 0
}

// ParseQuery reads what the user typed. prefix is the character that
// starts the tokens after the terms, ':' unless the user chose another,
// and filters are the user's own, by their keywords.
func ParseQuery(text string, opts Options, prefix rune, filters map[string]string) *Query {
	q := &Query{}
	if prefix == 0 {
		prefix = ':'
	}
	q.Key = fmt.Sprintf("%s\x00%v%c%x", text, opts, prefix, hashOf(filters))
	clean, tokens := stripTokens(text, prefix)
	if strings.HasPrefix(clean, "*") {
		q.Bypass = true
		clean = clean[1:]
	}
	q.Text = clean
	for _, tok := range tokens {
		q.token(tok, opts, filters)
	}
	norm := strings.TrimSpace(strings.ReplaceAll(Normalize(clean), "/", `\`))
	if strings.Contains(norm, `\`) && !strings.Contains(norm, `\ `) {
		q.Path = parsePath(norm, opts)
		q.Pattern = &Pattern{opts: opts}
		// A path names what it wants: the rules do not hide it.
		q.Bypass = true
		return q
	}
	q.Pattern = Parse(clean, opts)
	return q
}

func parsePath(path string, opts Options) *PathQuery {
	pq := &PathQuery{}
	r := []rune(path)
	if len(r) >= 2 && unicode.IsLetter(r[0]) {
		switch r[1] {
		case ':':
			pq.Drive = unicode.ToLower(r[0])
		case '\\':
			// "c\foo" is "c:\foo".
			pq.Drive = unicode.ToLower(r[0])
			path = string(r[0]) + `:\` + strings.TrimLeft(string(r[2:]), `\`)
		}
	}
	listing := strings.HasSuffix(path, `\`)
	if pq.Drive != 0 {
		exact := strings.ToLower(path)
		for len(exact) > 3 && strings.HasSuffix(exact, `\`) {
			exact = exact[:len(exact)-1]
		}
		pq.Exact, pq.Listing = exact, listing
	}
	// The folders, and after the last separator the name.
	cut := strings.LastIndexByte(path, '\\')
	dirs, file := path[:cut], path[cut+1:]
	for _, seg := range strings.Split(dirs, `\`) {
		if seg == "" {
			continue
		}
		if sr := []rune(seg); len(sr) == 2 && sr[1] == ':' && unicode.IsLetter(sr[0]) {
			pq.Drive = unicode.ToLower(sr[0])
			continue
		}
		pq.Dirs = append(pq.Dirs, ParseText(seg, opts))
	}
	if strings.TrimSpace(file) != "" {
		pq.File = Parse(file, opts)
		if pq.File.Drive != 0 {
			pq.Drive = pq.File.Drive
		}
	}
	return pq
}

// token reads one token of those after the terms.
func (q *Query) token(tok string, opts Options, filters map[string]string) {
	switch {
	case isSortToken(tok):
		desc := strings.Contains(tok, "-")
		switch strings.ToUpper(strings.Trim(tok, "-")) {
		case "S":
			q.Sort, q.Descending = SortSize, desc
		case "C":
			q.Sort, q.Descending = SortCreated, desc
		case "M":
			q.Sort, q.Descending = SortModified, desc
		case "A":
			q.Sort, q.Descending = SortAccessed, desc
		case "F":
			if desc {
				q.Filter.FilesOnly = true
			} else {
				q.Filter.DirsOnly = true
			}
		}
	case len(tok) > 1 && tok[0] == '.':
		g := group{noDirs: true, exts: map[string]bool{}}
		for _, ext := range strings.Split(tok[1:], ".") {
			if ext != "" {
				g.exts[strings.ToLower(ext)] = true
			}
		}
		q.Filter.groups = append(q.Filter.groups, g)
	case len(tok) > 1 && tok[0] == '@':
		g := group{links: true}
		known := false
		for _, kw := range strings.Split(tok[1:], "|") {
			if rule, ok := ruleOf(strings.TrimSpace(kw), filters); ok {
				g.rule(rule, filters, 0)
				known = true
			}
		}
		if !known {
			q.Unknown = true
			return
		}
		q.Filter.groups = append(q.Filter.groups, g)
	case len(tok) > 1 && tok[0] == '?':
		var g group
		for _, pat := range strings.FieldsFunc(tok[1:], func(r rune) bool { return r == '|' || r == ';' }) {
			if !strings.HasPrefix(pat, "*") {
				pat = "*" + pat
			}
			if !strings.HasSuffix(pat, "*") {
				pat += "*"
			}
			g.globs = append(g.globs, strings.ToLower(pat))
		}
		g.dirGlobs = true
		q.Filter.groups = append(q.Filter.groups, g)
	case len(tok) > 1 && tok[0] == ':':
		if p := Parse(tok[1:], opts); !p.Empty() {
			q.Segments = append(q.Segments, p)
		}
	default:
		q.Unknown = true
	}
}

func isSortToken(tok string) bool {
	t := strings.TrimPrefix(tok, "-")
	if t == tok {
		t = strings.TrimSuffix(tok, "-")
	} else if strings.HasSuffix(t, "-") {
		t = t[:len(t)-1]
	}
	return len(t) == 1 && strings.ContainsAny(t, "SCMAFscmaf")
}

// stripTokens takes the tokens off the end of a query: what follows the
// last prefix character that starts the query or comes after a space, as
// "report :@doc,M-". A colon inside a word, as that of a drive or a time,
// starts none.
func stripTokens(query string, prefix rune) (clean string, tokens []string) {
	r := []rune(strings.TrimRightFunc(query, unicode.IsSpace))
	if len(r) < 2 {
		return query, nil
	}
	at := -1
	var quote rune
	for i := len(r) - 1; i >= 0; i-- {
		c := r[i]
		if escaped(r, i) {
			continue
		}
		if c == '"' || c == '\'' {
			switch quote {
			case 0:
				quote = c
			case c:
				quote = 0
			}
			continue
		}
		if quote != 0 || c != prefix {
			continue
		}
		if i == 0 || unicode.IsSpace(r[i-1]) && !escaped(r, i-1) {
			at = i
			break
		}
		// A run of prefix characters, as "::", starts where the run does.
		j := i
		for j > 0 && r[j-1] == prefix {
			j--
		}
		if j < i && (j == 0 || unicode.IsSpace(r[j-1]) && !escaped(r, j-1)) {
			at = j
		}
		break
	}
	if at < 0 || len(r)-at < 2 {
		return query, nil
	}
	parts, ok := splitTokens(r[at+1:])
	if !ok {
		return query, nil
	}
	return strings.TrimRightFunc(string(r[:at]), unicode.IsSpace), parts
}

// escaped reports a character with an odd number of backslashes before
// it.
func escaped(r []rune, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && r[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

// splitTokens splits tokens at the commas outside quotes, and takes the
// quotes and the escapes off each. A token that is empty, or holds a
// space outside quotes, makes the whole no tokens at all.
func splitTokens(r []rune) ([]string, bool) {
	var out []string
	var cur []rune
	var quote rune
	bare := false // a space outside quotes, inside the token
	flush := func() bool {
		tok := strings.TrimSpace(string(cur))
		cur = cur[:0]
		if tok == "" {
			return false
		}
		out = append(out, tok)
		return true
	}
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\' && i+1 < len(r):
			i++
			cur = append(cur, r[i])
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur = append(cur, c)
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ',':
			if bare || !flush() {
				return nil, false
			}
		case unicode.IsSpace(c):
			if strings.TrimSpace(string(cur)) != "" {
				// Only the spaces around a token are no part of it.
				rest := strings.TrimSpace(string(r[i:]))
				if rest != "" && !strings.HasPrefix(rest, ",") {
					bare = true
				}
			}
			cur = append(cur, c)
		default:
			cur = append(cur, c)
		}
	}
	if bare || !flush() {
		return nil, false
	}
	return out, true
}
