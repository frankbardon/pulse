package processing

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

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

type countAggregator struct{}

func newCountAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &countAggregator{}, nil
}

func (a *countAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return float64(len(vals)), nil
}

// --- Sum ---

type sumAggregator struct{}

func newSumAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &sumAggregator{}, nil
}

func (a *sumAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum, nil
}

// --- Average ---

type averageAggregator struct{}

func newAverageAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &averageAggregator{}, nil
}

func (a *averageAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return mean(vals), nil
}

// --- Min ---

type minAggregator struct{}

func newMinAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &minAggregator{}, nil
}

func (a *minAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
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

type maxAggregator struct{}

func newMaxAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &maxAggregator{}, nil
}

func (a *maxAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
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

type stdDevAggregator struct{}

func newStdDevAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &stdDevAggregator{}, nil
}

func (a *stdDevAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return populationStdDev(vals), nil
}

// --- Range ---

type rangeAggregator struct{}

func newRangeAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &rangeAggregator{}, nil
}

func (a *rangeAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
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

type frequencyAggregator struct{}

func newFrequencyAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &frequencyAggregator{}, nil
}

func (a *frequencyAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
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
	if len(vals) == 0 {
		return 0, nil
	}
	sort.Float64s(vals)
	n := len(vals)
	if n%2 == 1 {
		return vals[n/2], nil
	}
	return (vals[n/2-1] + vals[n/2]) / 2, nil
}

// --- Variance ---

type varianceAggregator struct{}

func newVarianceAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &varianceAggregator{}, nil
}

func (a *varianceAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
	return populationVariance(vals), nil
}

// --- Mode ---

type modeAggregator struct{}

func newModeAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &modeAggregator{}, nil
}

func (a *modeAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
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

type skewnessAggregator struct{}

func newSkewnessAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &skewnessAggregator{}, nil
}

func (a *skewnessAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
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

type kurtosisAggregator struct{}

func newKurtosisAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &kurtosisAggregator{}, nil
}

func (a *kurtosisAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
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

type distinctCountAggregator struct{}

func newDistinctCountAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &distinctCountAggregator{}, nil
}

func (a *distinctCountAggregator) Aggregate(records []*Record, field string) (float64, error) {
	vals := collectValues(records, field)
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
	if len(vals) == 0 {
		return 0, nil
	}
	sort.Float64s(vals)
	n := len(vals)
	if n == 1 {
		return vals[0], nil
	}
	rank := a.percentile / 100.0 * float64(n-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return vals[lower], nil
	}
	return vals[lower] + (rank-float64(lower))*(vals[upper]-vals[lower]), nil
}
