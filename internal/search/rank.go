package search

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The tiers of a match: by the name as written, by the initials of its
// pinyin, by its full pinyin, and by a reading seldom used.
const (
	TierName = iota
	TierInitials
	TierFull
	TierFullRare
)

// The shapes of a match, from the best: a whole word, the start of one,
// inside one, letters scattered over the name, and not in the name at all.
const (
	ShapeWholeWord = iota
	ShapeWordStart
	ShapeInsideWord
	ShapeScattered
	ShapeNoName
)

// startCap is the furthest start that still tells results apart.
const startCap = 0x7F

// Rank is how well a query sits in a name: what orders results.
type Rank struct {
	Tier  int
	Start int
	// Coverage is the share of the name the query took, and Contiguity
	// how much in one piece: 1 for a term in one run.
	Coverage, Contiguity float64
	Shape                int
	// Matched is unset for a name the query is not in.
	Matched bool
	// joined tells of a query whose words read as one, over a span.
	joined          bool
	spanStart, span int
}

var (
	noRank          = Rank{Tier: math.MaxInt32, Start: math.MaxInt32, Shape: ShapeNoName}
	noPositiveTerms = Rank{Tier: TierName, Shape: ShapeScattered, Matched: true}
)

// NoNameRank is the rank of a result found by the folders above it: its
// name holds none of the query, or not all.
func NoNameRank(pathCoverage float64) Rank {
	return Rank{Tier: TierFull, Start: startCap, Coverage: pathCoverage, Shape: ShapeNoName, Matched: true}
}

// LeftContiguous reports a match in one piece from the name's start.
func (r Rank) LeftContiguous() bool { return r.Start == 0 && r.Contiguity >= 1 }

// Strong reports a match a user would call one at a glance.
func (r Rank) Strong() bool {
	return r.Matched && r.Tier <= TierFull && r.Shape <= ShapeInsideWord
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return min(max(v, 0), 1)
}

// Key packs a rank into a number that orders results: the smaller, the
// better.
func (r Rank) Key() uint32 {
	notLeft := uint32(1)
	if r.LeftContiguous() {
		notLeft = 0
	}
	contiguity := uint32(math.RoundToEven((1 - clamp01(r.Contiguity)) * 0xFF))
	shape := uint32(min(max(r.Shape, 0), ShapeNoName))
	start := uint32(min(max(r.Start, 0), startCap))
	coverage := uint32(math.RoundToEven((1 - clamp01(r.Coverage)) * 0x1FF))
	tier := uint32(min(max(r.Tier, 0), 0xF))
	return contiguity<<24 | shape<<21 | notLeft<<20 | start<<13 | coverage<<4 | tier
}

// The kinds of characters that tell where words start.
const (
	kindSeparator uint8 = iota
	kindLower
	kindUpper
	kindDigit
	kindUncased
)

func kindOf(c rune) uint8 {
	if isHan(c) {
		return kindUncased
	}
	switch {
	case unicode.IsDigit(c):
		return kindDigit
	case unicode.IsLower(c):
		return kindLower
	case unicode.IsUpper(c), unicode.IsTitle(c):
		return kindUpper
	case unicode.IsLetter(c):
		return kindUncased
	}
	return kindSeparator
}

// isBoundary reports whether a word may start, or end, at a place.
func isBoundary(kinds []uint8, i int, uncased bool) bool {
	if i <= 0 || i >= len(kinds) {
		return true
	}
	prev, cur := kinds[i-1], kinds[i]
	if prev == kindSeparator || cur == kindSeparator {
		return prev != cur
	}
	if prev == kindUncased || cur == kindUncased {
		if prev == kindUncased && cur == kindUncased {
			return uncased
		}
		return true
	}
	if (prev == kindDigit) != (cur == kindDigit) {
		return true
	}
	return prev == kindLower && cur == kindUpper
}

func classify(kinds []uint8, start, end int) int {
	if !isBoundary(kinds, start, false) {
		return ShapeInsideWord
	}
	if isBoundary(kinds, end, false) {
		return ShapeWholeWord
	}
	return ShapeWordStart
}

