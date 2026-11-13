package search

import (
	"strings"
	"unicode"
	"unsafe"
)

// Options are how the user has queries read.
type Options struct {
	// Fuzzy lets a bare term match its characters scattered in a name;
	// without it a bare term is taken as it is, and ' asks for scattered.
	Fuzzy bool
	// OrFirst makes | bind tighter than the space, as Lertaro once did:
	// "a | b c" is then (a or b) and c, where it otherwise is a or (b and c).
	OrFirst bool
	// Pinyin reads Chinese names by their pinyin.
	Pinyin bool
}

// DefaultOptions are those of a first run.
func DefaultOptions() Options { return Options{Fuzzy: true, Pinyin: true} }

// Term is one word of a query.
type Term struct {
	Kind    Kind
	Inverse bool
	// Text is the word in lower case.
	Text []rune
	// full and common read the word as pinyin, by every reading of a
	// character and by its two commonest; nil for a word pinyin cannot
	// read.
	full, common *reader
}

func (t *Term) plain() bool { return t.Kind == Exact || t.Kind == Fuzzy }

// Pattern is a query's terms: what a name must hold to match.
type Pattern struct {
	// Drive is the letter of the only drive asked for, in lower case, 0
	// for any.
	Drive rune
	// Sets must all match, each by one of its terms. Groups, when the
	// query mixes | and spaces with the space binding tighter, are the
	// alternatives instead: one of them must match by all its sets.
	Sets   [][]Term
	Groups [][][]Term

	opts       Options
	exclusions bool
	// live is set when pinyin can read some word of the query: names
	// that are not ASCII are then matched by their readings.
	live bool
	// lone is the query's only word, when it has one and no more.
	lone *Term
	// joined reads the query's words as one, as "sousuo jieguo" for
	// 搜索结果.
	joinedFull, joinedCommon *reader
	joinedLen                int
	mixed                    *reader
	positives                int
	posSets                  int
	termLen                  int
	filters                  []filter
	// quick are the terms a name of ASCII alone must hold, for names to
	// be turned away before they are made ready to match.
	quick []quickTerm
	// ident is the query in lower case, as a name that is it is told,
	// and spaced whether it holds a space.
	ident  string
	spaced bool
}

// filter is what a name's mask must hold for a group of sets to match it.
type filter struct {
	required uint64
	any      [][]uint64
}

// Empty reports a pattern without terms.
func (p *Pattern) Empty() bool { return len(p.Sets) == 0 }

// effective are the sets every use of the pattern's terms goes through.
func (p *Pattern) effective(fn func(set []Term)) {
	if p.Groups == nil {
		for _, set := range p.Sets {
			fn(set)
		}
		return
	}
	for _, g := range p.Groups {
		for _, set := range g {
			fn(set)
		}
	}
}

// Parse reads a query's terms, with the drive a term as "d:" asks for.
func Parse(query string, opts Options) *Pattern {
	query = Normalize(query)
	var drive rune
	var terms []string
	for _, raw := range strings.Fields(query) {
		r := []rune(raw)
		if len(r) >= 2 && unicode.IsLetter(r[0]) && r[1] == ':' {
			drive = unicode.ToLower(r[0])
			if len(r) > 2 {
				terms = append(terms, string(r[2:]))
			}
			continue
		}
		terms = append(terms, raw)
	}
	return build(drive, strings.Join(terms, " "), opts)
}

// ParseText reads a query's terms, taking none for a drive.
func ParseText(query string, opts Options) *Pattern {
	return build(0, Normalize(query), opts)
}

func build(drive rune, query string, opts Options) *Pattern {
	p := &Pattern{Drive: drive, opts: opts}
	p.ident = strings.ToLower(strings.TrimSpace(query))
	p.spaced = strings.ContainsFunc(p.ident, unicode.IsSpace)
	tokens := mergeQuoted(strings.Fields(strings.ReplaceAll(query, `\ `, "\x01")))
	for i, tok := range tokens {
		tokens[i] = strings.ReplaceAll(tok, "\x01", " ")
	}
	p.Sets = parseSets(tokens, opts)
	if !opts.OrFirst {
		if groups, both := parseGroups(tokens, opts); both {
			p.Groups = groups
		}
	}
	p.finish()
	return p
}

