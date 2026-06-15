package processing

import (
	"encoding/json"
	"math"
	"sort"
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// float64BufPool reuses []float64 working buffers across aggregations.
// Buffers obtained from getFloat64Buf must be returned via putFloat64Buf
// after use. Returned buffers have length 0; capacity is preserved.
var float64BufPool = sync.Pool{
	New: func() any {
		// Pool stores *[]float64 to avoid allocating a slice header on each Put.
		s := make([]float64, 0, 64)
		return &s
	},
}

// acquireFloat64Buf retrieves a buffer pointer from the pool with at least
// the requested capacity. The slice's length is 0. The returned pointer must
// be returned via releaseFloat64Buf.
func acquireFloat64Buf(minCap int) *[]float64 {
	bp := float64BufPool.Get().(*[]float64)
	buf := (*bp)[:0]
	if cap(buf) < minCap {
		buf = make([]float64, 0, minCap)
	}
	*bp = buf
	return bp
}

// releaseFloat64Buf returns a buffer pointer to the pool. The buffer's
// underlying capacity is preserved; the length is reset to 0.
func releaseFloat64Buf(bp *[]float64) {
	if bp == nil {
		return
	}
	*bp = (*bp)[:0]
	float64BufPool.Put(bp)
}

// collectCache memoizes collected non-null float64 values per field for the
// duration of a single Process call. The cache owns the underlying buffer
// pointers; release() returns them all to the pool. Slices handed out by
// get() are read-only — aggregators that mutate (sort, partition) must copy
// the slice before mutating.
type collectCache struct {
	bufs map[string]*[]float64
}

func newCollectCache() *collectCache {
	return &collectCache{bufs: make(map[string]*[]float64)}
}

// get returns the cached non-null float64 slice for field, populating the
// cache on first access. The returned slice must NOT be mutated by the
// caller; sort-dependent aggregators must copy first.
func (c *collectCache) get(records []*Record, field string) []float64 {
	if c == nil {
		return collectValues(records, field)
	}
	if bp, ok := c.bufs[field]; ok {
		return *bp
	}
	bp := acquireFloat64Buf(len(records))
	buf := *bp
	for _, r := range records {
		if v, ok := r.NumericValue(field); ok {
			buf = append(buf, v)
		}
	}
	*bp = buf
	c.bufs[field] = bp
	return buf
}

// release returns every buffer held by the cache to the pool.
func (c *collectCache) release() {
	if c == nil {
		return
	}
	for k, bp := range c.bufs {
		releaseFloat64Buf(bp)
		delete(c.bufs, k)
	}
}

// valueAggregator is the unexported sibling of Aggregator that operates on a
// pre-collected []float64 slice. Built-in aggregators implement this so the
// orchestrator can collect each field's values once per Process call and
// share the slice across aggregations.
//
// Implementations MUST NOT mutate the input slice. Aggregators that need a
// sorted view (median, percentile) must copy the slice before sorting.
type valueAggregator interface {
	aggregateValues(vals []float64) (float64, error)
}

// collectValues extracts non-null float64 values from records for the given field.
func collectValues(records []*Record, field string) []float64 {
	vals := make([]float64, 0, len(records))
	for _, r := range records {
		if v, ok := r.NumericValue(field); ok {
			vals = append(vals, v)
		}
	}
	return vals
}

// mean computes the arithmetic mean of a float64 slice.
func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

// populationVariance computes population variance.
func populationVariance(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := mean(vals)
	sumSq := 0.0
	for _, v := range vals {
		d := v - m
		sumSq += d * d
	}
	return sumSq / float64(len(vals))
}

// populationStdDev computes population standard deviation.
func populationStdDev(vals []float64) float64 {
	return math.Sqrt(populationVariance(vals))
}

// sumSquaredDeviations returns the M2 Welford moment for the buffered
// path: sum_i (v_i - m)^2 where m is the (pre-computed) mean. Used by
// Components() emission on the variance / stddev aggregators so the
// frozen state matches the streaming Welford bucket numerically.
// Empty input returns 0 (matches the M2=0 initial state).
func sumSquaredDeviations(vals []float64, m float64) float64 {
	sum := 0.0
	for _, v := range vals {
		d := v - m
		sum += d * d
	}
	return sum
}

// sumDeviationPowers returns (M2, M3, M4) — the second, third, and
// fourth central moments — for the buffered path. M4 is computed only
// when wantM4 is true (skewness needs M2 and M3 only). The buffered
// versions match the Chan-Welford parallel-merged streaming moments to
// within ULP on well-conditioned inputs; bit-exact on a single-pass
// run. Used by Components() emission on the skewness / kurtosis
// aggregators.
func sumDeviationPowers(vals []float64, m float64, wantM4 bool) (m2, m3, m4 float64) {
	for _, v := range vals {
		d := v - m
		d2 := d * d
		m2 += d2
		m3 += d2 * d
		if wantM4 {
			m4 += d2 * d2
		}
	}
	return m2, m3, m4
}

// --- Count ---

// countAggregator counts non-null values for a field. The streaming path
// uses the n field; the buffered path ignores it (collectValues + len).
//
// Components emission is floor-only — see MetaAggregator wiring below;
// the scalar value IS the universal floor n, so no operator-specific
// keys ride.
type countAggregator struct {
	n int64
}

func newCountAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &countAggregator{}, nil
}

