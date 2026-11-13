// Package search matches file names against what the user typed, and ranks
// what matched. It knows nothing of disks or of the interface.
//
// The grammar, the scores and the order of results follow Lertaro's
// (https://github.com/Lertaro/Lertaro, MIT), whose matcher descends from
// fzf's: the files here are that code read and written again in Go, so
// that a query means in MyFiles what it means there.
package search

import "unicode"

// Kind is how a term's text must sit in a name.
type Kind uint8

const (
	// Fuzzy takes the text's characters in order, anywhere in the name.
	Fuzzy Kind = iota
	// Exact takes the text as it is, anywhere.
	Exact
	// Boundary takes it as it is, between two ends of words.
	Boundary
	// Prefix and Suffix take it at the start and at the end of the name,
	// and Equal as the whole name.
	Prefix
	Suffix
	Equal
)

// The scores of fzf, which Lertaro keeps.
const (
	scoreMatch        = 16
	scoreGapStart     = -3
	scoreGapExtension = -1
	bonusBoundary     = scoreMatch / 2
	bonusNonWord      = scoreMatch / 2
	bonusCamel123     = bonusBoundary + scoreGapExtension
	bonusConsecutive  = -(scoreGapStart + scoreGapExtension)
	bonusFirstChar    = 2
	bonusWhite        = bonusBoundary + 2
	bonusDelimiter    = bonusBoundary + 1
	// maxCells is the most cells the table of the full algorithm takes:
	// past it, the greedy one scores.
	maxCells = 250_000
)

// The classes of characters, in fzf's order: what a character is decides
// what starting a match on it, or after it, is worth.
const (
	clsWhite uint8 = iota
	clsNonWord
	clsDelimiter
	clsLower
	clsUpper
	clsLetter
	clsNumber
)

var (
	asciiClass [128]uint8
	asciiLower [128]byte
	// bonusOf is what a character of a class after one of another is
	// worth, by (previous<<3 | current).
	bonusOf [64]int16
)

func init() {
	for c := range 128 {
		asciiClass[c] = classOfRune(rune(c))
		asciiLower[c] = byte(c)
		if c >= 'A' && c <= 'Z' {
			asciiLower[c] = byte(c + 32)
		}
	}
	for prev := range uint8(7) {
		for cur := range uint8(7) {
			bonusOf[prev<<3|cur] = bonusFor(prev, cur)
		}
	}
}

func bonusFor(prev, cur uint8) int16 {
	if cur >= clsNonWord {
		switch prev {
		case clsWhite:
			return bonusWhite
		case clsDelimiter:
			return bonusDelimiter
		case clsNonWord:
			return bonusBoundary
		}
	}
	if prev == clsLower && cur == clsUpper || prev != clsNumber && cur == clsNumber {
		return bonusCamel123
	}
	switch cur {
	case clsNonWord, clsDelimiter:
		return bonusNonWord
	case clsWhite:
		return bonusWhite
	}
	return 0
}

func classOfRune(c rune) uint8 {
	switch {
	case c >= 'a' && c <= 'z':
		return clsLower
	case c >= 'A' && c <= 'Z':
		return clsUpper
	case c >= '0' && c <= '9':
		return clsNumber
	case unicode.IsSpace(c):
		return clsWhite
	case c == '\\' || c == '/' || c == ':' || c == ';' || c == ',' || c == '|':
		return clsDelimiter
	case unicode.IsLetter(c):
		return clsLetter
	}
	return clsNonWord
}

func classOf(c rune) uint8 {
	if c < 128 {
		return asciiClass[c]
	}
	if isHan(c) {
		return clsLetter
	}
	return classOfRune(c)
}

// lowerOf is a character without its case.
func lowerOf(c rune) rune {
	if c < 128 {
		return rune(asciiLower[c])
	}
	if isHan(c) {
		return c
	}
	return unicode.ToLower(c)
}

// Text is a name made ready to match: its characters without case, and
// what each was.
type Text struct {
	// Chars are the characters in lower case, wide forms folded, and Cls
	// the class of each as the name has it.
	Chars []rune
	Cls   []uint8
}

// span is where a term matched and what the match is worth. A term that
// did not match has start < 0.
type span struct {
	start, end, score int
}

var noSpan = span{-1, -1, 0}

func (s span) ok() bool { return s.start >= 0 }

// slab is the memory one worker matches in, kept between names.
type slab struct {
	bonus  []int16
	first  []int32
	scores []int16
	consec []int16
}