// parseSets reads the terms with | binding tighter than the space.
func parseSets(tokens []string, opts Options) [][]Term {
	var sets [][]Term
	var current []Term
	afterBar := false
	for _, tok := range tokens {
		if len(current) > 0 && !afterBar && tok == "|" {
			afterBar = true
			continue
		}
		if len(current) > 0 && !afterBar {
			sets = append(sets, current)
			current = nil
		}
		afterBar = false
		current = addToken(tok, current, opts)
	}
	if len(current) > 0 {
		sets = append(sets, current)
	}
	return sets
}

// parseGroups reads the terms with the space binding tighter than |, and
// reports whether the query has both.
func parseGroups(tokens []string, opts Options) (groups [][][]Term, both bool) {
	var group [][]Term
	var set []Term
	space, pipe := false, false
	for _, tok := range tokens {
		if tok == "|" {
			pipe = true
			if len(set) > 0 {
				group = append(group, set)
				set = nil
			}
			if len(group) > 0 {
				groups = append(groups, group)
				group = nil
			}
			continue
		}
		if len(set) > 0 {
			space = true
			group = append(group, set)
			set = nil
		}
		set = addToken(tok, set, opts)
	}
	if len(set) > 0 {
		group = append(group, set)
	}
	if len(group) > 0 {
		groups = append(groups, group)
	}
	return groups, space && pipe
}

// addToken reads a term's marks: ! leaves out, ' flips between scattered
// and as it is, '…' asks for whole words, ^ for the start and $ for the
// end.
func addToken(tok string, current []Term, opts Options) []Term {
	kind := Exact
	if opts.Fuzzy {
		kind = Fuzzy
	}
	inverse := false
	if strings.HasPrefix(tok, "!") {
		inverse, kind = true, Exact
		tok = tok[1:]
	}
	if tok != "$" && strings.HasSuffix(tok, "$") {
		kind = Suffix
		tok = tok[:len(tok)-1]
	}
	switch {
	case len(tok) > 2 && strings.HasPrefix(tok, "'") && strings.HasSuffix(tok, "'"):
		kind = Boundary
		tok = tok[1 : len(tok)-1]
	case strings.HasPrefix(tok, "'"):
		if kind != Suffix {
			if opts.Fuzzy && !inverse {
				kind = Exact
			} else {
				kind = Fuzzy
			}
		}
		tok = tok[1:]
	case strings.HasPrefix(tok, "^"):
		if kind == Suffix {
			kind = Equal
		} else {
			kind = Prefix
		}
		tok = strings.TrimPrefix(tok[1:], "'")
	}
	if tok == "" {
		return current
	}
	text := []rune(tok)
	for i, c := range text {
		text[i] = lowerOf(c)
	}
	return append(current, Term{Kind: kind, Inverse: inverse, Text: text})
}

// mergeQuoted joins the tokens of a phrase in single quotes into one.
func mergeQuoted(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		open := -1
		if strings.HasPrefix(tok, "'") {
			open = 0
		} else if strings.HasPrefix(tok, "!'") {
			open = 1
		}
		if open < 0 || len(tok) > open+2 && strings.HasSuffix(tok, "'") {
			out = append(out, tok)
			continue
		}
		closing := -1
		for j := i + 1; j < len(tokens); j++ {
			if tokens[j] == "|" {
				break
			}
			if strings.HasSuffix(tokens[j], "'") {
				closing = j
				break
			}
		}
		if closing < 0 {
			out = append(out, tok)
			continue
		}
		out = append(out, strings.Join(tokens[i:closing+1], " "))
		i = closing
	}
	return out
}