func (a *countAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *countAggregator) aggregateValues(vals []float64) (float64, error) {
	return float64(len(vals)), nil
}

// --- Sum ---

// sumAggregator sums non-null values. The sum field is the running
// accumulator on the streaming path; the buffered path stamps it via
// aggregateValues so Components() can read it post-Aggregate. The
// frozenSum mirror survives Finalize's reset so the orchestrator's
// post-Finalize Components() call sees the final value.
type sumAggregator struct {
	sum       float64
	frozenSum float64
}

func newSumAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &sumAggregator{}, nil
}

func (a *sumAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *sumAggregator) aggregateValues(vals []float64) (float64, error) {
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	a.frozenSum = sum
	return sum, nil
}

// --- Average ---

// averageAggregator tracks running sum and count for streaming mean.
// frozenSum mirrors the final sum so Components() survives Finalize's
// reset; the buffered path stamps it via aggregateValues.
type averageAggregator struct {
	sum       float64
	n         int64
	frozenSum float64
}

func newAverageAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &averageAggregator{}, nil
}

func (a *averageAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *averageAggregator) aggregateValues(vals []float64) (float64, error) {
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	a.frozenSum = sum
	if len(vals) == 0 {
		return 0, nil
	}
	return sum / float64(len(vals)), nil
}

// --- Min ---

// frozenMin survives Finalize's reset so Components() returns the same
// value an immediately-prior Finalize emitted; the buffered path
// populates it via aggregateValues.
type minAggregator struct {
	min       float64
	seen      bool
	frozenMin float64
}

func newMinAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &minAggregator{}, nil
}

func (a *minAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *minAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) == 0 {
		a.frozenMin = 0
		return 0, nil
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	a.frozenMin = m
	return m, nil
}

// --- Max ---

// frozenMax mirrors max post-Finalize for Components().
type maxAggregator struct {
	max       float64
	seen      bool
	frozenMax float64
}

func newMaxAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &maxAggregator{}, nil
}

func (a *maxAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *maxAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) == 0 {
		a.frozenMax = 0
		return 0, nil
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	a.frozenMax = m
	return m, nil
}

// --- StdDev ---

// stdDevAggregator tracks Welford's running mean and M2 for streaming
// computation. Buffered path ignores these and uses populationStdDev.
//
// frozenN/frozenMean/frozenM2 mirror the post-finalize Welford state
// (population denominator: variance = m2 / n) so Components() can emit
// {mean, m2, variance, stddev} after the streaming Finalize-reset wipes
// the live (n, mean, m2). The buffered path stamps the same mirrors
// via aggregateValues so Components() works on both code paths.
type stdDevAggregator struct {
	n          int64
	mean       float64
	m2         float64
	frozenN    int64
	frozenMean float64
	frozenM2   float64
}

func newStdDevAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &stdDevAggregator{}, nil
}

func (a *stdDevAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *stdDevAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenN = int64(len(vals))
	a.frozenMean = mean(vals)
	a.frozenM2 = sumSquaredDeviations(vals, a.frozenMean)
	return populationStdDev(vals), nil
}

// --- Range ---

// frozenMin/frozenMax mirror the bracketing extrema post-Finalize so
// Components() can emit them; the buffered path populates via
// aggregateValues.
type rangeAggregator struct {
	min, max             float64
	seen                 bool
	frozenMin, frozenMax float64
}

func newRangeAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &rangeAggregator{}, nil
}

func (a *rangeAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *rangeAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) == 0 {
		a.frozenMin, a.frozenMax = 0, 0
		return 0, nil
	}
	minV, maxV := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	a.frozenMin, a.frozenMax = minV, maxV
	return maxV - minV, nil
}

// --- Frequency ---
//
// frequencyAggregator's scalar return is the modal count; Components()
// adds the per-value cardinality plus the modal value (smallest-value
// tie-break, matching AGG_MODE's deterministic ordering). frozenDistinct
// / frozenModeValue / frozenModeCount mirror the post-Aggregate /
// post-Finalize state so Components() works on both buffered and
// streaming code paths — the streaming Finalize step nils out `counts`
// after computing, so the frozen mirrors are the source of truth.
type frequencyAggregator struct {
	counts          map[float64]int
	frozenDistinct  int
	frozenModeValue float64
	frozenModeCount int
	frozenFinalized bool
}

func newFrequencyAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &frequencyAggregator{}, nil
}

