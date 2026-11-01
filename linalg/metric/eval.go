package metric

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// Eval is the metric between a and b in form f, under the package's
// NaN policy (pairwise-complete, p/p_observed rescaling for the Scaled
// metrics, zero overlap NaN). s carries the fitted statistics a Fitted
// metric reads and is ignored otherwise.
//
// Errors: PULSE_VECTOR_METRIC_UNKNOWN for a form the metric does not
// have (or the zero Metric), *LengthMismatchError for unequal lengths,
// *StatsError for missing or unusable statistics, and *DomainError —
// beside a NaN value — for an element outside the metric's domain.
func (m Metric) Eval(f Form, a, b []float64, s *Stats) (float64, error) {
	d := m.def
	if d == nil {
		return math.NaN(), refusal("", reasonUnregistered, f, "", namesWith(func(d *definition) bool { return slices.Contains(d.forms, f) }))
	}
	if !slices.Contains(d.forms, f) {
		return math.NaN(), refusal(d.name, reasonNoForm, f, "", namesWith(func(d *definition) bool { return slices.Contains(d.forms, f) }))
	}
	if len(a) != len(b) {
		return math.NaN(), &LengthMismatchError{Left: len(a), Right: len(b)}
	}
	return d.eval(d, f, a, b, s)
}

// SetCounts are the three counts a set metric reads: the DISTINCT
// sizes of the two sets and of their intersection. A caller holding
// label lists or bitmasks builds them directly (popcounts) instead of
// expanding to 0 / 1 vectors.
type SetCounts struct {
	A, B, Inter int
}

// EvalSets is a set metric (Family FamilySet) over counts c in form f:
// jaccard |A∩B|/|A∪B| and dice 2|A∩B|/(|A|+|B|) are NaN for two empty
// sets, overlap |A∩B|/min(|A|,|B|) for any empty side; hamming |A△B| is
// a count (two empty sets give 0) and is not rescaled — sets have no
// missing elements. A non-set metric or a missing form is
// PULSE_VECTOR_METRIC_UNKNOWN; counts that cannot describe two sets are
// a *DomainError (ReasonSetCounts) beside NaN.
func (m Metric) EvalSets(f Form, c SetCounts) (float64, error) {
	d := m.def
	valid := namesWith(func(d *definition) bool { return d.sets != nil && slices.Contains(d.forms, f) })
	if d == nil || d.sets == nil {
		return math.NaN(), refusal(m.Name(), reasonNotAccepted, f, "", valid)
	}
	if !slices.Contains(d.forms, f) {
		return math.NaN(), refusal(d.name, reasonNoForm, f, "", valid)
	}
	if c.A < 0 || c.B < 0 || c.Inter < 0 || c.Inter > min(c.A, c.B) {
		return math.NaN(), &DomainError{Metric: d.name, Index: -1, Value: float64(c.Inter), Reason: ReasonSetCounts}
	}
	return d.sets(f, c), nil
}

// Observed is the number of complete pairs of a and b: the indices
// where neither side is NaN (p_observed).
func Observed(a, b []float64) (int, error) {
	if len(a) != len(b) {
		return 0, &LengthMismatchError{Left: len(a), Right: len(b)}
	}
	n := 0
	for i := range a {
		if !math.IsNaN(a[i]) && !math.IsNaN(b[i]) {
			n++
		}
	}
	return n, nil
}

// missing reports whether index i is not a complete pair.
func missing(a, b []float64, i int) bool { return math.IsNaN(a[i]) || math.IsNaN(b[i]) }

// rescale applies R's stats::dist() p/p_observed convention to a sum
// over n of p elements: s / (n / p), untouched when nothing is missing
// (so a complete vector's bits never depend on the rescaling).
func rescale(s float64, n, p int) float64 {
	if n == p {
		return s
	}
	return s / (float64(n) / float64(p))
}

// evalSquares is euclidean (the square root) and sq_euclidean: the
// sum of squared differences over the complete pairs, rescaled by
// p/p_observed.
func evalSquares(d *definition, _ Form, a, b []float64, _ *Stats) (float64, error) {
	var s float64
	n := 0
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		diff := a[i] - b[i]
		s += float64(diff * diff)
		n++
	}
	if n == 0 {
		return math.NaN(), nil
	}
	s = rescale(s, n, len(a))
	if d.name == "euclidean" {
		return math.Sqrt(s), nil
	}
	return s, nil
}

// evalManhattan is the sum of absolute differences over the complete
// pairs, rescaled by p/p_observed.
func evalManhattan(_ *definition, _ Form, a, b []float64, _ *Stats) (float64, error) {
	var s float64
	n := 0
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		s += math.Abs(a[i] - b[i])
		n++
	}
	if n == 0 {
		return math.NaN(), nil
	}
	return rescale(s, n, len(a)), nil
}