// finish works out what matching needs of the terms, once.
func (p *Pattern) finish() {
	plainCount := 0
	var firstPlain *Term
	each := func(sets [][]Term) {
		for si := range sets {
			for ti := range sets[si] {
				t := &sets[si][ti]
				if t.Inverse {
					p.exclusions = true
					continue
				}
				p.positives++
				if !t.plain() {
					continue
				}
				plainCount++
				if firstPlain == nil {
					firstPlain = t
				}
				if p.opts.Pinyin && isWord(t.Text) {
					t.full, t.common = newReader(t.Text, false), newReader(t.Text, true)
					p.live = true
				}
			}
		}
	}
	if p.Groups == nil {
		each(p.Sets)
	} else {
		// The groups hold the terms that are matched; the flat sets are
		// kept for what asks how the query was typed.
		for _, g := range p.Groups {
			each(g)
		}
		for si := range p.Sets {
			for ti := range p.Sets[si] {
				if t := &p.Sets[si][ti]; !t.Inverse && t.plain() && p.opts.Pinyin && isWord(t.Text) {
					t.full, t.common = newReader(t.Text, false), newReader(t.Text, true)
				}
			}
		}
	}
	if p.positives == 1 && plainCount == 1 {
		p.lone = firstPlain
		if p.opts.Pinyin && p.opts.Fuzzy && firstPlain.Kind == Fuzzy && isMixed(firstPlain.Text) {
			p.mixed = newReader(firstPlain.Text, false)
		}
	}
	if p.opts.Pinyin {
		if joined := p.joinedTerm(); joined != nil {
			p.joinedFull, p.joinedCommon = newReader(joined, false), newReader(joined, true)
			p.joinedLen = len(joined)
			p.live = true
		}
	}
	p.termLen = p.totalTermLength()
	p.filters = p.buildFilters()
	p.posSets = p.positiveSets()
	if p.Groups == nil {
		for _, set := range p.Sets {
			if len(set) != 1 || set[0].Inverse {
				continue
			}
			q := quickTerm{kind: set[0].Kind}
			for _, c := range set[0].Text {
				if c >= 0x80 {
					q.text = nil
					break
				}
				q.text = append(q.text, byte(c))
			}
			if len(q.text) > 0 {
				p.quick = append(p.quick, q)
			}
		}
	}
}

// quickTerm is a term of ASCII: its text in lower case, and whether it
// must be in a name in one piece.
type quickTerm struct {
	text []byte
	kind Kind
}

// Quick reports whether a name may match: false for a name of ASCII
// alone that lacks a term, told by reading its bytes as they are. A name
// that is not ASCII may always match, by another reading of it.
func (p *Pattern) Quick(name []byte) bool {
	for i := range p.quick {
		q := &p.quick[i]
		ok := false
		switch q.kind {
		case Exact, Boundary:
			ok = holdsFold(name, q.text)
		case Prefix:
			ok = len(name) >= len(q.text) && (holdsFold(name[:len(q.text)], q.text) || name[0] == ' ')
		case Suffix:
			ok = len(name) >= len(q.text) && (holdsFold(name[len(name)-len(q.text):], q.text) || name[len(name)-1] == ' ')
		case Equal:
			ok = len(name) == len(q.text) && holdsFold(name, q.text) || len(name) > 0 && (name[0] == ' ' || name[len(name)-1] == ' ')
		default:
			at := 0
			for _, b := range name {
				if asciiLower[b&0x7F] == q.text[at] && b < 0x80 {
					at++
					if at == len(q.text) {
						ok = true
						break
					}
				}
			}
		}
		if !ok {
			for _, b := range name {
				if b >= 0x80 {
					return true
				}
			}
			return false
		}
	}
	return true
}

// holdsFold reports whether a name holds a text in lower case, without
// case.
func holdsFold(name, text []byte) bool {
	first := text[0]
	for i := 0; i+len(text) <= len(name); i++ {
		if b := name[i]; b >= 0x80 || asciiLower[b] != first {
			continue
		}
		j := 1
		for ; j < len(text); j++ {
			if b := name[i+j]; b >= 0x80 || asciiLower[b] != text[j] {
				break
			}
		}
		if j == len(text) {
			return true
		}
	}
	return false
}

// joinedTerm is the query's words written as one, when they are plain
// words of letters that pinyin can read each: nil otherwise.
func (p *Pattern) joinedTerm() []rune {
	if p.Drive != 0 || p.Groups != nil || p.exclusions || len(p.Sets) < 2 {
		return nil
	}
	plain := Exact
	if p.opts.Fuzzy {
		plain = Fuzzy
	}
	var joined []rune
	for _, set := range p.Sets {
		if len(set) != 1 || set[0].Inverse || set[0].Kind != plain || set[0].full == nil {
			return nil
		}
		for _, c := range set[0].Text {
			if c < 'a' || c > 'z' {
				return nil
			}
			joined = append(joined, c)
		}
		if len(joined) > readerMax {
			return nil
		}
	}
	if len(joined) < 2 {
		return nil
	}
	return joined
}

