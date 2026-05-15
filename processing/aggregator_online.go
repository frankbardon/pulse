package processing

import (
	"math"
)

// This file holds OnlineAggregator implementations attached to the
// existing aggregator structs from aggregator.go. An aggregator that
// implements OnlineAggregator can run in the streaming path of the
// processor (single-pass over the iterator, no full-dataset buffering).
//
// Aggregators that fundamentally need the full value set (sort-based:
// MEDIAN, PERCENTILE; two-pass: ZSCORE) intentionally do NOT implement
// OnlineAggregator. The orchestrator falls back to the buffered path
// whenever any aggregation in the request is non-online.
//
// Numerical stability for variance/stddev/skewness/kurtosis uses the
// Welford / Welford-Pébaÿ recurrence over central moments M2, M3, M4
// so the streaming output matches the buffered (two-pass) output to
// float64 precision on well-conditioned inputs.

// --- Count (online) ---

func (a *countAggregator) UpdateRow(r *Record, field string) error {
	if _, ok := r.NumericValue(field); ok {
		a.n++
	}
	return nil
}

func (a *countAggregator) Finalize() (float64, error) {
	out := float64(a.n)
	a.n = 0
	return out, nil
}

// --- Sum (online) ---

func (a *sumAggregator) UpdateRow(r *Record, field string) error {
	if v, ok := r.NumericValue(field); ok {
		a.sum += v
	}
	return nil
}

func (a *sumAggregator) Finalize() (float64, error) {
	out := a.sum
	a.sum = 0
	return out, nil
}

// --- Average (online) ---

func (a *averageAggregator) UpdateRow(r *Record, field string) error {
	if v, ok := r.NumericValue(field); ok {
		a.sum += v
		a.n++
	}
	return nil
}

func (a *averageAggregator) Finalize() (float64, error) {
	if a.n == 0 {
		a.sum = 0
		return 0, nil
	}
	out := a.sum / float64(a.n)
	a.sum, a.n = 0, 0
	return out, nil
}

// --- Min (online) ---

func (a *minAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if !a.seen || v < a.min {
		a.min = v
		a.seen = true
	}
	return nil
}

func (a *minAggregator) Finalize() (float64, error) {
	if !a.seen {
		return 0, nil
	}
	out := a.min
	a.min, a.seen = 0, false
	return out, nil
}

// --- Max (online) ---

func (a *maxAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if !a.seen || v > a.max {
		a.max = v
		a.seen = true
	}
	return nil
}

func (a *maxAggregator) Finalize() (float64, error) {
	if !a.seen {
		return 0, nil
	}
	out := a.max
	a.max, a.seen = 0, false
	return out, nil
}

// --- Range (online) ---

func (a *rangeAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if !a.seen {
		a.min, a.max = v, v
		a.seen = true
		return nil
	}
	if v < a.min {
		a.min = v
	}
	if v > a.max {
		a.max = v
	}
	return nil
}

func (a *rangeAggregator) Finalize() (float64, error) {
	if !a.seen {
		return 0, nil
	}
	out := a.max - a.min
	a.min, a.max, a.seen = 0, 0, false
	return out, nil
}

// --- Variance (online, Welford) ---
//
// Welford's algorithm:
//   n      += 1
//   delta   = x - mean
//   mean   += delta / n
//   delta2  = x - mean
//   M2     += delta * delta2
// Population variance = M2 / n.

func (a *varianceAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	a.n++
	delta := v - a.mean
	a.mean += delta / float64(a.n)
	delta2 := v - a.mean
	a.m2 += delta * delta2
	return nil
}

func (a *varianceAggregator) Finalize() (float64, error) {
	if a.n == 0 {
		return 0, nil
	}
	out := a.m2 / float64(a.n)
	a.n, a.mean, a.m2 = 0, 0, 0
	return out, nil
}

// --- StdDev (online, Welford → sqrt) ---

func (a *stdDevAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	a.n++
	delta := v - a.mean
	a.mean += delta / float64(a.n)
	delta2 := v - a.mean
	a.m2 += delta * delta2
	return nil
}

func (a *stdDevAggregator) Finalize() (float64, error) {
	if a.n == 0 {
		return 0, nil
	}
	out := math.Sqrt(a.m2 / float64(a.n))
	a.n, a.mean, a.m2 = 0, 0, 0
	return out, nil
}

// --- Skewness (online, Welford-Pébaÿ central moments through M3) ---
//
// Uses the standard recurrences for higher central moments:
//   delta     = x - mean
//   delta_n   = delta / n
//   delta_n2  = delta_n^2
//   term1     = delta * delta_n * (n-1)
//   M3       += term1 * delta_n * (n-2) - 3 * delta_n * M2
//   M2       += term1
//   mean     += delta_n
//
// Skewness = M3 / n / (M2/n)^1.5 = (M3 * sqrt(n)) / M2^1.5
// Buffered version returns mean of ((x-mean)/sd)^3 == M3/(n*sd^3).