func (a *frequencyAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *frequencyAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenFinalized = true
	if len(vals) == 0 {
		a.frozenDistinct = 0
		a.frozenModeValue = 0
		a.frozenModeCount = 0
		return 0, nil
	}
	counts := make(map[float64]int)
	for _, v := range vals {
		counts[v]++
	}
	maxCount := 0
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
	}
	// Mode value: smallest among ties — matches modeAggregator's
	// deterministic ordering so AGG_MODE and AGG_FREQUENCY agree on
	// the winner over the same input set.
	var modeValue float64
	first := true
	for v, c := range counts {
		if c == maxCount {
			if first || v < modeValue {
				modeValue = v
				first = false
			}
		}
	}
	a.frozenDistinct = len(counts)
	a.frozenModeValue = modeValue
	a.frozenModeCount = maxCount
	return float64(maxCount), nil
}

// --- ZScore (aggregator) ---

// zscoreAggregator is the buffered-only AGG_ZSCORE: it folds every
// non-null value into a population mean and stddev, then returns the
// mean of the standardized scores (always 0 by definition; emitted for
// scalar-channel compatibility). Components() surfaces the population
// parameters that ARE final, plus the FINAL row's value and its z-
// score, so the operator still produces a usable components map under
// the "last value seen" semantics the story documents for an
// aggregator whose scalar payload is information-free.
//
// frozenPopMean/frozenPopStdDev/frozenTargetValue mirror the post-
// Aggregate state so Components() can emit
// {pop_mean, pop_stddev, target_value, zscore} after the buffered path
// (the only path: AGG_ZSCORE is non-streamable by registry).
type zscoreAggregator struct {
	frozenPopMean      float64
	frozenPopStdDev    float64
	frozenTargetValue  float64
	frozenZScore       float64
	frozenHasFinalized bool
}

func newZScoreAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &zscoreAggregator{}, nil
}

func (a *zscoreAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *zscoreAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenHasFinalized = true
	if len(vals) == 0 {
		a.frozenPopMean, a.frozenPopStdDev, a.frozenTargetValue, a.frozenZScore = 0, 0, 0, 0
		return 0, nil
	}
	m := mean(vals)
	sd := populationStdDev(vals)
	a.frozenPopMean = m
	a.frozenPopStdDev = sd
	a.frozenTargetValue = vals[len(vals)-1]
	if sd == 0 {
		a.frozenZScore = 0
		return 0, nil
	}
	// Final row's z-score — the story-documented "last value seen"
	// component for the aggregator whose scalar payload is the mean
	// standardized score (always 0 by definition).
	a.frozenZScore = (a.frozenTargetValue - m) / sd
	// Mean z-score is always 0 by definition, but compute it properly
	zSum := 0.0
	for _, v := range vals {
		zSum += (v - m) / sd
	}
	return zSum / float64(len(vals)), nil
}

// --- Median ---
//
// medianAggregator's scalar return is the median of the non-null
// values for the named field. Components() surfaces the sorted-
// position indices used to bracket the median (position_low /
// position_high) plus the resolved median value itself. Both indices
// match for odd N (single-pivot path) and differ by 1 for even N
// (linear interpolation between the two middle positions). The
// aggregator declares ComponentsMergeability=None — the median cannot
// be computed incrementally without sorting the full value set, so the
// streaming path defers Components() emission until terminal buffered
// flush (E4-S4 wiring).
//
// frozenN / frozenMedian / frozenPositionLow / frozenPositionHigh
// mirror the post-Aggregate state so Components() returns a stable
// snapshot even on aggregator reuse. frozenFinalized distinguishes
// "ran on empty input" (frozenN==0 + frozenFinalized==true) from
// "never ran" (frozenFinalized==false) — both collapse to (nil, nil)
// per the universal-floor convention, but the flag keeps the contract
// inspectable in tests.
type medianAggregator struct {
	frozenFinalized   bool
	frozenN           int
	frozenMedian      float64
	frozenPositionLow int
	frozenPositionHigh int
}

func newMedianAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &medianAggregator{}, nil
}

func (a *medianAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *medianAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenFinalized = true
	a.frozenN = len(vals)
	if len(vals) == 0 {
		a.frozenMedian = 0
		a.frozenPositionLow = 0
		a.frozenPositionHigh = 0
		return 0, nil
	}
	// median sorts in place — copy first to avoid mutating a shared cached slice.
	work := make([]float64, len(vals))
	copy(work, vals)
	sort.Float64s(work)
	n := len(work)
	if n%2 == 1 {
		idx := n / 2
		a.frozenPositionLow = idx
		a.frozenPositionHigh = idx
		a.frozenMedian = work[idx]
		return work[idx], nil
	}
	lo := n/2 - 1
	hi := n / 2
	a.frozenPositionLow = lo
	a.frozenPositionHigh = hi
	a.frozenMedian = (work[lo] + work[hi]) / 2
	return a.frozenMedian, nil
}

// --- Variance ---