func firstPositiveLen(set []Term) int {
	for i := range set {
		if !set[i].Inverse {
			return len(set[i].Text)
		}
	}
	return 0
}

func (p *Pattern) totalTermLength() int {
	sum := func(sets [][]Term) int {
		n := 0
		for _, set := range sets {
			n += firstPositiveLen(set)
		}
		return n
	}
	if p.Groups == nil {
		return sum(p.Sets)
	}
	longest := 0
	for _, g := range p.Groups {
		longest = max(longest, sum(g))
	}
	return longest
}

func (p *Pattern) buildFilters() []filter {
	of := func(sets [][]Term) filter {
		var f filter
		for _, set := range sets {
			inverse := false
			for i := range set {
				inverse = inverse || set[i].Inverse
			}
			if inverse {
				// What must be absent cannot be told from a mask.
				continue
			}
			if len(set) == 1 {
				f.required |= maskOf(set[0].Text)
				continue
			}
			masks := make([]uint64, len(set))
			for i := range set {
				masks[i] = maskOf(set[i].Text)
			}
			f.any = append(f.any, masks)
		}
		return f
	}
	var out []filter
	if p.Groups == nil {
		out = []filter{of(p.Sets)}
	} else {
		for _, g := range p.Groups {
			out = append(out, of(g))
		}
	}
	for _, f := range out {
		if f.required == 0 && len(f.any) == 0 {
			// A group anything may match: no name is left out.
			return nil
		}
	}
	return out
}

// Split is the pattern's sets as patterns of their own, for a query of
// several words that must all be there: nil for any other.
func (p *Pattern) Split() []*Pattern {
	if p.Groups != nil || len(p.Sets) < 2 || len(p.Sets) > 16 {
		return nil
	}
	out := make([]*Pattern, len(p.Sets))
	for i, set := range p.Sets {
		terms := make([]Term, len(set))
		for j := range set {
			terms[j] = Term{Kind: set[j].Kind, Inverse: set[j].Inverse, Text: set[j].Text}
		}
		sub := &Pattern{Sets: [][]Term{terms}, opts: p.opts}
		sub.finish()
		out[i] = sub
	}
	return out
}

// Options are those the pattern was read with.
func (p *Pattern) Options() Options { return p.opts }

// Positive reports whether the pattern asks for something to be there,
// not only for things to be absent.
func (p *Pattern) Positive() bool { return p.positives > 0 }

// TermLength is how many characters the query's words have.
func (p *Pattern) TermLength() int { return p.termLen }

// Admits reports whether a name of this mask can match: false tells it
// cannot, without the name being read.
func (p *Pattern) Admits(mask uint64) bool {
	if p.filters == nil {
		return true
	}
next:
	for i := range p.filters {
		f := &p.filters[i]
		if mask&f.required != f.required {
			continue
		}
		for _, masks := range f.any {
			ok := false
			for _, m := range masks {
				if mask&m == m {
					ok = true
					break
				}
			}
			if !ok {
				continue next
			}
		}
		return true
	}
	return false
}

// Required is the mask every matching name has, when the pattern is one
// group that asks for one: scanning tests it before all else.
func (p *Pattern) Required() (mask uint64, only bool) {
	if len(p.filters) != 1 {
		return 0, false
	}
	return p.filters[0].required, len(p.filters[0].any) == 0
}

// Match is how a pattern matched a name.
type Match struct {
	Score int
	// Begin is where the leftmost term's match starts and End where the
	// rightmost ends, in characters; Valid is unset when no term took any.
	Begin, End int
	Valid      bool
}

// witness tells of a match by the query's words read as one.
type witness struct {
	joined     bool
	start, end int
	tier       int
}

// Scratch is the memory one goroutine matches in.
type Scratch struct {
	text, alt Text
	sl        slab
	marks     []bool
	kinds     []uint8
}

// Match matches a name, given as it is stored.
func (p *Pattern) Match(name []byte, s *Scratch) (Match, bool) {
	ascii := s.text.Set(name)
	m, _, ok := p.matchText(&s.text, ascii, s)
	return m, ok
}

// MatchString is Match for a string.
func (p *Pattern) MatchString(name string, s *Scratch) (Match, bool) {
	return p.Match(unsafeBytes(name), s)
}

func unsafeBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

