// Package multiplicity is the pure multiple-comparison correction core:
// it maps one vector of raw p-values to its adjusted twin under a named
// method (bonferroni, holm, bh, by, none), matching R's stats::p.adjust.
//
// The package is deliberately family-agnostic. It knows nothing about
// tests, overlays, matrices, rows or columns; a caller assembles one
// family's p-values into a slice, adjusts it, and maps the result back
// to its own coordinates.
//
// Contract, shared by every method:
//   - the input slice is never mutated; the result is a fresh slice of
//     the same length, position i adjusting input i;
//   - NaN inputs are excluded from the family size m and come back as
//     NaN in place (R drops NA from n the same way);
//   - results are capped at 1;
//   - ties are ordered by original position, and the step-down (holm)
//     and step-up (bh, by) methods enforce monotonicity with a running
//     max / min over the sorted order, so tied inputs adjust equally;
//   - with m <= 1 every method returns the input unchanged, as R does.
package multiplicity

import (
	"fmt"
	"math"
	"sort"
)

// Method names one p-value correction procedure.
type Method string

// The supported correction methods.
const (
	// MethodNone is the identity: the result is an exact copy.
	MethodNone Method = "none"
	// MethodBonferroni multiplies every p by m (family-wise error).
	MethodBonferroni Method = "bonferroni"
	// MethodHolm is Holm's step-down procedure (family-wise error).
	MethodHolm Method = "holm"
	// MethodBH is the Benjamini-Hochberg step-up procedure (false
	// discovery rate under independence or positive dependence).
	MethodBH Method = "bh"
	// MethodBY is the Benjamini-Yekutieli step-up procedure (false
	// discovery rate under arbitrary dependence).
	MethodBY Method = "by"
)

// Methods returns every supported method, in a stable order.
func Methods() []Method {
	return []Method{MethodNone, MethodBonferroni, MethodHolm, MethodBH, MethodBY}
}

// Valid reports whether m names a supported method.
func (m Method) Valid() bool {
	switch m {
	case MethodNone, MethodBonferroni, MethodHolm, MethodBH, MethodBY:
		return true
	}
	return false
}

// Adjust returns the adjusted p-values of p under method. It errors
// only on an unknown method; callers validate method names before the
// fold and surface their own coded error.
func Adjust(method Method, p []float64) ([]float64, error) {
	switch method {
	case MethodNone:
		return None(p), nil
	case MethodBonferroni:
		return Bonferroni(p), nil
	case MethodHolm:
		return Holm(p), nil
	case MethodBH:
		return BH(p), nil
	case MethodBY:
		return BY(p), nil
	}
	return nil, fmt.Errorf("multiplicity: unknown method %q", string(method))
}

// FamilySize returns m, the number of non-NaN p-values in p — the
// count every method divides or multiplies by.
func FamilySize(p []float64) int {
	n := 0
	for _, v := range p {
		if !math.IsNaN(v) {
			n++
		}
	}
	return n
}

// None returns an exact copy of p.
func None(p []float64) []float64 {
	out := make([]float64, len(p))
	copy(out, p)
	return out
}

// Bonferroni returns min(1, m·p) for each non-NaN p.
func Bonferroni(p []float64) []float64 {
	out := None(p)
	m := FamilySize(p)
	if m <= 1 {
		return out
	}
	fm := float64(m)
	for i, v := range p {
		if math.IsNaN(v) {
			continue
		}
		out[i] = math.Min(1, fm*v)
	}
	return out
}

// Holm returns Holm's step-down adjustment: with the non-NaN p sorted
// ascending (ties by original position), the k-th (1-based) adjusts to
// min(1, max_{j<=k} (m-j+1)·p_(j)).
func Holm(p []float64) []float64 {
	out := None(p)
	idx := sortedIndex(p, false)
	m := len(idx)
	if m <= 1 {
		return out
	}
	running := math.Inf(-1)
	for k, i := range idx {
		v := float64(m-k) * p[i]
		running = math.Max(running, v)
		out[i] = math.Min(1, running)
	}
	return out
}

// BH returns the Benjamini-Hochberg step-up adjustment: with the
// non-NaN p sorted descending, the one of ascending rank i adjusts to
// min(1, min_{j>=i} (m/j)·p_(j)).
func BH(p []float64) []float64 {
	return stepUp(p, 1)
}

// BY returns the Benjamini-Yekutieli step-up adjustment: BH scaled by
// q = sum_{k=1..m} 1/k.
func BY(p []float64) []float64 {
	m := FamilySize(p)
	return stepUp(p, harmonic(m))
}

// stepUp is the shared BH / BY kernel: (q·m/i)·p_(i) with a running
// minimum from the largest p down, capped at 1. The operation order
// mirrors R's `q * n / i * p[o]` (and `n / i * p[o]` for BH, q = 1).
func stepUp(p []float64, q float64) []float64 {
	out := None(p)
	idx := sortedIndex(p, true)
	m := len(idx)
	if m <= 1 {
		return out
	}
	scale := q * float64(m)
	running := math.Inf(1)
	for k, i := range idx {
		rank := m - k // ascending rank of this p
		v := scale / float64(rank) * p[i]
		running = math.Min(running, v)
		out[i] = math.Min(1, running)
	}
	return out
}

// harmonic returns sum_{k=1..m} 1/k, summed smallest term first for
// accuracy (R accumulates in long double).
func harmonic(m int) float64 {
	s := 0.0
	for k := m; k >= 1; k-- {
		s += 1 / float64(k)
	}
	return s
}

// sortedIndex returns the positions of the non-NaN entries of p sorted
// by value — ascending, or descending when desc — with ties kept in
// original-position order.
func sortedIndex(p []float64, desc bool) []int {
	idx := make([]int, 0, len(p))
	for i, v := range p {
		if !math.IsNaN(v) {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if desc {
			return p[idx[a]] > p[idx[b]]
		}
		return p[idx[a]] < p[idx[b]]
	})
	return idx
}