// varianceAggregator uses Welford's online recurrence in the streaming
// path. Buffered path uses populationVariance and does not read these.
//
// frozenN/frozenMean/frozenM2 mirror the post-finalize Welford state
// (population denominator: variance = m2 / n) so Components() can emit
// {mean, m2, variance} after the streaming Finalize-reset wipes the
// live (n, mean, m2). The buffered path stamps the same mirrors via
// aggregateValues so Components() works on both code paths.
type varianceAggregator struct {
	n          int64
	mean       float64
	m2         float64
	frozenN    int64
	frozenMean float64
	frozenM2   float64
}

func newVarianceAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &varianceAggregator{}, nil
}

func (a *varianceAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *varianceAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenN = int64(len(vals))
	a.frozenMean = mean(vals)
	a.frozenM2 = sumSquaredDeviations(vals, a.frozenMean)
	return populationVariance(vals), nil
}

// --- Mode ---
//
// modeAggregator's scalar return is the smallest-value tie-break
// winner among values sharing the maximal count. Components() adds
// the modal count, the distinct-value cardinality, and the tie_count
// (number of distinct values tied at the maximal count). frozenValue
// / frozenCount / frozenDistinct / frozenTieCount mirror the post-
// Aggregate / post-Finalize state so Components() works on both
// buffered and streaming code paths — the streaming Finalize step
// nils out `counts` after computing, so the frozen mirrors are the
// source of truth.
type modeAggregator struct {
	counts          map[float64]int
	frozenValue     float64
	frozenCount     int
	frozenDistinct  int
	frozenTieCount  int
	frozenFinalized bool
}

func newModeAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &modeAggregator{}, nil
}

func (a *modeAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *modeAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenFinalized = true
	if len(vals) == 0 {
		a.frozenValue = 0
		a.frozenCount = 0
		a.frozenDistinct = 0
		a.frozenTieCount = 0
		return 0, nil
	}
	counts := make(map[float64]int)
	for _, v := range vals {
		counts[v]++
	}
	maxCount := 0
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
	}
	// Among values with max frequency, return the smallest (deterministic tie-breaking).
	var result float64
	first := true
	tieCount := 0
	for v, c := range counts {
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
	a.frozenDistinct = len(counts)
	a.frozenTieCount = tieCount
	return result, nil
}

// --- Skewness ---

// skewnessAggregator uses the Welford-Pébaÿ recurrence through M3 for
// online streaming. Buffered path is independent.
//
// frozenN/frozenMean/frozenM2/frozenM3 mirror the post-finalize Welford
// moments so Components() can emit {mean, m2, m3, skewness} after the
// streaming Finalize-reset wipes the live state. The buffered path
// stamps the same mirrors via aggregateValues; skewness is the
// population moment estimator m3 / (n * sd^3) so the emitted value is
// bit-identical to the scalar return.
type skewnessAggregator struct {
	n          int64
	mean       float64
	m2         float64
	m3         float64
	frozenN    int64
	frozenMean float64
	frozenM2   float64
	frozenM3   float64
}

func newSkewnessAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &skewnessAggregator{}, nil
}

func (a *skewnessAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *skewnessAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenN = int64(len(vals))
	a.frozenMean = mean(vals)
	a.frozenM2, a.frozenM3, _ = sumDeviationPowers(vals, a.frozenMean, false)
	if len(vals) <= 1 {
		return 0, nil
	}
	sd := populationStdDev(vals)
	if sd == 0 {
		return 0, nil
	}
	n := float64(len(vals))
	sum := 0.0
	for _, v := range vals {
		sum += math.Pow((v-a.frozenMean)/sd, 3)
	}
	return sum / n, nil
}

// --- Kurtosis ---

// kurtosisAggregator uses the Welford-Pébaÿ recurrence through M4.
//
// frozenN/frozenMean/frozenM2/frozenM3/frozenM4 mirror the post-finalize
// Welford moments so Components() can emit {mean, m2, m3, m4, kurtosis}
// after the streaming Finalize-reset wipes the live state. The buffered
// path stamps the same mirrors via aggregateValues; kurtosis is the
// excess form m4 / (n * variance^2) - 3 so the emitted value is bit-
// identical to the scalar return.
type kurtosisAggregator struct {
	n          int64
	mean       float64
	m2         float64
	m3         float64
	m4         float64
	frozenN    int64
	frozenMean float64
	frozenM2   float64
	frozenM3   float64
	frozenM4   float64
}

func newKurtosisAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &kurtosisAggregator{}, nil
}

func (a *kurtosisAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *kurtosisAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenN = int64(len(vals))
	a.frozenMean = mean(vals)
	a.frozenM2, a.frozenM3, a.frozenM4 = sumDeviationPowers(vals, a.frozenMean, true)
	if len(vals) <= 1 {
		return 0, nil
	}
	sd := populationStdDev(vals)
	if sd == 0 {
		return 0, nil
	}
	n := float64(len(vals))
	sum := 0.0
	for _, v := range vals {
		sum += math.Pow((v-a.frozenMean)/sd, 4)
	}
	return sum/n - 3, nil
}

