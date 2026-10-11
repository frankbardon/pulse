package vecmath

import "math"

// SetOverlap is the three counts every set-similarity kernel reads: the
// DISTINCT sizes of the two sets and of their intersection. Callers
// build it from label lists (LabelOverlap) or from bitmask popcounts;
// the kernels never see the members themselves.
//
// # Edge semantics (pinned)
//
//   - Jaccard |A∩B|/|A∪B|, Dice 2|A∩B|/(|A|+|B|) and Overlap
//     |A∩B|/min(|A|,|B|) are NaN when their denominator is zero: two
//     empty sets for Jaccard and Dice, any empty side for Overlap.
//   - Hamming |A△B| is a count; two empty sets give 0.
//   - A null (absent) set is the caller's concern: the expression layer
//     answers NaN before reaching these kernels. An empty set is a set.
type SetOverlap struct {
	A, B, Inter int
}

// LabelOverlap counts the distinct members of a and b and of their
// intersection. Duplicates within one list count once; order is
// irrelevant. Neither input is mutated.
func LabelOverlap(a, b []string) SetOverlap {
	inA := make(map[string]struct{}, len(a))
	for _, s := range a {
		inA[s] = struct{}{}
	}
	inB := make(map[string]struct{}, len(b))
	inter := 0
	for _, s := range b {
		if _, dup := inB[s]; dup {
			continue
		}
		inB[s] = struct{}{}
		if _, ok := inA[s]; ok {
			inter++
		}
	}
	return SetOverlap{A: len(inA), B: len(inB), Inter: inter}
}

// Jaccard is |A∩B| / |A∪B|; NaN when both sets are empty.
func Jaccard(o SetOverlap) float64 {
	union := o.A + o.B - o.Inter
	if union == 0 {
		return math.NaN()
	}
	return float64(o.Inter) / float64(union)
}

// Dice is 2|A∩B| / (|A|+|B|); NaN when both sets are empty.
func Dice(o SetOverlap) float64 {
	den := o.A + o.B
	if den == 0 {
		return math.NaN()
	}
	return float64(2*o.Inter) / float64(den)
}

// Hamming is |A△B|, the number of members in exactly one set.
func Hamming(o SetOverlap) int {
	return o.A + o.B - 2*o.Inter
}

// Overlap is |A∩B| / min(|A|,|B|) (the Szymkiewicz–Simpson
// coefficient); NaN when either set is empty.
func Overlap(o SetOverlap) float64 {
	den := min(o.A, o.B)
	if den == 0 {
		return math.NaN()
	}
	return float64(o.Inter) / float64(den)
}