func (p *Pattern) matchText(t *Text, ascii bool, s *Scratch) (Match, witness, bool) {
	if !ascii && p.live {
		if m, w, ok := p.scoreLive(t, s); ok {
			return m, w, true
		}
	} else if m, ok := p.tryMatch(t, s); ok {
		return m, witness{}, true
	}
	if ascii {
		return Match{}, witness{}, false
	}
	// What follows reads the name another way, and what was left out by
	// name stays out.
	if p.exclusions && !p.exclusionsPass(t, s) {
		return Match{}, witness{}, false
	}
	if m, ok := p.matchAccents(t, s); ok {
		return m, witness{}, true
	}
	if p.mixed != nil {
		if start, end, ok := p.mixed.find(t.Chars); ok {
			stem := stemLength(t.Chars)
			score := pinyinBonus(t, start, true) + 25*min(end-start, stem)/stem
			return Match{Score: score, Begin: start, End: end, Valid: true}, witness{}, true
		}
	}
	return Match{}, witness{}, false
}

type accum struct {
	score      int
	begin, end int
	valid      bool
}

func (a *accum) add(sp span) {
	a.score += sp.score
	a.at(sp.start, sp.end)
}

func (a *accum) at(start, end int) {
	if start >= end {
		return
	}
	if !a.valid || start < a.begin {
		a.begin = start
	}
	if end > a.end {
		a.end = end
	}
	a.valid = true
}

func (a *accum) match() Match {
	return Match{Score: a.score, Begin: a.begin, End: a.end, Valid: a.valid}
}

// tryMatch matches the terms as they are written.
func (p *Pattern) tryMatch(t *Text, s *Scratch) (Match, bool) {
	if p.Groups != nil {
		for _, g := range p.Groups {
			if m, ok := matchSets(g, t, s); ok {
				return m, true
			}
		}
		return Match{}, false
	}
	return matchSets(p.Sets, t, s)
}

func matchSets(sets [][]Term, t *Text, s *Scratch) (Match, bool) {
	var a accum
	for _, set := range sets {
		sp, ok := matchSet(set, t, s)
		if !ok {
			return Match{}, false
		}
		a.add(sp)
	}
	return a.match(), true
}

// matchSet matches a set by the first of its terms that does. A term to
// leave out that is there fails the set, and one that is not satisfies it.
func matchSet(set []Term, t *Text, s *Scratch) (span, bool) {
	for i := range set {
		term := &set[i]
		sp := matchKind(term.Kind, t, term.Text, &s.sl, nil)
		if sp.ok() {
			if term.Inverse {
				return span{}, false
			}
			return sp, true
		}
		if term.Inverse {
			return span{}, true
		}
	}
	return span{}, false
}

func (p *Pattern) exclusionsPass(t *Text, s *Scratch) bool {
	pass := true
	p.effective(func(set []Term) {
		for i := range set {
			if term := &set[i]; term.Inverse && matchKind(term.Kind, t, term.Text, &s.sl, nil).ok() {
				pass = false
			}
		}
	})
	return pass
}

// matchAccents matches the name with its accented letters taken for the
// letters they are written over, so that "cancion" finds Canción. Such a
// match must be tight: the letters it takes near each other.
func (p *Pattern) matchAccents(t *Text, s *Scratch) (Match, bool) {
	if !s.alt.foldAccents(t) {
		return Match{}, false
	}
	m, ok := p.tryMatch(&s.alt, s)
	if !ok || !m.Valid {
		return Match{}, false
	}
	n := p.termLen
	if width := m.End - m.Begin; width > max(4*n, 8) || m.Score < 5*n {
		return Match{}, false
	} else if width > n && n > 0 {
		m.Score = m.Score * n / width
	}
	return m, true
}

// foldAccents makes a the name of t with accented letters as the letters
// they are written over, and reports whether it has any.
func (a *Text) foldAccents(t *Text) bool {
	found := false
	a.Chars = append(a.Chars[:0], t.Chars...)
	a.Cls = append(a.Cls[:0], t.Cls...)
	for i, c := range a.Chars {
		if base := baseLetter(c); base != 0 {
			a.Chars[i] = base
			found = true
		}
	}
	return found
}

// stemLength is how many of a name's characters come before its
// extension.
func stemLength(chars []rune) int {
	for i := len(chars) - 1; i > 0; i-- {
		if chars[i] == '.' {
			if i < len(chars)-1 {
				return i
			}
			break
		}
	}
	return len(chars)
}