// --- Distinct Count ---
//
// distinctCountAggregator's scalar return is the cardinality of the
// per-value set. Components() surfaces the same cardinality under the
// {cardinality} key. frozenCardinality mirrors the post-Aggregate /
// post-Finalize state so Components() works on both buffered and
// streaming code paths — the streaming Finalize step nils out `set`
// after computing, so the frozen mirror is the source of truth.
type distinctCountAggregator struct {
	set               map[float64]struct{}
	frozenCardinality int
	frozenFinalized   bool
}

func newDistinctCountAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &distinctCountAggregator{}, nil
}

func (a *distinctCountAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *distinctCountAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenFinalized = true
	if len(vals) == 0 {
		a.frozenCardinality = 0
		return 0, nil
	}
	set := make(map[float64]struct{})
	for _, v := range vals {
		set[v] = struct{}{}
	}
	a.frozenCardinality = len(set)
	return float64(len(set)), nil
}

// --- Percentile ---
//
// percentileAggregator's scalar return is the configured percentile
// (default p50) of the non-null values for the named field, resolved
// via linear interpolation between the bracketing positions in the
// sorted value set. Components() surfaces (p, position, lower, upper,
// method, value) — `position` is the floor index used as the lower
// bracket, `lower` / `upper` are the bracketing values, `method` is
// the interpolation kind (always "linear" today), `value` mirrors the
// scalar return. The aggregator declares ComponentsMergeability=None
// — incrementing percentiles without a sorted view is not defensible,
// so the streaming path defers Components() emission until terminal
// buffered flush (E4-S4 wiring).
//
// frozen* mirrors hold the post-Aggregate state so Components()
// returns a stable snapshot. frozenFinalized distinguishes
// "ran on empty input" from "never ran" — both collapse to
// (nil, nil) per the universal-floor convention.
const percentileMethodLinear = "linear"

type percentileParams struct {
	Percentile float64 `json:"percentile"`
}

type percentileAggregator struct {
	percentile        float64
	frozenFinalized   bool
	frozenN           int
	frozenP           float64
	frozenPosition    int
	frozenLower       float64
	frozenUpper       float64
	frozenMethod      string
	frozenValue       float64
}

func newPercentileAggregator(agg *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	p := 50.0
	if len(agg.Params) > 0 {
		var params percentileParams
		if err := json.Unmarshal(agg.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "invalid percentile params: "+err.Error())
		}
		p = params.Percentile
	}
	if p < 0 || p > 100 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "percentile must be between 0 and 100")
	}
	return &percentileAggregator{percentile: p}, nil
}

func (a *percentileAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *percentileAggregator) aggregateValues(vals []float64) (float64, error) {
	a.frozenFinalized = true
	a.frozenN = len(vals)
	a.frozenP = a.percentile
	a.frozenMethod = percentileMethodLinear
	if len(vals) == 0 {
		a.frozenPosition = 0
		a.frozenLower = 0
		a.frozenUpper = 0
		a.frozenValue = 0
		return 0, nil
	}
	// percentile sorts in place — copy first to avoid mutating a shared cached slice.
	work := make([]float64, len(vals))
	copy(work, vals)
	sort.Float64s(work)
	n := len(work)
	if n == 1 {
		a.frozenPosition = 0
		a.frozenLower = work[0]
		a.frozenUpper = work[0]
		a.frozenValue = work[0]
		return work[0], nil
	}
	rank := a.percentile / 100.0 * float64(n-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	a.frozenPosition = lower
	a.frozenLower = work[lower]
	a.frozenUpper = work[upper]
	if lower == upper {
		a.frozenValue = work[lower]
		return work[lower], nil
	}
	a.frozenValue = work[lower] + (rank-float64(lower))*(work[upper]-work[lower])
	return a.frozenValue, nil
}

// --- Null Count ---

// nullCountAggregator counts records whose value for the named field is
// null (inverse of countAggregator, which counts non-null entries). The
// buffered path subtracts the non-null count from the total record
// count; the streaming path increments nNull on every UpdateRow where
// Record.NumericValue returns ok=false.
type nullCountAggregator struct {
	nNull int64
}

func newNullCountAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &nullCountAggregator{}, nil
}

// Aggregate walks the record set directly; the orchestrator's
// valueAggregator shortcut (collectCache) drops nulls before handing the
// slice over, so AGG_NULL_COUNT deliberately does NOT implement
// valueAggregator — it would always report zero.
func (a *nullCountAggregator) Aggregate(records []*Record, field string) (float64, error) {
	var nNull int64
	for _, r := range records {
		if _, ok := r.NumericValue(field); !ok {
			nNull++
		}
	}
	return float64(nNull), nil
}