// evalChebyshev is the largest absolute difference over the complete
// pairs; a maximum is not rescaled.
func evalChebyshev(_ *definition, _ Form, a, b []float64, _ *Stats) (float64, error) {
	var s float64
	n := 0
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		s = math.Max(s, math.Abs(a[i]-b[i]))
		n++
	}
	if n == 0 {
		return math.NaN(), nil
	}
	return s, nil
}

// evalCosine is the cosine similarity over the complete pairs (the dot
// product and both norms over the same pairs; NaN for a zero norm) or
// its distance 1 − cos, the cosine clamped to [−1, 1] first so the
// distance lies in [0, 2].
func evalCosine(_ *definition, f Form, a, b []float64, _ *Stats) (float64, error) {
	var dot, aa, bb float64
	n := 0
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		dot += float64(a[i] * b[i])
		aa += float64(a[i] * a[i])
		bb += float64(b[i] * b[i])
		n++
	}
	if n == 0 || aa == 0 || bb == 0 {
		return math.NaN(), nil
	}
	c := dot / (math.Sqrt(aa) * math.Sqrt(bb))
	if f == Similarity {
		return c, nil
	}
	return 1 - math.Max(-1, math.Min(1, c)), nil
}

// evalCorrelation is Pearson r over the complete pairs: each side
// centered on its own complete-pair mean, then the cosine. NaN with no
// complete pair or a zero variance on either side.
func evalCorrelation(_ *definition, _ Form, a, b []float64, _ *Stats) (float64, error) {
	var sa, sb float64
	n := 0
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		sa += a[i]
		sb += b[i]
		n++
	}
	if n == 0 {
		return math.NaN(), nil
	}
	ma, mb := sa/float64(n), sb/float64(n)
	var sab, saa, sbb float64
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		da, db := a[i]-ma, b[i]-mb
		sab += float64(da * db)
		saa += float64(da * da)
		sbb += float64(db * db)
	}
	if saa == 0 || sbb == 0 {
		return math.NaN(), nil
	}
	return sab / (math.Sqrt(saa) * math.Sqrt(sbb)), nil
}

// nonNegative returns the first complete-pair element below zero as a
// *DomainError, else nil.
func nonNegative(name string, a, b []float64) error {
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		for _, v := range [2]float64{a[i], b[i]} {
			if v < 0 {
				return &DomainError{Metric: name, Index: i, Value: v, Reason: ReasonNegative}
			}
		}
	}
	return nil
}

// closure sums each side over the complete pairs.
func closure(a, b []float64) (sa, sb float64, n int) {
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		sa += a[i]
		sb += b[i]
		n++
	}
	return sa, sb, n
}

// evalHellinger closes each side over the complete pairs and is
// sqrt(Σ(√(a/Σa) − √(b/Σb))² / 2), in [0, 1]. NaN with no complete pair
// or a zero total on either side.
func evalHellinger(d *definition, _ Form, a, b []float64, _ *Stats) (float64, error) {
	if err := nonNegative(d.name, a, b); err != nil {
		return math.NaN(), err
	}
	sa, sb, n := closure(a, b)
	if n == 0 || sa == 0 || sb == 0 {
		return math.NaN(), nil
	}
	var s float64
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		diff := math.Sqrt(a[i]/sa) - math.Sqrt(b[i]/sb)
		s += float64(diff * diff)
	}
	return math.Sqrt(s / 2), nil
}

// evalAitchison is the Euclidean distance between the centered
// log-ratio transforms of the complete pairs: with r_i = ln a_i − ln b_i
// and r̄ their mean, sqrt(Σ(r_i − r̄)²). A negative element is a
// DomainError (ReasonNegative); a zero part a DomainError
// (ReasonZero) — NaN for the row, which the consumer counts.
func evalAitchison(d *definition, _ Form, a, b []float64, _ *Stats) (float64, error) {
	if err := nonNegative(d.name, a, b); err != nil {
		return math.NaN(), err
	}
	var sr float64
	n := 0
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		for _, v := range [2]float64{a[i], b[i]} {
			if v == 0 {
				return math.NaN(), &DomainError{Metric: d.name, Index: i, Value: v, Reason: ReasonZero}
			}
		}
		sr += detmath.Log(a[i]) - detmath.Log(b[i])
		n++
	}
	if n == 0 {
		return math.NaN(), nil
	}
	mr := sr / float64(n)
	var s float64
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		diff := detmath.Log(a[i]) - detmath.Log(b[i]) - mr
		s += float64(diff * diff)
	}
	return math.Sqrt(s), nil
}

// evalSet reads 0 / 1 vectors as sets over the complete pairs and
// hands the counts to the set kernel; hamming's count is rescaled by
// p/p_observed. No complete pair gives NaN; an element other than 0 or
// 1 is a DomainError (ReasonNotBinary).
func evalSet(d *definition, f Form, a, b []float64, _ *Stats) (float64, error) {
	var c SetCounts
	n := 0
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		for _, v := range [2]float64{a[i], b[i]} {
			if v != 0 && v != 1 {
				return math.NaN(), &DomainError{Metric: d.name, Index: i, Value: v, Reason: ReasonNotBinary}
			}
		}
		n++
		inA, inB := a[i] == 1, b[i] == 1
		if inA {
			c.A++
		}
		if inB {
			c.B++
		}
		if inA && inB {
			c.Inter++
		}
	}
	if n == 0 {
		return math.NaN(), nil
	}
	v := d.sets(f, c)
	if d.scaled {
		v = rescale(v, n, len(a))
	}
	return v, nil
}