func grow16(s []int16, n int) []int16 {
	if cap(s) < n {
		return make([]int16, n, n+n/2+16)
	}
	return s[:n]
}

// matchKind matches one term's text in a name. marks, where given, is
// set at the characters a fuzzy match took.
func matchKind(kind Kind, t *Text, pat []rune, sl *slab, marks []bool) span {
	switch kind {
	case Fuzzy:
		return fuzzyV2(t, pat, sl, marks)
	case Exact:
		return exactMatch(t, pat, false)
	case Boundary:
		return exactMatch(t, pat, true)
	case Prefix:
		return prefixMatch(t, pat)
	case Suffix:
		return suffixMatch(t, pat)
	case Equal:
		return equalMatch(t, pat)
	}
	return noSpan
}

// fuzzyScope finds the part of the name a fuzzy match can lie in: from
// the character before the first of the pattern's to the last occurrence
// of its last.
func fuzzyScope(chars, pat []rune) (start, end int, ok bool) {
	at := 0
	start = -1
	for i, target := range pat {
		found := -1
		for j := at; j < len(chars); j++ {
			if chars[j] == target {
				found = j
				break
			}
		}
		if found < 0 {
			return 0, 0, false
		}
		if i == 0 {
			start = max(0, found-1)
		}
		at = found + 1
	}
	end = at
	last := pat[len(pat)-1]
	for j := len(chars) - 1; j >= end; j-- {
		if chars[j] == last {
			end = j + 1
			break
		}
	}
	return start, end, true
}

// calculateScore scores a match found greedily in chars[start:end].
func calculateScore(t *Text, pat []rune, start, end int) int {
	pi, score := 0, 0
	inGap := false
	consecutive := 0
	firstBonus := int16(0)
	prev := clsWhite
	if start > 0 {
		prev = t.Cls[start-1]
	}
	for i := start; i < end; i++ {
		cur := t.Cls[i]
		if pi < len(pat) && t.Chars[i] == pat[pi] {
			bonus := bonusOf[prev<<3|cur]
			score += scoreMatch
			if consecutive == 0 {
				firstBonus = bonus
			} else {
				if bonus >= bonusBoundary && bonus > firstBonus {
					firstBonus = bonus
				}
				bonus = max(bonus, firstBonus, bonusConsecutive)
			}
			if pi == 0 {
				score += int(bonus) * bonusFirstChar
			} else {
				score += int(bonus)
			}
			pi++
			consecutive++
			inGap = false
		} else {
			if inGap {
				score += scoreGapExtension
			} else {
				score += scoreGapStart
			}
			inGap = true
			consecutive = 0
			firstBonus = 0
		}
		prev = cur
	}
	if pi != len(pat) {
		return -1
	}
	return score
}

// fuzzySingle matches a pattern of one character: its first occurrence at
// the start of a word, else the best placed.
func fuzzySingle(t *Text, c rune, marks []bool) span {
	bestScore, bestPos := 0, -1
	prev := clsWhite
	for i, ch := range t.Chars {
		cur := t.Cls[i]
		bonus := bonusOf[prev<<3|cur]
		prev = cur
		if ch != c {
			continue
		}
		score := scoreMatch + int(bonus)*bonusFirstChar
		if score <= bestScore {
			continue
		}
		bestScore, bestPos = score, i
		if bonus >= bonusBoundary {
			break
		}
	}
	if bestPos < 0 {
		return noSpan
	}
	if marks != nil {
		marks[bestPos] = true
	}
	return span{bestPos, bestPos + 1, bestScore}
}

// fuzzyV1 is the greedy match: the first occurrence going forward, shrunk
// going back.
func fuzzyV1(t *Text, pat []rune, marks []bool) span {
	start, end, ok := fuzzyScope(t.Chars, pat)
	if !ok {
		return noSpan
	}
	pi := len(pat) - 1
	shrunk := start
	for i := end - 1; i >= start; i-- {
		if t.Chars[i] == pat[pi] {
			pi--
			if pi < 0 {
				shrunk = i
				break
			}
		}
	}
	if marks != nil {
		pi = 0
		for i := shrunk; i < end && pi < len(pat); i++ {
			if t.Chars[i] == pat[pi] {
				marks[i] = true
				pi++
			}
		}
	}
	return span{shrunk, end, calculateScore(t, pat, shrunk, end)}
}

