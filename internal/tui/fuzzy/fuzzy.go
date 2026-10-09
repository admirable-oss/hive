// Package fuzzy ranks strings against a typed pattern, for pickers: the
// pattern's characters must appear in order, and matches score higher when
// they start words, run together, or come early.
package fuzzy

import (
	"slices"
	"strings"
	"unicode"
)

// Score bonuses and penalties.
const (
	scoreMatch       = 16
	bonusBoundary    = 12 // first character of a word
	bonusConsecutive = 10
	bonusFirst       = 8 // the very first character
	penaltyGap       = 1 // per skipped character, after the first match
	penaltyLeading   = 1 // per character before the first match, up to 4
)

// Match reports whether pattern matches text, with a score (higher is
// better) and the rune positions in text that matched. Matching ignores
// case unless the pattern has an upper-case letter; spaces in the pattern
// are ignored.
func Match(pattern, text string) (score int, positions []int, ok bool) {
	pat := []rune(strings.ReplaceAll(pattern, " ", ""))
	if len(pat) == 0 {
		return 0, nil, true
	}
	fold := !hasUpper(pat)
	txt := []rune(text)
	if fold {
		for i, r := range pat {
			pat[i] = unicode.ToLower(r)
		}
	}
	at := func(i int) rune {
		if fold {
			return unicode.ToLower(txt[i])
		}
		return txt[i]
	}

	// Find the end of the leftmost match, then walk back to tighten it
	// (as fzf v1 does), so "ab" in "a_xab" picks the final "ab".
	pi, end := 0, -1
	for i := range txt {
		if at(i) == pat[pi] {
			pi++
			if pi == len(pat) {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return 0, nil, false
	}
	pi = len(pat) - 1
	start := end
	for i := end; i >= 0; i-- {
		if at(i) == pat[pi] {
			pi--
			if pi < 0 {
				start = i
				break
			}
		}
	}

	pi = 0
	prev := -2
	for i := start; i <= end && pi < len(pat); i++ {
		if at(i) != pat[pi] {
			continue
		}
		s := scoreMatch
		if i == 0 {
			s += bonusFirst
		}
		if isBoundary(txt, i) {
			s += bonusBoundary
		}
		if prev == i-1 {
			s += bonusConsecutive
		} else if prev >= 0 {
			s -= penaltyGap * (i - prev - 1)
		}
		score += s
		positions = append(positions, i)
		prev = i
		pi++
	}
	score -= penaltyLeading * min(start, 4)
	return score, positions, true
}

func hasUpper(rs []rune) bool {
	for _, r := range rs {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// isBoundary reports whether txt[i] starts a word: after a separator, or a
// capital after a lower-case letter (camelCase).
func isBoundary(txt []rune, i int) bool {
	if i == 0 {
		return true
	}
	p, c := txt[i-1], txt[i]
	if !unicode.IsLetter(p) && !unicode.IsDigit(p) {
		return unicode.IsLetter(c) || unicode.IsDigit(c)
	}
	return unicode.IsLower(p) && unicode.IsUpper(c)
}

// Result is a ranked candidate.
type Result struct {
	Index     int // into the candidates
	Score     int
	Positions []int
}

// Rank matches pattern against every candidate and returns the matches,
// best first; ties keep the candidates' order. An empty pattern returns
// every candidate in order.
func Rank(pattern string, candidates []string) []Result {
	var out []Result
	for i, c := range candidates {
		if s, pos, ok := Match(pattern, c); ok {
			out = append(out, Result{Index: i, Score: s, Positions: pos})
		}
	}
	slices.SortStableFunc(out, func(a, b Result) int { return b.Score - a.Score })
	return out
}
