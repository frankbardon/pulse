package processing

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
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

// populationStdDev computes population standard deviation.
func populationStdDev(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := mean(vals)
	sumSq := 0.0
	for _, v := range vals {
		d := v - m
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(vals)))
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