// marked is a name with the characters a query took in it.
type marked struct {
	// from maps the characters matched to those of the name as written.
	from  []int
	marks []bool
	tier  int
	shape int
	w     witness
}

// prepare makes the scratch's text the name, with what tells where its
// words start, and returns where each character came from in the name as
// written: nil when each is where it was.
func (s *Scratch) prepare(name []byte) (from []int, ascii bool) {
	t := &s.text
	ascii = t.Set(name)
	if cap(s.kinds) < len(name) {
		s.kinds = make([]uint8, 0, len(name)+32)
	}
	kinds := s.kinds[:0]
	if ascii {
		for _, b := range name {
			kinds = append(kinds, asciiKind[b])
		}
		s.kinds = kinds
		return nil, true
	}
	composed := false
	for rest := name; len(rest) > 0; {
		r, size := rune(rest[0]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRune(rest)
		}
		rest = rest[size:]
		r = foldWidth(r)
		composed = composed || composes(r)
		kinds = append(kinds, kindOf(r))
	}
	if composed {
		// Accents written apart, which few names have: the characters
		// are not where they were.
		var runes []rune
		runes, from = normalize(string(name), true)
		kinds = kinds[:0]
		for _, r := range runes {
			kinds = append(kinds, kindOf(r))
		}
	}
	s.kinds = kinds
	return from, false
}

// mark finds which characters of the prepared name the query takes, and
// by what tier and shape. w tells of a match by the query's words read as
// one.
func (p *Pattern) mark(from []int, w witness, s *Scratch) marked {
	t := &s.text
	n := len(t.Chars)
	if cap(s.marks) < n {
		s.marks = make([]bool, n, n+32)
	}
	marks := s.marks[:n]
	clear(marks)
	out := marked{from: from, marks: marks, tier: TierFullRare, shape: ShapeWholeWord, w: w}
	if w.joined {
		for i := w.start; i < w.end && i < len(marks); i++ {
			marks[i] = true
		}
		out.tier, out.shape = w.tier, ShapeWordStart
		return out
	}
	p.effective(func(set []Term) {
		setShape, positive := ShapeNoName, false
		for i := range set {
			term := &set[i]
			if term.Inverse {
				continue
			}
			positive = true
			tier, shape := p.markTerm(t, s.kinds, term, marks, s)
			out.tier = min(out.tier, tier)
			setShape = min(setShape, shape)
		}
		if positive {
			out.shape = max(out.shape, min(setShape, ShapeScattered))
		}
	})
	return out
}

// at is the character of the name as written that one matched came from.
func (m *marked) at(i int) int {
	if m.from == nil {
		return i
	}
	return m.from[i]
}

var asciiKind [128]uint8

func init() {
	for c := range 128 {
		asciiKind[c] = kindOf(rune(c))
	}
}

// markTerm marks where a term is in a name: as written, wherever it is;
// else read as pinyin; else under accents; else scattered.
func (p *Pattern) markTerm(t *Text, kinds []uint8, term *Term, marks []bool, s *Scratch) (tier, shape int) {
	shape = ShapeNoName
	found := false
	for at := 0; len(term.Text) > 0; {
		i := indexFrom(t.Chars, term.Text, at)
		if i < 0 {
			break
		}
		for k := i; k < i+len(term.Text); k++ {
			marks[k] = true
		}
		shape = min(shape, classify(kinds, i, i+len(term.Text)))
		found = true
		at = i + 1
	}
	if found {
		return TierName, shape
	}
	if term.full != nil {
		if start, end, ok := term.full.find(t.Chars); ok {
			for k := start; k < end; k++ {
				marks[k] = true
			}
			_, _, common := term.common.find(t.Chars)
			switch {
			case !common:
				return TierFullRare, ShapeWordStart
			case end-start == len(term.Text):
				return TierInitials, ShapeWordStart
			}
			return TierFull, ShapeWordStart
		}
	}
	if s.alt.foldAccents(t) {
		if i := indexFrom(s.alt.Chars, term.Text, 0); i >= 0 {
			for k := i; k < i+len(term.Text); k++ {
				marks[k] = true
			}
			return TierFull, ShapeWordStart
		}
		if term.Kind == Fuzzy && fuzzyV2(&s.alt, term.Text, &s.sl, marks).ok() {
			return TierFull, ShapeWordStart
		}
	}
	if term.Kind == Fuzzy && fuzzyV2(t, term.Text, &s.sl, marks).ok() {
		return TierName, ShapeScattered
	}
	if p.mixed != nil {
		if start, end, ok := p.mixed.find(t.Chars); ok {
			for k := start; k < end; k++ {
				marks[k] = true
			}
			return TierFull, ShapeWordStart
		}
	}
	return TierFull, ShapeNoName
}