// --- MetaAggregator implementations (per-operator components map) ---
//
// Each Components() returns ONLY the operator-specific keys declared in
// descriptor/capabilities_aggregators.go. The universal floor ({n,
// n_null}) is filled by the orchestrator from per-record bookkeeping,
// never re-emitted here. Floor-only operators (AGG_COUNT, AGG_NULL_COUNT)
// return (nil, nil) — their entire payload IS the universal floor.
//
// Compile-time interface locks below catch interface drift at build
// time: a `processing.MetaAggregator` cast that fails the build is a
// red flag the per-operator implementation has fallen out of sync with
// the sibling interface declared in processing/interfaces.go.

// Components returns nil — AGG_COUNT is a floor-only operator (its
// scalar value IS the universal floor n).
func (a *countAggregator) Components() (map[string]any, error) {
	return nil, nil
}

// Components returns {sum} — the running sum of contributing rows.
// Reads frozenSum so the streaming path's Finalize-reset does not
// erase the value before the orchestrator's Components() call.
func (a *sumAggregator) Components() (map[string]any, error) {
	return map[string]any{
		"sum": a.frozenSum,
	}, nil
}

// Components returns {sum} — mean is derivable as sum / n by callers
// that need it; the floor's n is the matching denominator. The schema
// (descriptor/capabilities_aggregators.go) intentionally exposes sum
// only — emitting mean here would duplicate the scalar result. Reads
// frozenSum so streaming Finalize-reset does not erase the value.
func (a *averageAggregator) Components() (map[string]any, error) {
	return map[string]any{
		"sum": a.frozenSum,
	}, nil
}

// Components returns {min} — the smallest contributing value. Empty
// input yields a zero-valued min; callers gate on floor n > 0. Reads
// frozenMin so streaming Finalize-reset does not erase the value.
func (a *minAggregator) Components() (map[string]any, error) {
	return map[string]any{
		"min": a.frozenMin,
	}, nil
}

// Components returns {max} — the largest contributing value. Empty
// input yields a zero-valued max; callers gate on floor n > 0. Reads
// frozenMax so streaming Finalize-reset does not erase the value.
func (a *maxAggregator) Components() (map[string]any, error) {
	return map[string]any{
		"max": a.frozenMax,
	}, nil
}

// Components returns {min, max} — the inputs to the scalar range
// (max - min). Empty input yields zero for both; callers gate on
// floor n > 0. Reads frozenMin/frozenMax so streaming Finalize-reset
// does not erase the values.
func (a *rangeAggregator) Components() (map[string]any, error) {
	return map[string]any{
		"min": a.frozenMin,
		"max": a.frozenMax,
	}, nil
}

// Components returns nil — AGG_NULL_COUNT is a floor-only operator
// (its scalar value IS the universal floor n_null).
func (a *nullCountAggregator) Components() (map[string]any, error) {
	return nil, nil
}

// --- Welford-family Components implementations (E1-S6) -----------
//
// The Welford-family aggregators — variance, stddev, skewness, kurtosis
// — share a common shape: streaming Finalize captures (n, mean, m2,
// [m3, m4]) into frozen mirrors before resetting the live state; the
// buffered path stamps the same mirrors via aggregateValues. Components
// reads the frozen state so the emit survives both code paths.
//
// Numerical contract: emitted values match the scalar return bit-for-
// bit on a single-pass run. The Chan-Welford parallel-merge path
// (MergeOnline) lands the merged moments in `a.n / a.mean / a.m2 /
// a.m3 / a.m4` before Finalize copies them to the frozen mirrors —
// every shard worker shares the same recurrence, so the components
// match the single-pass run within ULP on well-conditioned inputs.

// Components returns {mean, m2, variance} — the population Welford
// moments. variance = m2 / n; matches the scalar return of the
// AGG_VARIANCE aggregator (populationVariance). Empty input emits all
// zeros so the orchestrator's universal floor (n=0) is the source of
// truth for "no rows seen".
func (a *varianceAggregator) Components() (map[string]any, error) {
	if a.frozenN == 0 {
		return map[string]any{
			"mean":     0.0,
			"m2":       0.0,
			"variance": 0.0,
		}, nil
	}
	variance := a.frozenM2 / float64(a.frozenN)
	return map[string]any{
		"mean":     a.frozenMean,
		"m2":       a.frozenM2,
		"variance": variance,
	}, nil
}

// Components returns {mean, m2, variance, stddev} — adds the population
// stddev (sqrt(variance)) to the variance components. Matches the
// scalar return of AGG_STDDEV (populationStdDev).
func (a *stdDevAggregator) Components() (map[string]any, error) {
	if a.frozenN == 0 {
		return map[string]any{
			"mean":     0.0,
			"m2":       0.0,
			"variance": 0.0,
			"stddev":   0.0,
		}, nil
	}
	variance := a.frozenM2 / float64(a.frozenN)
	return map[string]any{
		"mean":     a.frozenMean,
		"m2":       a.frozenM2,
		"variance": variance,
		"stddev":   math.Sqrt(variance),
	}, nil
}

