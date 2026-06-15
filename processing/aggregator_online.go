package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
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
	a.frozenSum = out
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
		a.frozenSum = 0
		a.sum = 0
		return 0, nil
	}
	a.frozenSum = a.sum
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
		a.frozenMin = 0
		return 0, nil
	}
	out := a.min
	a.frozenMin = out
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
		a.frozenMax = 0
		return 0, nil
	}
	out := a.max
	a.frozenMax = out
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
		a.frozenMin, a.frozenMax = 0, 0
		return 0, nil
	}
	a.frozenMin, a.frozenMax = a.min, a.max
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
		a.frozenN, a.frozenMean, a.frozenM2 = 0, 0, 0
		return 0, nil
	}
	a.frozenN, a.frozenMean, a.frozenM2 = a.n, a.mean, a.m2
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
		a.frozenN, a.frozenMean, a.frozenM2 = 0, 0, 0
		return 0, nil
	}
	a.frozenN, a.frozenMean, a.frozenM2 = a.n, a.mean, a.m2
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
	a.frozenN, a.frozenMean, a.frozenM2, a.frozenM3 = a.n, a.mean, a.m2, a.m3
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
	a.frozenN, a.frozenMean, a.frozenM2, a.frozenM3, a.frozenM4 = a.n, a.mean, a.m2, a.m3, a.m4
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
	// Stamp the frozen mirror before nilling the live set so
	// Components() reads the post-finalize cardinality after the
	// streaming-path reset.
	a.frozenCardinality = len(a.set)
	a.frozenFinalized = true
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
	a.frozenFinalized = true
	if len(a.counts) == 0 {
		a.frozenDistinct = 0
		a.frozenModeValue = 0
		a.frozenModeCount = 0
		a.counts = nil
		return 0, nil
	}
	maxCount := 0
	for _, c := range a.counts {
		if c > maxCount {
			maxCount = c
		}
	}
	// Mode value: smallest among ties — matches modeAggregator's
	// deterministic ordering so the streaming and buffered paths
	// emit byte-identical Components{}.
	var modeValue float64
	first := true
	for v, c := range a.counts {
		if c == maxCount {
			if first || v < modeValue {
				modeValue = v
				first = false
			}
		}
	}
	a.frozenDistinct = len(a.counts)
	a.frozenModeValue = modeValue
	a.frozenModeCount = maxCount
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
	a.frozenFinalized = true
	if len(a.counts) == 0 {
		a.frozenValue = 0
		a.frozenCount = 0
		a.frozenDistinct = 0
		a.frozenTieCount = 0
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
	tieCount := 0
	for v, c := range a.counts {
		if c == maxCount {
			tieCount++
			if first || v < result {
				result = v
				first = false
			}
		}
	}
	a.frozenValue = result
	a.frozenCount = maxCount
	a.frozenDistinct = len(a.counts)
	a.frozenTieCount = tieCount
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

// --- Mergeable implementations (per-shard parallel reduce) ---
//
// MergeOnline folds another aggregator instance's state into the
// receiver. Implementations assume the other instance was constructed
// from the same Aggregation spec; the per-shard parallel orchestrator
// in service/shard_reduce.go enforces this invariant. A type-mismatched
// other returns PROCESSING_INTERNAL — a programming bug, not a runtime
// user-input error.

func mergeTypeMismatch(receiver string) error {
	return errors.NewCodedError(errors.PROCESSING_INTERNAL,
		"MergeOnline received incompatible aggregator type for "+receiver)
}

func (a *countAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*countAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_COUNT")
	}
	a.n += b.n
	return nil
}

func (a *sumAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*sumAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_SUM")
	}
	a.sum += b.sum
	return nil
}

func (a *averageAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*averageAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_AVERAGE")
	}
	a.sum += b.sum
	a.n += b.n
	return nil
}

func (a *minAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*minAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_MIN")
	}
	if !b.seen {
		return nil
	}
	if !a.seen || b.min < a.min {
		a.min = b.min
		a.seen = true
	}
	return nil
}

func (a *maxAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*maxAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_MAX")
	}
	if !b.seen {
		return nil
	}
	if !a.seen || b.max > a.max {
		a.max = b.max
		a.seen = true
	}
	return nil
}

func (a *rangeAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*rangeAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_RANGE")
	}
	if !b.seen {
		return nil
	}
	if !a.seen {
		a.min, a.max = b.min, b.max
		a.seen = true
		return nil
	}
	if b.min < a.min {
		a.min = b.min
	}
	if b.max > a.max {
		a.max = b.max
	}
	return nil
}

// varianceAggregator uses the Chan-Welford parallel formula to merge
// two (n, mean, M2) partials:
//
//	n      = n_a + n_b
//	delta  = mean_b - mean_a
//	mean   = mean_a + delta * n_b / n
//	M2     = M2_a + M2_b + delta^2 * n_a * n_b / n
//
// The result equals the single-pass Welford output to within ULP on
// well-conditioned inputs; goldens use a tolerance comparison.
func (a *varianceAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*varianceAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_VARIANCE")
	}
	mergeWelford(&a.n, &a.mean, &a.m2, b.n, b.mean, b.m2)
	return nil
}

func (a *stdDevAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*stdDevAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_STDDEV")
	}
	mergeWelford(&a.n, &a.mean, &a.m2, b.n, b.mean, b.m2)
	return nil
}

// mergeWelford applies the Chan-Welford parallel reduction in place on
// the receiver's (n, mean, M2). When the incoming partial has zero
// observations the receiver is unchanged; when the receiver is empty
// the incoming partial wins outright.
func mergeWelford(nA *int64, meanA *float64, m2A *float64, nB int64, meanB float64, m2B float64) {
	if nB == 0 {
		return
	}
	if *nA == 0 {
		*nA = nB
		*meanA = meanB
		*m2A = m2B
		return
	}
	total := *nA + nB
	delta := meanB - *meanA
	newMean := *meanA + delta*float64(nB)/float64(total)
	newM2 := *m2A + m2B + delta*delta*float64(*nA)*float64(nB)/float64(total)
	*nA = total
	*meanA = newMean
	*m2A = newM2
}

func (a *frequencyAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*frequencyAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_FREQUENCY")
	}
	if len(b.counts) == 0 {
		return nil
	}
	if a.counts == nil {
		a.counts = make(map[float64]int, len(b.counts))
	}
	for v, c := range b.counts {
		a.counts[v] += c
	}
	return nil
}

func (a *modeAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*modeAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_MODE")
	}
	if len(b.counts) == 0 {
		return nil
	}
	if a.counts == nil {
		a.counts = make(map[float64]int, len(b.counts))
	}
	for v, c := range b.counts {
		a.counts[v] += c
	}
	return nil
}

func (a *distinctCountAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*distinctCountAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_DISTINCT_COUNT")
	}
	if len(b.set) == 0 {
		return nil
	}
	if a.set == nil {
		a.set = make(map[float64]struct{}, len(b.set))
	}
	for v := range b.set {
		a.set[v] = struct{}{}
	}
	return nil
}

func (a *nullCountAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*nullCountAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_NULL_COUNT")
	}
	a.nNull += b.nNull
	return nil
}