// fuzzyV2 is fzf's full algorithm: of all the ways the pattern's
// characters lie in the name in order, the one worth most.
func fuzzyV2(t *Text, pat []rune, sl *slab, marks []bool) span {
	m, n := len(pat), len(t.Chars)
	if m == 0 {
		return span{0, 0, 0}
	}
	if m > n {
		return noSpan
	}
	if m == 1 {
		return fuzzySingle(t, pat[0], marks)
	}
	minIdx, maxIdx, ok := fuzzyScope(t.Chars, pat)
	if !ok {
		return noSpan
	}
	scoped := maxIdx - minIdx
	if m > 1000 || scoped*m > maxCells {
		return fuzzyV1(t, pat, marks)
	}
	chars := t.Chars[minIdx:maxIdx]
	sl.bonus = grow16(sl.bonus, scoped)
	bonus := sl.bonus
	if cap(sl.first) < m {
		sl.first = make([]int32, m, m+8)
	}
	first := sl.first[:m]
	for i := range first {
		first[i] = -1
	}

	// The bonus of each character, and the first place each of the
	// pattern's can be.
	pi, lastIdx := 0, 0
	prev := clsWhite
	for off := range scoped {
		cur := t.Cls[minIdx+off]
		bonus[off] = bonusOf[prev<<3|cur]
		prev = cur
		if pi < m && chars[off] == pat[pi] {
			first[pi] = int32(off)
			lastIdx = off
			pi++
		}
	}
	if pi != m {
		return noSpan
	}

	f0 := int(first[0])
	width := lastIdx - f0 + 1
	sl.scores = grow16(sl.scores, m*width)
	sl.consec = grow16(sl.consec, m*width)
	scores, consec := sl.scores, sl.consec

	inGap := false
	var previous int16
	p0 := pat[0]
	for col := f0; col <= lastIdx; col++ {
		rel := col - f0
		if chars[col] == p0 {
			score := scoreMatch + bonus[col]*bonusFirstChar
			scores[rel], consec[rel] = score, 1
			previous, inGap = score, false
		} else {
			gap := int16(scoreGapStart)
			if inGap {
				gap = scoreGapExtension
			}
			score := max(previous+gap, 0)
			scores[rel], consec[rel] = score, 0
			previous, inGap = score, true
		}
	}

	maxScore, maxPos := int16(0), f0
	for pidx := 1; pidx < m; pidx++ {
		pc := pat[pidx]
		row := pidx * width
		prevRow := row - width
		inGap = false
		start := int(first[pidx])
		if rel := start - f0; rel > 0 {
			scores[row+rel-1], consec[row+rel-1] = 0, 0
		}
		for col := start; col <= lastIdx; col++ {
			rel := col - f0
			var s1, s2, run int16
			if rel > 0 {
				gap := int16(scoreGapStart)
				if inGap {
					gap = scoreGapExtension
				}
				s2 = scores[row+rel-1] + gap
			}
			if chars[col] == pc && rel > 0 {
				s1 = scores[prevRow+rel-1] + scoreMatch
				b := bonus[col]
				run = consec[prevRow+rel-1] + 1
				if run > 1 {
					fb := bonus[col-int(run)+1]
					if b >= bonusBoundary && b > fb {
						run = 1
					} else {
						b = max(b, fb, bonusConsecutive)
					}
				}
				if s1+b < s2 {
					s1 += bonus[col]
					run = 0
				} else {
					s1 += b
				}
			}
			consec[row+rel] = run
			inGap = s1 < s2
			cell := max(s1, s2, 0)
			scores[row+rel] = cell
			if pidx == m-1 && cell > maxScore {
				maxScore, maxPos = cell, col
			}
		}
	}
	start := backtrack(scores, consec, first, f0, width, m, maxPos, minIdx, marks)
	return span{minIdx + start, minIdx + maxPos + 1, int(maxScore)}
}

// backtrack walks the table back from the best cell to where the match
// starts, setting marks, where given, at the characters it took.
func backtrack(scores, consec []int16, first []int32, f0, width, m, maxPos, minIdx int, marks []bool) int {
	i, j := m-1, maxPos
	prefer := true
	for i >= 0 && j >= int(first[i]) {
		row := i * width
		rel := j - f0
		score := scores[row+rel]
		var diagonal, left int16
		if i > 0 && rel > 0 {
			diagonal = scores[row-width+rel-1]
		}
		if rel > 0 {
			left = scores[row+rel-1]
		}
		if score > diagonal && (score > left || score == left && prefer) {
			if marks != nil {
				if pos := minIdx + j; pos < len(marks) {
					marks[pos] = true
				}
			}
			if i == 0 {
				return j
			}
			i--
		}
		// The cell of the next row counts only where this match wrote it:
		// the slab is not cleared between names.
		prefer = consec[row+rel] > 1 ||
			i < m-1 && rel+1 < width && rel+1 >= int(first[i+1])-f0-1 && consec[row+width+rel+1] > 0
		j--
	}
	if marks != nil {
		for k := 0; k <= i; k++ {
			if pos := minIdx + int(first[k]); pos < len(marks) {
				marks[pos] = true
			}
		}
	}
	return max(0, int(first[0]))
}