// Components returns {mean, m2, m3, skewness} — the population Welford
// moments through M3 plus the bias-uncorrected skewness estimator
// m3 / (n * sd^3). Matches the scalar return of AGG_SKEWNESS.
// Single-row / zero-variance inputs emit skewness=0 to match the
// scalar fallback.
func (a *skewnessAggregator) Components() (map[string]any, error) {
	if a.frozenN == 0 {
		return map[string]any{
			"mean":     0.0,
			"m2":       0.0,
			"m3":       0.0,
			"skewness": 0.0,
		}, nil
	}
	var skewness float64
	if a.frozenN > 1 && a.frozenM2 > 0 {
		n := float64(a.frozenN)
		variance := a.frozenM2 / n
		sd := math.Sqrt(variance)
		if sd > 0 {
			skewness = a.frozenM3 / (n * sd * sd * sd)
		}
	}
	return map[string]any{
		"mean":     a.frozenMean,
		"m2":       a.frozenM2,
		"m3":       a.frozenM3,
		"skewness": skewness,
	}, nil
}

// Components returns {mean, m2, m3, m4, kurtosis} — the population
// Welford moments through M4 plus the excess kurtosis estimator
// m4 / (n * variance^2) - 3. Matches the scalar return of
// AGG_KURTOSIS. Single-row / zero-variance inputs emit kurtosis=0 to
// match the scalar fallback.
func (a *kurtosisAggregator) Components() (map[string]any, error) {
	if a.frozenN == 0 {
		return map[string]any{
			"mean":     0.0,
			"m2":       0.0,
			"m3":       0.0,
			"m4":       0.0,
			"kurtosis": 0.0,
		}, nil
	}
	var kurtosis float64
	if a.frozenN > 1 && a.frozenM2 > 0 {
		n := float64(a.frozenN)
		variance := a.frozenM2 / n
		if variance > 0 {
			kurtosis = a.frozenM4/(n*variance*variance) - 3
		}
	}
	return map[string]any{
		"mean":     a.frozenMean,
		"m2":       a.frozenM2,
		"m3":       a.frozenM3,
		"m4":       a.frozenM4,
		"kurtosis": kurtosis,
	}, nil
}

// Components returns {pop_mean, pop_stddev, target_value, zscore} —
// the buffered-only AGG_ZSCORE's population summary plus the FINAL row
// processed and its standardized score under those population params.
// The scalar return of AGG_ZSCORE is the mean standardized score
// (always 0 by definition); Components surfaces the params that ARE
// final + last-value semantics documented on the type. Empty input
// emits zeros so the universal floor's n drives the gate.
func (a *zscoreAggregator) Components() (map[string]any, error) {
	if !a.frozenHasFinalized {
		return map[string]any{
			"pop_mean":     0.0,
			"pop_stddev":   0.0,
			"target_value": 0.0,
			"zscore":       0.0,
		}, nil
	}
	return map[string]any{
		"pop_mean":     a.frozenPopMean,
		"pop_stddev":   a.frozenPopStdDev,
		"target_value": a.frozenTargetValue,
		"zscore":       a.frozenZScore,
	}, nil
}

// --- Map-state Components implementations (E1-S7) ----------------
//
// The three map-state aggregators — AGG_DISTINCT_COUNT, AGG_MODE,
// AGG_FREQUENCY — maintain a per-value count map (or set) for their
// primary computation; Components() surfaces summary stats over that
// map. Each emits from the frozen mirror fields stamped by either the
// buffered path (aggregateValues) or the streaming path (Finalize),
// since both paths nil out the live map after computing.
//
// Categorical-decode FOLLOWUP: when the underlying field type is
// categorical_*, the float64 keys are encoded dictionary indices. The
// scalar return path also surfaces the raw float64 (the dictionary
// index, not the label) — Components() emits the same raw value so
// the two channels agree. Decoding to the human-visible label
// requires plumbing the schema dictionary into the aggregator and
// is left for a follow-up effort.

// Components returns {cardinality} — the number of distinct non-null
// values observed. Reads frozenCardinality so the streaming path's
// Finalize-reset does not erase the value before Components() runs.
// Empty input returns (nil, nil) so the orchestrator's universal
// floor (n=0) is the source of truth for "no rows seen", matching
// the empty-input convention used by Welford-family Rich().
func (a *distinctCountAggregator) Components() (map[string]any, error) {
	if a.frozenFinalized && a.frozenCardinality == 0 {
		// Distinguish "ran, saw nothing" from "never ran": when the
		// scalar path executed but found no rows, emit cardinality=0
		// so consumers can read the post-Aggregate state without
		// having to consult the floor independently.
		return map[string]any{"cardinality": 0}, nil
	}
	if !a.frozenFinalized {
		return nil, nil
	}
	return map[string]any{
		"cardinality": a.frozenCardinality,
	}, nil
}