// positiveSets counts the sets that ask for something to be there.
func (p *Pattern) positiveSets() int {
	n := 0
	p.effective(func(set []Term) {
		for i := range set {
			if !set[i].Inverse {
				n++
				break
			}
		}
	})
	return n
}

// Eval matches a name, given as it is stored, and ranks the match.
func (p *Pattern) Eval(name []byte, s *Scratch) (Rank, bool) {
	if len(name) == 0 {
		return noRank, false
	}
	// Most names do not match: only those that do are looked at closer.
	ascii := s.text.Set(name)
	if ascii {
		return p.evalASCII(name, s)
	}
	_, w, ok := p.matchText(&s.text, ascii, s)
	if !ok {
		return noRank, false
	}
	from, _ := s.prepare(name)
	return p.rankOf(p.mark(from, w, s)), true
}

// evalASCII matches and ranks a name of ASCII alone, which the scratch's
// text is made of already. Such a name has one reading, so finding where
// each term is in it tells both whether the query matches and how well:
// the name is gone over once.
func (p *Pattern) evalASCII(name []byte, s *Scratch) (Rank, bool) {
	t := &s.text
	n := len(t.Chars)
	if cap(s.marks) < n {
		s.marks = make([]bool, n, n+32)
	}
	marks := s.marks[:n]
	clear(marks)
	s.kinds = s.kinds[:0]
	m := marked{marks: marks, tier: TierFull, shape: ShapeWholeWord}
	matched := false
	if p.Groups == nil {
		matched = true
		for _, set := range p.Sets {
			if !p.directSet(set, name, s, &m) {
				// The rest need not be looked at: the name is out.
				return noRank, false
			}
		}
	} else {
		for _, g := range p.Groups {
			all := true
			for _, set := range g {
				if !p.directSet(set, name, s, &m) {
					all = false
				}
			}
			matched = matched || all
		}
	}
	if !matched {
		return noRank, false
	}
	return p.rankOf(m), true
}

// directSet finds a set's terms in a name of ASCII, marks them, and
// reports whether the set is satisfied: by the first of its terms that
// decides, as when the terms are matched one by one.
func (p *Pattern) directSet(set []Term, name []byte, s *Scratch, m *marked) bool {
	t := &s.text
	decided, satisfied, positive := false, false, false
	setShape := ShapeNoName
	for i := range set {
		term := &set[i]
		if term.Inverse {
			if !decided {
				decided, satisfied = true, !matchKind(term.Kind, t, term.Text, &s.sl, nil).ok()
			}
			continue
		}
		positive = true
		// A term that must be at a place in the name is there, or is no
		// match wherever else its text is.
		if term.Kind > Exact && !matchKind(term.Kind, t, term.Text, &s.sl, nil).ok() {
			continue
		}
		found, shape := false, ShapeNoName
		for at := 0; ; {
			i := indexFrom(t.Chars, term.Text, at)
			if i < 0 {
				break
			}
			for k := i; k < i+len(term.Text); k++ {
				m.marks[k] = true
			}
			if len(s.kinds) == 0 {
				for _, b := range name {
					s.kinds = append(s.kinds, asciiKind[b])
				}
			}
			shape = min(shape, classify(s.kinds, i, i+len(term.Text)))
			found = true
			at = i + 1
		}
		if !found && term.Kind == Fuzzy && fuzzyV2(t, term.Text, &s.sl, m.marks).ok() {
			found, shape = true, ShapeScattered
		}
		if found {
			m.tier = TierName
			setShape = min(setShape, shape)
			if !decided {
				decided, satisfied = true, true
			}
		}
	}
	if positive {
		m.shape = max(m.shape, min(setShape, ShapeScattered))
	}
	return satisfied
}

