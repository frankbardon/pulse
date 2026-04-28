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

// --- Count ---

// countAggregator counts non-null values for a field. The streaming path
// uses the n field; the buffered path ignores it (collectValues + len).
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

// sumAggregator sums non-null values. The sum field is used by the
// streaming path; the buffered path computes from a slice and does not
// rely on it.
type sumAggregator struct {
	sum float64
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
	return sum, nil
}

// --- Average ---

// averageAggregator tracks running sum and count for streaming mean.
type averageAggregator struct {
	sum float64
	n   int64
}

func newAverageAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &averageAggregator{}, nil
}

func (a *averageAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *averageAggregator) aggregateValues(vals []float64) (float64, error) {
	return mean(vals), nil
}

// --- Min ---

type minAggregator struct {
	min  float64
	seen bool
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
		return 0, nil
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m, nil
}

// --- Max ---

type maxAggregator struct {
	max  float64
	seen bool
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
		return 0, nil
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m, nil
}

// --- StdDev ---

// stdDevAggregator tracks Welford's running mean and M2 for streaming
// computation. Buffered path ignores these and uses populationStdDev.
type stdDevAggregator struct {
	n    int64
	mean float64
	m2   float64
}

func newStdDevAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &stdDevAggregator{}, nil
}

func (a *stdDevAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *stdDevAggregator) aggregateValues(vals []float64) (float64, error) {
	return populationStdDev(vals), nil
}

// --- Range ---

type rangeAggregator struct {
	min, max float64
	seen     bool
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
	return maxV - minV, nil
}

// --- Frequency ---

type frequencyAggregator struct {
	counts map[float64]int
}

func newFrequencyAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &frequencyAggregator{}, nil
}

func (a *frequencyAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *frequencyAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) == 0 {
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
	return float64(maxCount), nil
}

// --- ZScore (aggregator) ---

type zscoreAggregator struct{}

func newZScoreAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &zscoreAggregator{}, nil
}

func (a *zscoreAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *zscoreAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) == 0 {
		return 0, nil
	}
	m := mean(vals)
	sd := populationStdDev(vals)
	if sd == 0 {
		return 0, nil
	}
	// Mean z-score is always 0 by definition, but compute it properly
	zSum := 0.0
	for _, v := range vals {
		zSum += (v - m) / sd
	}
	return zSum / float64(len(vals)), nil
}

// --- Median ---

type medianAggregator struct{}

func newMedianAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &medianAggregator{}, nil
}

func (a *medianAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *medianAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) == 0 {
		return 0, nil
	}
	// median sorts in place — copy first to avoid mutating a shared cached slice.
	work := make([]float64, len(vals))
	copy(work, vals)
	sort.Float64s(work)
	n := len(work)
	if n%2 == 1 {
		return work[n/2], nil
	}
	return (work[n/2-1] + work[n/2]) / 2, nil
}

// --- Variance ---

// varianceAggregator uses Welford's online recurrence in the streaming
// path. Buffered path uses populationVariance and does not read these.
type varianceAggregator struct {
	n    int64
	mean float64
	m2   float64
}

func newVarianceAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &varianceAggregator{}, nil
}

func (a *varianceAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *varianceAggregator) aggregateValues(vals []float64) (float64, error) {
	return populationVariance(vals), nil
}

// --- Mode ---

type modeAggregator struct {
	counts map[float64]int
}

func newModeAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &modeAggregator{}, nil
}

func (a *modeAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *modeAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) == 0 {
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
	for v, c := range counts {
		if c == maxCount {
			if first || v < result {
				result = v
				first = false
			}
		}
	}
	return result, nil
}

// --- Skewness ---

// skewnessAggregator uses the Welford-Pébaÿ recurrence through M3 for
// online streaming. Buffered path is independent.
type skewnessAggregator struct {
	n    int64
	mean float64
	m2   float64
	m3   float64
}

func newSkewnessAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &skewnessAggregator{}, nil
}

func (a *skewnessAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *skewnessAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) <= 1 {
		return 0, nil
	}
	m := mean(vals)
	sd := populationStdDev(vals)
	if sd == 0 {
		return 0, nil
	}
	n := float64(len(vals))
	sum := 0.0
	for _, v := range vals {
		sum += math.Pow((v-m)/sd, 3)
	}
	return sum / n, nil
}

// --- Kurtosis ---

// kurtosisAggregator uses the Welford-Pébaÿ recurrence through M4.
type kurtosisAggregator struct {
	n    int64
	mean float64
	m2   float64
	m3   float64
	m4   float64
}

func newKurtosisAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &kurtosisAggregator{}, nil
}

func (a *kurtosisAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *kurtosisAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) <= 1 {
		return 0, nil
	}
	m := mean(vals)
	sd := populationStdDev(vals)
	if sd == 0 {
		return 0, nil
	}
	n := float64(len(vals))
	sum := 0.0
	for _, v := range vals {
		sum += math.Pow((v-m)/sd, 4)
	}
	return sum/n - 3, nil
}

// --- Distinct Count ---

type distinctCountAggregator struct {
	set map[float64]struct{}
}

func newDistinctCountAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &distinctCountAggregator{}, nil
}

func (a *distinctCountAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return a.aggregateValues(vals)
}

func (a *distinctCountAggregator) aggregateValues(vals []float64) (float64, error) {
	if len(vals) == 0 {
		return 0, nil
	}
	set := make(map[float64]struct{})
	for _, v := range vals {
		set[v] = struct{}{}
	}
	return float64(len(set)), nil
}

// --- Percentile ---

type percentileParams struct {
	Percentile float64 `json:"percentile"`
}

type percentileAggregator struct {
	percentile float64
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
	if len(vals) == 0 {
		return 0, nil
	}
	// percentile sorts in place — copy first to avoid mutating a shared cached slice.
	work := make([]float64, len(vals))
	copy(work, vals)
	sort.Float64s(work)
	n := len(work)
	if n == 1 {
		return work[0], nil
	}
	rank := a.percentile / 100.0 * float64(n-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return work[lower], nil
	}
	return work[lower] + (rank-float64(lower))*(work[upper]-work[lower]), nil
}