// Components returns {value, count, distinct_count, tie_count} — the
// modal value (smallest-value tie-break), the modal count, the
// cardinality of the per-value count map, and the number of distinct
// values sharing the modal count. Reads frozen* mirrors so the
// streaming path's Finalize-reset does not erase the values before
// Components() runs. Empty input returns (nil, nil) so the
// orchestrator's universal floor (n=0) is the source of truth.
func (a *modeAggregator) Components() (map[string]any, error) {
	if !a.frozenFinalized || a.frozenCount == 0 {
		return nil, nil
	}
	return map[string]any{
		"value":          a.frozenValue,
		"count":          a.frozenCount,
		"distinct_count": a.frozenDistinct,
		"tie_count":      a.frozenTieCount,
	}, nil
}

// Components returns {distinct_count, mode_value, mode_count} — the
// cardinality of the per-value count map plus the modal value
// (smallest-value tie-break, matching AGG_MODE) and the modal count.
// mode_count duplicates the scalar return of AGG_FREQUENCY — it is
// the per-operator definition of the modal cardinality. Reads
// frozen* mirrors so the streaming path's Finalize-reset does not
// erase the values before Components() runs. Empty input returns
// (nil, nil) so the orchestrator's universal floor (n=0) is the
// source of truth.
func (a *frequencyAggregator) Components() (map[string]any, error) {
	if !a.frozenFinalized || a.frozenModeCount == 0 {
		return nil, nil
	}
	return map[string]any{
		"distinct_count": a.frozenDistinct,
		"mode_value":     a.frozenModeValue,
		"mode_count":     a.frozenModeCount,
	}, nil
}

// --- Order-stat Components implementations (E1-S9) ---------------
//
// AGG_MEDIAN and AGG_PERCENTILE are the canonical non-mergeable
// aggregators — they require a sorted view of the full value set
// (ComponentsMergeability=None). Streaming chunks deliberately omit
// per-chunk Components emission and emit only at terminal buffered
// flush (the streaming-path per-chunk omission lands in E4-S4; this
// story implements the buffered emission + documents the contract).
// Both Components implementations read frozen mirrors stamped by
// aggregateValues, so the buffered path's processRecords exit
// produces a stable snapshot.

// Components returns {position_low, position_high, median} — the
// sorted-position indices that bracket the median plus the resolved
// value. Empty input returns (nil, nil) so the orchestrator's
// universal floor (n=0) is the source of truth for "no rows seen".
func (a *medianAggregator) Components() (map[string]any, error) {
	if !a.frozenFinalized || a.frozenN == 0 {
		return nil, nil
	}
	return map[string]any{
		"position_low":  a.frozenPositionLow,
		"position_high": a.frozenPositionHigh,
		"median":        a.frozenMedian,
	}, nil
}

// Components returns {p, position, lower, upper, method, value} —
// the configured percentile, the sorted-position floor index, the
// bracketing values, the interpolation method, and the resolved
// scalar. method is always "linear" today; future interpolation
// kinds (lower/higher/nearest) plug in by surfacing the configured
// constant here. Empty input returns (nil, nil) per the universal-
// floor convention.
func (a *percentileAggregator) Components() (map[string]any, error) {
	if !a.frozenFinalized || a.frozenN == 0 {
		return nil, nil
	}
	return map[string]any{
		"p":        a.frozenP,
		"position": a.frozenPosition,
		"lower":    a.frozenLower,
		"upper":    a.frozenUpper,
		"method":   a.frozenMethod,
		"value":    a.frozenValue,
	}, nil
}

// Compile-time interface locks for the seven scalar aggregators.
// Keeps the wiring grep-discoverable and catches interface drift at
// build time when interfaces.go.MetaAggregator changes shape.
var (
	_ MetaAggregator = (*countAggregator)(nil)
	_ MetaAggregator = (*sumAggregator)(nil)
	_ MetaAggregator = (*averageAggregator)(nil)
	_ MetaAggregator = (*minAggregator)(nil)
	_ MetaAggregator = (*maxAggregator)(nil)
	_ MetaAggregator = (*rangeAggregator)(nil)
	_ MetaAggregator = (*nullCountAggregator)(nil)

	// Welford-family compile-time locks (E1-S6).
	_ MetaAggregator = (*varianceAggregator)(nil)
	_ MetaAggregator = (*stdDevAggregator)(nil)
	_ MetaAggregator = (*skewnessAggregator)(nil)
	_ MetaAggregator = (*kurtosisAggregator)(nil)
	_ MetaAggregator = (*zscoreAggregator)(nil)

	// Map-state compile-time locks (E1-S7).
	_ MetaAggregator = (*distinctCountAggregator)(nil)
	_ MetaAggregator = (*modeAggregator)(nil)
	_ MetaAggregator = (*frequencyAggregator)(nil)

	// Order-stat compile-time locks (E1-S9).
	_ MetaAggregator = (*medianAggregator)(nil)
	_ MetaAggregator = (*percentileAggregator)(nil)
)