// Rank tells how well the query sits in a name: unmatched for a name it
// is not in.
func (p *Pattern) Rank(name string, s *Scratch) Rank {
	if r, ok := p.Eval(unsafeBytes(name), s); ok {
		return r
	}
	return noRank
}

// rankOf makes a rank of what was marked in a name that matches.
func (p *Pattern) rankOf(m marked) Rank {
	matched, leftmost, runs := 0, -1, 0
	var squares int64
	run := 0
	for i, on := range m.marks {
		if !on {
			if run > 0 {
				runs++
				squares += int64(run) * int64(run)
				run = 0
			}
			continue
		}
		if leftmost < 0 {
			leftmost = i
		}
		matched++
		run++
	}
	if run > 0 {
		runs++
		squares += int64(run) * int64(run)
	}
	if matched == 0 {
		// It matches, and by nothing that is there: by what it lacks.
		return noPositiveTerms
	}
	r := Rank{Tier: m.tier, Shape: m.shape, Matched: true}
	r.Coverage = float64(matched) / float64(len(m.marks))
	if n := p.posSets; n > 1 {
		r.Contiguity = min(1, float64(n)/float64(runs))
	} else {
		r.Contiguity = float64(squares) / float64(int64(matched)*int64(matched))
	}
	r.Start = m.at(leftmost)
	if m.w.joined {
		r.joined = true
		r.spanStart = m.at(m.w.start)
		r.span = m.w.end - m.w.start
	}
	return r
}

// Highlight tells which characters of a text the query takes, one for
// each character of it.
func (p *Pattern) Highlight(text string, s *Scratch) []bool {
	n := utf8.RuneCountInString(text)
	out := make([]bool, n)
	if n == 0 || p.Empty() {
		return out
	}
	from, ascii := s.prepare(unsafeBytes(text))
	var w witness
	if p.joinedFull != nil {
		_, w, _ = p.matchText(&s.text, ascii, s)
	}
	m := p.mark(from, w, s)
	for i, on := range m.marks {
		if !on {
			continue
		}
		// What was composed of several characters lights them all.
		start, end := m.at(i), n
		for k := i + 1; k < len(m.marks); k++ {
			if m.at(k) > start {
				end = m.at(k)
				break
			}
		}
		for k := start; k < end; k++ {
			out[k] = true
		}
	}
	return out
}

// Grade is how sure a match is, which orders results before all else of
// the match.
type Grade uint8

const (
	// GradeExact is a name that is the query, or reads as all of it.
	GradeExact Grade = iota + 1
	// GradeStrong is a query in the name as a word or a part of one.
	GradeStrong
	// GradeApproximate is letters scattered, or a rare reading.
	GradeApproximate
	// GradeSecondary is a query found beside the name, as in the folders
	// above it.
	GradeSecondary
	// GradeLearned is a result the user opened for this query before.
	GradeLearned
)

// Grade grades a ranked match of a name.
func (p *Pattern) Grade(name []byte, dir bool, r Rank) Grade {
	switch {
	case !r.Matched || r.Shape == ShapeNoName:
		return GradeSecondary
	case !r.Strong():
		return GradeApproximate
	case dir || r.Start != 0 || r.Contiguity < 1:
		// A name that is the query starts with it, in one piece; and a
		// folder named as the query is not what is asked for more than a
		// file is.
		return GradeStrong
	}
	stem := name
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			stem = name[:i]
			break
		}
	}
	if len(stem) == 0 {
		stem = name
	}
	if foldEqual(stem, p.ident) || foldEqual(name, p.ident) {
		return GradeExact
	}
	// A reading of the whole name from its first character is the name
	// spelled out.
	if r.Tier != TierInitials && r.Tier != TierFull || p.spaced {
		return GradeStrong
	}
	full, stemLen := utf8.RuneCount(name), utf8.RuneCount(stem)
	if int(math.Round(r.Coverage*float64(full))) != stemLen {
		return GradeStrong
	}
	return GradeExact
}

