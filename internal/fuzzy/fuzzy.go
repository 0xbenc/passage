// Package fuzzy is a small, dependency-free fuzzy matcher modeled on fzf's
// integer scoring. It answers two questions about a query against a candidate
// string: does the query match (as an order-preserving subsequence), and if
// so, with what score and which matched rune positions.
//
// The scoring is deterministic integer arithmetic — no floats — so results are
// stable and golden-testable. Positions are rune indices into the candidate
// (not byte offsets), which is exactly what a renderer needs to highlight the
// matched runes after width-aware truncation.
//
// passage uses it to rank and highlight password-store entry paths. It only
// ever sees non-secret metadata (paths/display names), never secret bytes.
package fuzzy

import "unicode"

// Scoring constants, ported from fzf's FuzzyMatchV1. The absolute values do
// not matter; the ratios do — a boundary match outranks a mid-word match, a
// run of consecutive matches outranks a scattered one, and the first matched
// rune is weighted double so a leading match dominates.
const (
	scoreMatch        = 16
	scoreGapStart     = -3
	scoreGapExtension = -1

	// bonusBoundary rewards a match at a word boundary — the rune just
	// after a delimiter (/, -, _, ., :, ;, |, space, …) or at the start.
	// This is what makes "gh" rank "work/github" above an incidental
	// mid-word "gh".
	bonusBoundary = scoreMatch / 2 // 8
	// bonusCamel rewards a lower→Upper camelCase hump.
	bonusCamel = bonusBoundary - 1 // 7
	// bonusConsecutive rewards adjacency, cancelling the gap penalty so a
	// contiguous match always beats a split one.
	bonusConsecutive = -(scoreGapStart + scoreGapExtension) // 4
	// bonusFirstCharMultiplier double-weights the bonus of the first
	// matched rune.
	bonusFirstCharMultiplier = 2
)

// MinScorePerRune is the average score a match must reach per query rune to be
// considered *relevant* (as opposed to merely a valid subsequence). It exists
// because pure subsequence matching is too lenient: a short query like "redis"
// matches a long unrelated path whose letters happen to appear scattered
// across it (inte[r]net mi[d]w[e]st w[i]fi pa[s]sword). Such matches are
// gap-dominated and score far below a contiguous or word-boundary match.
//
// Tuned against scoreMatch (16): measured boundary/consecutive matches score
// ~17-26 per rune; scattered ones ~7-9. 12 sits cleanly between, with margin
// on both sides.
const MinScorePerRune = 12

// Result is a successful match: its score and the ascending rune indices in
// the candidate that the query matched.
type Result struct {
	Score     int
	Positions []int
}

// Relevant reports whether a match clears the relevance threshold for a query
// of the given rune length, filtering out scattered subsequence matches while
// keeping contiguous and word-boundary ones. An empty query is always relevant.
func Relevant(r Result, queryLen int) bool {
	return queryLen <= 0 || r.Score >= queryLen*MinScorePerRune
}

type charClass int

const (
	classWhite charClass = iota
	classNonWord
	classDigit
	classLower
	classUpper
)

func classOf(r rune) charClass {
	switch {
	case r == ' ' || r == '\t' || r == '\n' || r == '\r':
		return classWhite
	case unicode.IsDigit(r):
		return classDigit
	case unicode.IsUpper(r):
		return classUpper
	case unicode.IsLower(r):
		return classLower
	case unicode.IsLetter(r):
		// Letters without case (CJK, etc.) behave as word characters.
		return classLower
	default:
		return classNonWord
	}
}

// Match reports whether query matches candidate as an order-preserving
// subsequence and, if so, returns the score and matched rune positions.
//
// Case handling is smart-case: an all-lowercase query matches
// case-insensitively; any uppercase rune in the query makes the whole match
// case-sensitive. An empty query matches everything with score 0 and no
// positions.
func Match(query, candidate string) (Result, bool) {
	if query == "" {
		return Result{}, true
	}
	pattern := []rune(query)
	text := []rune(candidate)
	if len(pattern) > len(text) {
		return Result{}, false
	}
	caseSensitive := hasUpper(pattern)

	// Forward pass: locate the first subsequence match, recording where the
	// first query rune landed (sidx) and where the last one ended (eidx).
	sidx, eidx := -1, -1
	pidx := 0
	for idx := 0; idx < len(text); idx++ {
		if charsEqual(text[idx], pattern[pidx], caseSensitive) {
			if sidx < 0 {
				sidx = idx
			}
			pidx++
			if pidx == len(pattern) {
				eidx = idx + 1
				break
			}
		}
	}
	if eidx < 0 {
		return Result{}, false
	}

	// Backward pass: tighten sidx to the rightmost start that still spans
	// the whole pattern, so scoring works on the smallest matching window.
	pidx = len(pattern) - 1
	for idx := eidx - 1; idx >= sidx; idx-- {
		if charsEqual(text[idx], pattern[pidx], caseSensitive) {
			pidx--
			if pidx < 0 {
				sidx = idx
				break
			}
		}
	}

	score, positions := calculateScore(caseSensitive, text, pattern, sidx, eidx)
	return Result{Score: score, Positions: positions}, true
}

func calculateScore(caseSensitive bool, text, pattern []rune, sidx, eidx int) (int, []int) {
	pidx := 0
	score := 0
	inGap := false
	consecutive := 0
	firstBonus := 0
	positions := make([]int, 0, len(pattern))

	prevClass := classWhite
	if sidx > 0 {
		prevClass = classOf(text[sidx-1])
	}

	for idx := sidx; idx < eidx; idx++ {
		char := text[idx]
		class := classOf(char)
		if charsEqual(char, pattern[pidx], caseSensitive) {
			positions = append(positions, idx)
			score += scoreMatch
			bonus := bonusFor(prevClass, class)
			if consecutive == 0 {
				firstBonus = bonus
			} else {
				// A boundary inside a run restarts the boundary bonus;
				// otherwise consecutive adjacency is the floor.
				if bonus >= bonusBoundary && bonus > firstBonus {
					firstBonus = bonus
				}
				bonus = maxInt(maxInt(bonus, firstBonus), bonusConsecutive)
			}
			if pidx == 0 {
				score += bonus * bonusFirstCharMultiplier
			} else {
				score += bonus
			}
			inGap = false
			consecutive++
			pidx++
			if pidx == len(pattern) {
				break
			}
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
		prevClass = class
	}
	return score, positions
}

func bonusFor(prev, cur charClass) int {
	if cur == classWhite || cur == classNonWord {
		return 0
	}
	switch {
	case prev == classWhite || prev == classNonWord:
		return bonusBoundary
	case prev == classLower && cur == classUpper:
		return bonusCamel
	case prev != classDigit && cur == classDigit:
		return bonusBoundary
	}
	return 0
}

func charsEqual(a, b rune, caseSensitive bool) bool {
	if a == b {
		return true
	}
	if caseSensitive {
		return false
	}
	return unicode.ToLower(a) == unicode.ToLower(b)
}

func hasUpper(rs []rune) bool {
	for _, r := range rs {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