// wordEdge reports whether a word may start at a place of a name: at
// its ends, beside a separator, where letters meet digits or a script
// without case, and at a capital after a small letter.
func wordEdge(t *Text, i int) bool {
	if i <= 0 || i >= len(t.Cls) {
		return true
	}
	kind := func(cls uint8) uint8 {
		if cls <= clsDelimiter {
			return 0
		}
		return cls
	}
	prev, cur := kind(t.Cls[i-1]), kind(t.Cls[i])
	if prev == 0 || cur == 0 {
		return prev != cur
	}
	if prev == clsLetter || cur == clsLetter {
		return true
	}
	if (prev == clsNumber) != (cur == clsNumber) {
		return true
	}
	return prev == clsLower && cur == clsUpper
}

func pinyinBonus(t *Text, start int, common bool) int {
	bonus := 4
	switch {
	case start == 0:
		bonus = 26
	case wordEdge(t, start):
		bonus = 16
	}
	if !common {
		bonus -= 12
	}
	return bonus
}

// scoreLive matches a name that is not ASCII with the query's words read
// as pinyin where they are not in the name as written.
func (p *Pattern) scoreLive(t *Text, s *Scratch) (Match, witness, bool) {
	var m Match
	var readingOnly bool
	var covered int
	matched := false
	if p.Groups != nil {
		for _, g := range p.Groups {
			if m, readingOnly, covered, matched = p.scoreSets(g, t, s); matched {
				break
			}
		}
	} else {
		m, readingOnly, covered, matched = p.scoreSets(p.Sets, t, s)
	}
	if p.joinedFull != nil {
		if start, end, ok := p.joinedFull.find(t.Chars); ok {
			_, _, common := p.joinedCommon.find(t.Chars)
			stem := stemLength(t.Chars)
			score := pinyinBonus(t, start, common) + 25*min(end-start, stem)/stem
			if start == 0 && end-start >= stem {
				score += 40
			}
			wins := !matched || score > m.Score
			if !wins && readingOnly && score == m.Score {
				wins = start <= m.Begin && min(end-start, stem) >= min(covered, stem)
			}
			if wins {
				tier := TierFull
				switch {
				case !common:
					tier = TierFullRare
				case end-start == p.joinedLen:
					tier = TierInitials
				}
				return Match{Score: score, Begin: start, End: end, Valid: true},
					witness{joined: true, start: start, end: end, tier: tier}, true
			}
		}
	}
	return m, witness{}, matched
}

func (p *Pattern) scoreSets(sets [][]Term, t *Text, s *Scratch) (m Match, readingOnly bool, covered int, ok bool) {
	var a accum
	literals, reads := 0, 0
	loneStart, loneEnd := -1, -1
	for _, set := range sets {
		satisfied := false
		for i := range set {
			term := &set[i]
			readable := term.full != nil
			kind := term.Kind
			if readable && kind == Fuzzy {
				// A word pinyin can read is not scattered over a name
				// that is not ASCII: it is there as written, or read.
				kind = Exact
			}
			if sp := matchKind(kind, t, term.Text, &s.sl, nil); sp.ok() {
				if term.Inverse {
					return Match{}, false, 0, false
				}
				a.add(sp)
				covered += len(term.Text)
				literals++
				satisfied = true
				break
			}
			if term.Inverse {
				satisfied = true
				break
			}
			if !readable {
				continue
			}
			start, end, found := term.full.find(t.Chars)
			if !found {
				continue
			}
			_, _, common := term.common.find(t.Chars)
			a.score += pinyinBonus(t, start, common)
			a.at(start, end)
			covered += end - start
			if term == p.lone || p.lone != nil && term.Kind == p.lone.Kind && string(term.Text) == string(p.lone.Text) {
				loneStart, loneEnd = start, end
			}
			reads++
			satisfied = true
			break
		}
		if !satisfied {
			return Match{}, false, 0, false
		}
	}
	if literals == 0 {
		stem := stemLength(t.Chars)
		if reads > 0 {
			a.score += 25 * min(covered, stem) / stem
		}
		if p.lone != nil && loneStart == 0 && loneEnd-loneStart >= stem {
			// One word that spelled the whole name, from its first
			// character.
			a.score += 40
		}
	}
	return a.match(), literals == 0 && reads > 0, covered, true
}