// foldEqual reports whether a name is a text in lower case, without
// case.
func foldEqual(name []byte, lower string) bool {
	if len(name) == len(lower) {
		i := 0
		for ; i < len(name); i++ {
			if c := name[i]; c >= 0x80 || asciiLower[c] != lower[i] {
				break
			}
		}
		if i == len(name) {
			return true
		}
	}
	for _, c := range name {
		if c >= 0x80 {
			return strings.EqualFold(strings.TrimSpace(Normalize(string(name))), lower)
		}
	}
	return false
}

// CompareNames orders names for results that match as well as each other:
// numbers by their value, digits before letters before the rest.
func CompareNames(x, y string) int {
	// Names in ASCII, which most are, a byte at a time.
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		cx, cy := x[i], y[j]
		if cx >= 0x80 || cy >= 0x80 {
			return compareNamesFrom(x, y, i, j)
		}
		if cx-'0' < 10 && cy-'0' < 10 {
			si, sj := i, j
			for i < len(x) && x[i]-'0' < 10 {
				i++
			}
			for j < len(y) && y[j]-'0' < 10 {
				j++
			}
			if i < len(x) && x[i] >= 0x80 || j < len(y) && y[j] >= 0x80 {
				// The number may go on in digits of other scripts.
				return compareNamesFrom(x, y, si, sj)
			}
			for si < i && x[si] == '0' {
				si++
			}
			for sj < j && y[sj] == '0' {
				sj++
			}
			if i-si != j-sj {
				return cmpInt(i-si, j-sj)
			}
			if c := strings.Compare(x[si:i], y[sj:j]); c != 0 {
				return c
			}
			continue
		}
		if cx != cy {
			if kx, ky := orderClass(rune(cx)), orderClass(rune(cy)); kx != ky {
				return cmpInt(kx, ky)
			}
			return cmpInt(int(cx), int(cy))
		}
		i++
		j++
	}
	return cmpInt(len(x)-i, len(y)-j)
}

// compareNamesFrom is CompareNames from a place in each name on, for
// names in any script.
func compareNamesFrom(x, y string, i, j int) int {
	for i < len(x) && j < len(y) {
		cx, sx := rune(x[i]), 1
		if cx >= 0x80 {
			cx, sx = utf8.DecodeRuneInString(x[i:])
		}
		cy, sy := rune(y[j]), 1
		if cy >= 0x80 {
			cy, sy = utf8.DecodeRuneInString(y[j:])
		}
		if isDigit(cx) && isDigit(cy) {
			si := i
			for i < len(x) {
				c, size := utf8.DecodeRuneInString(x[i:])
				if !isDigit(c) {
					break
				}
				i += size
			}
			sj := j
			for j < len(y) {
				c, size := utf8.DecodeRuneInString(y[j:])
				if !isDigit(c) {
					break
				}
				j += size
			}
			nx := strings.TrimLeft(x[si:i], "0")
			ny := strings.TrimLeft(y[sj:j], "0")
			if len(nx) != len(ny) {
				return cmpInt(len(nx), len(ny))
			}
			if c := strings.Compare(nx, ny); c != 0 {
				return c
			}
			continue
		}
		if cx != cy {
			if kx, ky := orderClass(cx), orderClass(cy); kx != ky {
				return cmpInt(kx, ky)
			}
			return cmpInt(int(cx), int(cy))
		}
		i += sx
		j += sy
	}
	return cmpInt(len(x)-i, len(y)-j)
}

func isDigit(c rune) bool {
	if c < 0x80 {
		return c >= '0' && c <= '9'
	}
	return unicode.IsDigit(c)
}

func orderClass(c rune) int {
	if c < 0x80 {
		switch asciiClass[c] {
		case clsNumber:
			return 0
		case clsLower, clsUpper:
			return 1
		}
		return 2
	}
	switch {
	case unicode.IsDigit(c):
		return 0
	case unicode.IsLetter(c):
		return 1
	}
	return 2
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
