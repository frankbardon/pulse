package pulse

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/frankbardon/pulse/errors"
)

// LabelTableInfo is one registered label table as surfaced by
// LabelTables. RowCount and Enumerable describe whether the table can
// be reverse-searched (ResolveLabel requires a static Rows map).
type LabelTableInfo struct {
	Name     string `json:"name"`
	RowCount int    `json:"row_count"`
	// Enumerable reports whether the table exposes a static Rows map
	// (true) or only a function-driven Lookup closure (false). Reverse
	// search via ResolveLabel works only on enumerable tables.
	Enumerable bool `json:"enumerable"`
}

// LabelMatch is one (key, value) pair from a label table, where key is
// the on-disk categorical dictionary value and value is the display
// label. ResolveLabel returns these ranked by Score.
type LabelMatch struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Score is the match confidence in [0,1]: 1.0 is an exact (or
	// exact-key) hit, ~0.9+ a prefix/near-typo, and lower values are
	// fuzzy matches. Callers should treat a high top score that is
	// clearly ahead of the runner-up as confident, and present options
	// to the user when the best score is low or several are close.
	Score float64 `json:"score"`
}

// LabelTables returns the registered label tables in name order. It
// reports each table's row count and whether it is reverse-searchable.
// Returns nil when no label tables are registered.
func (p *Pulse) LabelTables() []LabelTableInfo {
	reg := p.svc.Extensions()
	if reg == nil || len(reg.LabelTables) == 0 {
		return nil
	}
	out := make([]LabelTableInfo, 0, len(reg.LabelTables))
	for name, tbl := range reg.LabelTables {
		out = append(out, LabelTableInfo{
			Name:       name,
			RowCount:   len(tbl.Rows),
			Enumerable: tbl.Rows != nil,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// labelMatchFloor is the minimum score for a fuzzy candidate to be
// considered a real match. Below this, a query is treated as having no
// confident match — but ResolveLabel still returns the closest few so a
// caller can offer "did you mean …?" suggestions.
const labelMatchFloor = 0.34

// labelClosestFallback caps how many sub-floor suggestions are returned
// when nothing clears the floor.
const labelClosestFallback = 3

// ResolveLabel reverse-searches a registered label table: given a
// human-readable query — including a minor misspelling — it returns up
// to limit matching (key, value) pairs ranked by a confidence Score in
// [0,1]. Matching is typo-tolerant: the query and each label are
// normalized (lowercased, punctuation folded to spaces, whitespace
// collapsed), and the score is the best of an exact/prefix/substring
// match, a Damerau edit-distance ratio (catches typos and adjacent
// transpositions), and a character-trigram Dice coefficient (catches
// word reordering and partial overlap). An exact key match also scores
// 1.0.
//
// Candidates scoring at or above an internal floor are returned best
// first. When nothing clears the floor the closest few are returned
// anyway (with their low scores) so the caller can present "did you
// mean" options rather than a dead end. An empty query returns the
// first limit rows in label order (a cheap "browse" mode, Score 0).
// limit <= 0 defaults to 10.
//
// Returns PULSE_LABEL_TABLE_UNKNOWN when the table is not registered and
// PULSE_LABEL_TABLE_NOT_ENUMERABLE when the table exposes only a Lookup
// closure (no static Rows to scan).
func (p *Pulse) ResolveLabel(table, query string, limit int) ([]LabelMatch, error) {
	reg := p.svc.Extensions()
	if reg == nil || reg.LabelTables == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_TABLE_UNKNOWN,
			fmt.Sprintf("label table %q is not registered", table),
			map[string]any{"table": table})
	}
	tbl, ok := reg.LabelTables[table]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_TABLE_UNKNOWN,
			fmt.Sprintf("label table %q is not registered", table),
			map[string]any{"table": table})
	}
	if tbl.Rows == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_TABLE_NOT_ENUMERABLE,
			fmt.Sprintf("label table %q is function-driven (no static Rows); reverse search is unavailable", table),
			map[string]any{"table": table})
	}
	if limit <= 0 {
		limit = 10
	}

	raw := strings.TrimSpace(query)
	if raw == "" {
		return browseRows(tbl.Rows, limit), nil
	}

	nq := normalizeLabel(raw)
	qTri := trigrams(nq)

	matches := make([]LabelMatch, 0, len(tbl.Rows))
	for key, val := range tbl.Rows {
		s := scoreLabel(raw, nq, qTri, key, val)
		if s <= 0 {
			continue
		}
		matches = append(matches, LabelMatch{Key: key, Value: val, Score: s})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if matches[i].Value != matches[j].Value {
			return matches[i].Value < matches[j].Value
		}
		return matches[i].Key < matches[j].Key
	})

	// Keep everything at or above the floor; if nothing clears it, fall
	// back to the closest few so the caller can still offer suggestions.
	kept := 0
	for kept < len(matches) && matches[kept].Score >= labelMatchFloor {
		kept++
	}
	if kept == 0 {
		kept = min(labelClosestFallback, len(matches))
	}
	matches = matches[:kept]
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

