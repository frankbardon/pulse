package synth

import "math"

// Pair arbitration modes (Spec.PairArbitration). When two captured
// relationships name the same target — two categorical pairs driving
// one categorical, a set-categorical and a within-set pair driving one
// set option, two categorical-numeric pairs driving one numeric —
// resolveConflicts keeps exactly one of them.
const (
	// PairArbitrationStrength keeps the relationship with the strongest
	// measured association: bias-corrected Cramér's V for a categorical
	// or set-option target, adjusted η² (ε²) for a numeric target. It is
	// the default (an empty Spec.PairArbitration means it).
	PairArbitrationStrength = "strength"
	// PairArbitrationDeclared keeps the first-declared relationship, the
	// rule every spec followed before strength arbitration existed.
	PairArbitrationDeclared = "declared"
)

// contingencyStrength is the bias-corrected Cramér's V (Bergsma 2013)
// of a contingency table given as cells. It is the strength a
// categorical-categorical, set-categorical or set-set pair carries into
// arbitration.
//
// The bias correction is the point rather than a refinement: arbitration
// compares tables of DIFFERENT shapes (a two-level set option against a
// five-level region), and plain V grows with the table's dimensions
// even for independent fields, so a noise field with many levels would
// out-bid a real two-level driver. Under independence the corrected
// figure is ~0 whatever the shape.
//
// Every product is rounded explicitly (float64(a*b)) so no architecture
// can fuse it into a multiply-add: the strength decides which pair
// generates, so a last-bit difference between arm64 and amd64 could
// flip a near-tie and change the file.
func contingencyStrength(cells []CategoricalPairCellSpec) float64 {
	rows := map[string]float64{}
	cols := map[string]float64{}
	var rowOrder, colOrder []string
	obs := map[[2]string]float64{}
	var n float64
	for _, c := range cells {
		if c.Count <= 0 {
			continue
		}
		cnt := float64(c.Count)
		if _, ok := rows[c.AValue]; !ok {
			rowOrder = append(rowOrder, c.AValue)
		}
		if _, ok := cols[c.BValue]; !ok {
			colOrder = append(colOrder, c.BValue)
		}
		rows[c.AValue] += cnt
		cols[c.BValue] += cnt
		obs[[2]string{c.AValue, c.BValue}] += cnt
		n += cnt
	}
	r, k := float64(len(rowOrder)), float64(len(colOrder))
	if r < 2 || k < 2 || n < 2 {
		return 0
	}
	// chi² over every (row, column) combination, observed or not: an
	// unobserved combination is a zero cell, and a structural zero is
	// exactly the association a within-set dependency carries. Walked in
	// first-seen order so the float sum is reproducible.
	var chi2 float64
	for _, a := range rowOrder {
		for _, b := range colOrder {
			e := float64(rows[a]*cols[b]) / n
			d := obs[[2]string{a, b}] - e
			chi2 += float64(d*d) / e
		}
	}
	phi2 := chi2 / n
	phi2c := phi2 - float64((r-1)*(k-1))/(n-1)
	if phi2c <= 0 {
		return 0
	}
	rc := r - float64((r-1)*(r-1))/(n-1)
	kc := k - float64((k-1)*(k-1))/(n-1)
	den := math.Min(rc-1, kc-1)
	if den <= 0 {
		return 0
	}
	return math.Sqrt(phi2c / den)
}

// groupedStrength is the adjusted η² (ε²) of a numeric target grouped
// by a categorical's levels or a set option's two states, computed from
// per-group (n, mean, sample std): the share of the target's variance
// the grouping explains, less what that many groups would explain by
// chance. It is the strength a categorical-numeric or set-numeric pair
// carries into arbitration.
//
// ok is false when a group carries no count (a hand-authored spec may
// omit `n`): the figure is then unknown, never guessed, and the target
// falls back to first-declared arbitration.
func groupedStrength(cats []CategoricalNumericCategorySpec) (float64, bool) {
	var n, sum float64
	for _, c := range cats {
		if c.N <= 0 {
			return 0, false
		}
		cnt := float64(c.N)
		n += cnt
		sum += float64(cnt * c.Mean)
	}
	k := float64(len(cats))
	if k < 2 || n <= k {
		return 0, true
	}
	grand := sum / n
	var ssb, ssw float64
	for _, c := range cats {
		cnt := float64(c.N)
		d := c.Mean - grand
		ssb += float64(cnt * float64(d*d))
		ssw += float64((cnt - 1) * float64(c.Std*c.Std))
	}
	sst := ssb + ssw
	if sst <= 0 {
		return 0, true
	}
	msw := ssw / (n - k)
	eps := (ssb - float64((k-1)*msw)) / sst
	if eps <= 0 {
		return 0, true
	}
	return eps, true
}