func bonusAt(t *Text, i int) int16 {
	if i == 0 {
		return bonusOf[clsWhite<<3|t.Cls[0]]
	}
	return bonusOf[t.Cls[i-1]<<3|t.Cls[i]]
}

func equalAt(chars, pat []rune, at int) bool {
	for i, c := range pat {
		if chars[at+i] != c {
			return false
		}
	}
	return true
}

// indexFrom is where pat first is in chars at or after from, -1 for
// nowhere.
func indexFrom(chars, pat []rune, from int) int {
	p0 := pat[0]
	for i := from; i+len(pat) <= len(chars); i++ {
		if chars[i] == p0 && equalAt(chars, pat, i) {
			return i
		}
	}
	return -1
}

// exactMatch matches the pattern as it is: of its occurrences, the one
// best placed, the first of them when several are as good. With boundary,
// only those between two ends of words count.
func exactMatch(t *Text, pat []rune, boundary bool) span {
	if len(pat) == 0 || len(pat) > len(t.Chars) {
		return noSpan
	}
	bestPos, bestBonus := -1, int16(-1)
	for off := 0; ; {
		i := indexFrom(t.Chars, pat, off)
		if i < 0 {
			break
		}
		bonus := bonusAt(t, i)
		if !boundary || isBoundaryMatch(t, i, i+len(pat), bonus) {
			if bonus > bestBonus {
				bestPos, bestBonus = i, bonus
				if bonus >= bonusBoundary {
					break
				}
			}
		}
		off = i + 1
	}
	if bestPos < 0 {
		return noSpan
	}
	end := bestPos + len(pat)
	if boundary {
		return span{bestPos, end, scoreMatch*len(pat) + bonusWhite*(len(pat)+1) + int(bestBonus)}
	}
	return span{bestPos, end, calculateScore(t, pat, bestPos, end)}
}

func isBoundaryMatch(t *Text, start, end int, startBonus int16) bool {
	if startBonus < bonusBoundary {
		return false
	}
	if start > 0 && t.Cls[start-1] > clsDelimiter {
		return false
	}
	return end >= len(t.Chars) || t.Cls[end] <= clsDelimiter
}

func leadingWhite(t *Text) int {
	i := 0
	for i < len(t.Cls) && t.Cls[i] == clsWhite {
		i++
	}
	return i
}

func trailingWhite(t *Text) int {
	n := 0
	for i := len(t.Cls) - 1; i >= 0 && t.Cls[i] == clsWhite; i-- {
		n++
	}
	return n
}

func prefixMatch(t *Text, pat []rune) span {
	if len(pat) == 0 {
		return span{0, 0, 0}
	}
	start := 0
	if !unicode.IsSpace(pat[0]) {
		start = leadingWhite(t)
	}
	if len(t.Chars)-start < len(pat) || !equalAt(t.Chars, pat, start) {
		return noSpan
	}
	end := start + len(pat)
	return span{start, end, calculateScore(t, pat, start, end)}
}

func suffixMatch(t *Text, pat []rune) span {
	trimmed := len(t.Chars)
	if len(pat) == 0 || !unicode.IsSpace(pat[len(pat)-1]) {
		trimmed -= trailingWhite(t)
	}
	if len(pat) == 0 {
		return span{trimmed, trimmed, 0}
	}
	start := trimmed - len(pat)
	if start < 0 || !equalAt(t.Chars, pat, start) {
		return noSpan
	}
	return span{start, trimmed, calculateScore(t, pat, start, trimmed)}
}

func equalMatch(t *Text, pat []rune) span {
	if len(pat) == 0 {
		return noSpan
	}
	start, trailing := 0, 0
	if !unicode.IsSpace(pat[0]) {
		start = leadingWhite(t)
	}
	if !unicode.IsSpace(pat[len(pat)-1]) {
		trailing = trailingWhite(t)
	}
	if len(t.Chars)-start-trailing != len(pat) || !equalAt(t.Chars, pat, start) {
		return noSpan
	}
	return span{start, start + len(pat), (scoreMatch+bonusWhite)*len(pat) + (bonusFirstChar-1)*bonusWhite}
}