// browseRows returns the first limit rows of a table in label order,
// each with Score 0 (there is no query to score against).
func browseRows(rows map[string]string, limit int) []LabelMatch {
	out := make([]LabelMatch, 0, len(rows))
	for key, val := range rows {
		out = append(out, LabelMatch{Key: key, Value: val})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value < out[j].Value
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// scoreLabel computes the [0,1] match confidence between a query and one
// (key, value) row. rawQ is the trimmed original query (for exact-key
// comparison); nq and qTri are the normalized query and its trigram
// multiset, precomputed once by the caller.
func scoreLabel(rawQ, nq string, qTri map[string]int, key, val string) float64 {
	if key == rawQ {
		return 1.0
	}
	nv := normalizeLabel(val)
	if nv == nq {
		return 1.0
	}

	score := 0.0
	switch {
	case strings.HasPrefix(nv, nq):
		score = 0.95
	case strings.HasPrefix(nq, nv):
		score = 0.9
	case strings.Contains(nv, nq):
		// Substring: scale by how much of the label the query covers.
		score = 0.7 + 0.15*ratioLen(nq, nv)
	}

	qr, vr := []rune(nq), []rune(nv)
	if maxLen := max(len(qr), len(vr)); maxLen > 0 {
		if lr := 1.0 - float64(osaDistance(qr, vr))/float64(maxLen); lr > score {
			score = lr
		}
	}
	if td := diceCoefficient(qTri, trigrams(nv)); td > score {
		score = td
	}
	return score
}

// ratioLen returns len(a)/len(b) in runes, clamped to [0,1].
func ratioLen(a, b string) float64 {
	la, lb := len([]rune(a)), len([]rune(b))
	if lb == 0 {
		return 0
	}
	if la > lb {
		return 1
	}
	return float64(la) / float64(lb)
}

// normalizeLabel lowercases, folds every non-alphanumeric rune to a
// single space, and trims — so "Coca-Cola" and "coca   cola" both
// normalize to "coca cola".
func normalizeLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// trigrams returns the character-trigram multiset of s, padded with two
// leading and one trailing space so short strings still yield trigrams
// (the pg_trgm convention).
func trigrams(s string) map[string]int {
	rs := []rune("  " + s + " ")
	m := make(map[string]int, len(rs))
	for i := 0; i+3 <= len(rs); i++ {
		m[string(rs[i:i+3])]++
	}
	return m
}

// diceCoefficient is 2|A∩B| / (|A|+|B|) over two trigram multisets,
// counting multiplicities. Returns 0 when either side is empty.
func diceCoefficient(a, b map[string]int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	sizeA, sizeB, inter := 0, 0, 0
	for _, c := range a {
		sizeA += c
	}
	for tri, c := range b {
		sizeB += c
		if ac, ok := a[tri]; ok {
			inter += min(ac, c)
		}
	}
	if sizeA+sizeB == 0 {
		return 0
	}
	return 2 * float64(inter) / float64(sizeA+sizeB)
}

// osaDistance is the optimal string alignment (restricted Damerau-
// Levenshtein) edit distance: insertions, deletions, substitutions, and
// adjacent transpositions, each cost 1.
func osaDistance(a, b []rune) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev2 := make([]int, lb+1)
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			best := min(del, min(ins, sub))
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if t := prev2[j-2] + 1; t < best {
					best = t
				}
			}
			curr[j] = best
		}
		prev2, prev, curr = prev, curr, prev2
	}
	return prev[lb]
}