func setJaccard(_ Form, c SetCounts) float64 {
	union := c.A + c.B - c.Inter
	if union == 0 {
		return math.NaN()
	}
	return float64(c.Inter) / float64(union)
}

func setDice(_ Form, c SetCounts) float64 {
	den := c.A + c.B
	if den == 0 {
		return math.NaN()
	}
	return float64(2*c.Inter) / float64(den)
}

func setHamming(_ Form, c SetCounts) float64 {
	return float64(c.A + c.B - 2*c.Inter)
}

func setOverlap(_ Form, c SetCounts) float64 {
	den := min(c.A, c.B)
	if den == 0 {
		return math.NaN()
	}
	return float64(c.Inter) / float64(den)
}

// positiveStat validates a fitted per-element statistic: present, the
// vector's length, every entry finite and > 0.
func positiveStat(name string, fit Fit, v []float64, p int) error {
	if v == nil {
		return &StatsError{Metric: name, Stat: fit, Index: -1, Reason: ReasonMissing}
	}
	if len(v) != p {
		return &StatsError{Metric: name, Stat: fit, Index: -1, Reason: ReasonLength}
	}
	for i, x := range v {
		if !(x > 0) || math.IsInf(x, 0) {
			return &StatsError{Metric: name, Stat: fit, Index: i, Reason: ReasonInvalid}
		}
	}
	return nil
}

// evalStdEuclidean is sqrt(Σ((a−b)/sd)²) over the complete pairs, the
// sum rescaled by p/p_observed.
func evalStdEuclidean(d *definition, _ Form, a, b []float64, s *Stats) (float64, error) {
	var sd []float64
	if s != nil {
		sd = s.SD
	}
	if err := positiveStat(d.name, d.fit, sd, len(a)); err != nil {
		return math.NaN(), err
	}
	var sum float64
	n := 0
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		z := (a[i] - b[i]) / sd[i]
		sum += float64(z * z)
		n++
	}
	if n == 0 {
		return math.NaN(), nil
	}
	return math.Sqrt(rescale(sum, n, len(a))), nil
}

// evalMahalanobis is √(dᵀ S⁻¹ d), d = a − b, over the fitted inverse
// covariance, row by row (Σ_i d_i · Σ_j S⁻¹_ij d_j, both sums left to
// right). Complete: any missing element gives NaN. A rounding-negative
// quadratic form is 0.
func evalMahalanobis(d *definition, _ Form, a, b []float64, s *Stats) (float64, error) {
	if s == nil || s.InverseCovariance == nil {
		return math.NaN(), &StatsError{Metric: d.name, Stat: d.fit, Index: -1, Reason: ReasonMissing}
	}
	inv := s.InverseCovariance
	p := len(a)
	if inv.N() != p {
		return math.NaN(), &StatsError{Metric: d.name, Stat: d.fit, Index: -1, Reason: ReasonLength}
	}
	if p == 0 {
		return math.NaN(), nil
	}
	diff := make([]float64, p)
	for j := range diff {
		if missing(a, b, j) {
			return math.NaN(), nil
		}
		diff[j] = a[j] - b[j]
	}
	var q float64
	for i := 0; i < p; i++ {
		var row float64
		for j := 0; j < p; j++ {
			row += float64(inv.At(i, j) * diff[j])
		}
		q += float64(diff[i] * row)
	}
	if q < 0 {
		q = 0
	}
	return math.Sqrt(q), nil
}

// evalChiSquare closes each side over the complete pairs into profiles
// and is sqrt(Σ(a/Σa − b/Σb)² / mass) over the fitted column masses
// (the correspondence-analysis distance between row profiles). NaN with
// no complete pair or a zero total on either side.
func evalChiSquare(d *definition, _ Form, a, b []float64, s *Stats) (float64, error) {
	var masses []float64
	if s != nil {
		masses = s.ColumnMasses
	}
	if err := positiveStat(d.name, d.fit, masses, len(a)); err != nil {
		return math.NaN(), err
	}
	if err := nonNegative(d.name, a, b); err != nil {
		return math.NaN(), err
	}
	sa, sb, n := closure(a, b)
	if n == 0 || sa == 0 || sb == 0 {
		return math.NaN(), nil
	}
	var sum float64
	for i := range a {
		if missing(a, b, i) {
			continue
		}
		diff := a[i]/sa - b[i]/sb
		sum += float64(diff*diff) / masses[i]
	}
	return math.Sqrt(sum), nil
}