func (a *skewnessAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	a.n++
	n := float64(a.n)
	delta := v - a.mean
	deltaN := delta / n
	term1 := delta * deltaN * (n - 1)
	a.m3 += term1*deltaN*(n-2) - 3*deltaN*a.m2
	a.m2 += term1
	a.mean += deltaN
	return nil
}

func (a *skewnessAggregator) Finalize() (float64, error) {
	if a.n <= 1 || a.m2 == 0 {
		a.n, a.mean, a.m2, a.m3 = 0, 0, 0, 0
		return 0, nil
	}
	n := float64(a.n)
	// Population variance and stddev.
	variance := a.m2 / n
	sd := math.Sqrt(variance)
	if sd == 0 {
		a.n, a.mean, a.m2, a.m3 = 0, 0, 0, 0
		return 0, nil
	}
	// Match buffered: mean of ((x-m)/sd)^3 = M3 / (n * sd^3)
	out := a.m3 / (n * sd * sd * sd)
	a.n, a.mean, a.m2, a.m3 = 0, 0, 0, 0
	return out, nil
}

// --- Kurtosis (online, Welford-Pébaÿ through M4) ---
//
// Recurrences (extending the M3 recurrence):
//   delta     = x - mean
//   delta_n   = delta / n
//   delta_n2  = delta_n^2
//   term1     = delta * delta_n * (n-1)
//   M4       += term1 * delta_n2 * (n^2 - 3n + 3)
//             + 6 * delta_n2 * M2
//             - 4 * delta_n * M3
//   M3       += term1 * delta_n * (n-2) - 3 * delta_n * M2
//   M2       += term1
//   mean     += delta_n
//
// Excess kurtosis = M4/(n*sd^4) - 3 == buffered output.

func (a *kurtosisAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	a.n++
	n := float64(a.n)
	delta := v - a.mean
	deltaN := delta / n
	deltaN2 := deltaN * deltaN
	term1 := delta * deltaN * (n - 1)
	a.m4 += term1*deltaN2*(n*n-3*n+3) + 6*deltaN2*a.m2 - 4*deltaN*a.m3
	a.m3 += term1*deltaN*(n-2) - 3*deltaN*a.m2
	a.m2 += term1
	a.mean += deltaN
	return nil
}

func (a *kurtosisAggregator) Finalize() (float64, error) {
	if a.n <= 1 || a.m2 == 0 {
		a.n, a.mean, a.m2, a.m3, a.m4 = 0, 0, 0, 0, 0
		return 0, nil
	}
	n := float64(a.n)
	variance := a.m2 / n
	if variance == 0 {
		a.n, a.mean, a.m2, a.m3, a.m4 = 0, 0, 0, 0, 0
		return 0, nil
	}
	out := a.m4/(n*variance*variance) - 3
	a.n, a.mean, a.m2, a.m3, a.m4 = 0, 0, 0, 0, 0
	return out, nil
}

// --- DistinctCount (online: running set) ---

func (a *distinctCountAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if a.set == nil {
		a.set = make(map[float64]struct{})
	}
	a.set[v] = struct{}{}
	return nil
}

func (a *distinctCountAggregator) Finalize() (float64, error) {
	out := float64(len(a.set))
	a.set = nil
	return out, nil
}

// --- Frequency (online: running histogram, returns max count) ---

func (a *frequencyAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if a.counts == nil {
		a.counts = make(map[float64]int)
	}
	a.counts[v]++
	return nil
}

func (a *frequencyAggregator) Finalize() (float64, error) {
	maxCount := 0
	for _, c := range a.counts {
		if c > maxCount {
			maxCount = c
		}
	}
	a.counts = nil
	return float64(maxCount), nil
}

// --- Mode (online: running histogram, returns smallest most-frequent value) ---

func (a *modeAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if a.counts == nil {
		a.counts = make(map[float64]int)
	}
	a.counts[v]++
	return nil
}

func (a *modeAggregator) Finalize() (float64, error) {
	if len(a.counts) == 0 {
		a.counts = nil
		return 0, nil
	}
	maxCount := 0
	for _, c := range a.counts {
		if c > maxCount {
			maxCount = c
		}
	}
	var result float64
	first := true
	for v, c := range a.counts {
		if c == maxCount {
			if first || v < result {
				result = v
				first = false
			}
		}
	}
	a.counts = nil
	return result, nil
}

// --- NullCount (online) ---

func (a *nullCountAggregator) UpdateRow(r *Record, field string) error {
	if _, ok := r.NumericValue(field); !ok {
		a.nNull++
	}
	return nil
}

func (a *nullCountAggregator) Finalize() (float64, error) {
	out := float64(a.nNull)
	a.nNull = 0
	return out, nil
}
