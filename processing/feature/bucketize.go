package feature

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_BUCKETIZE, newBucketize)
}

// bucketizeParams selects between explicit-boundary and quantile modes.
//
//	{"boundaries": [10, 20, 30]}  // explicit edges
//	{"quantiles": 4}              // 4 equal-frequency buckets
//
// Boundaries are exclusive on the lower edge. A value v lands in bucket i
// where boundaries[i-1] < v <= boundaries[i] (with -inf at boundaries[-1]
// and +inf at boundaries[len]). The output column carries the bucket index
// (0..N) as a float64.
type bucketizeParams struct {
	Boundaries []float64 `json:"boundaries"`
	Quantiles  int       `json:"quantiles"`
}

type bucketize struct {
	label      string
	boundaries []float64
	quantiles  int
	// quantileVals accumulates non-null inputs during PrePass when the
	// operator is in quantile mode. Finalize sorts and reduces it to N-1
	// boundary cutpoints stored back on boundaries; EmitRow then uses
	// boundaries the same way it does in explicit mode.
	quantileVals []float64
}

func newBucketize(feat *types.Feature, _ *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_BUCKETIZE requires a field")
	}
	var p bucketizeParams
	if len(feat.Params) > 0 {
		if err := json.Unmarshal(feat.Params, &p); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("FEAT_BUCKETIZE params: %v", err))
		}
	}
	if len(p.Boundaries) == 0 && p.Quantiles <= 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_BUCKETIZE requires either 'boundaries' or 'quantiles' > 0")
	}
	if len(p.Boundaries) > 0 && p.Quantiles > 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_BUCKETIZE: 'boundaries' and 'quantiles' are mutually exclusive")
	}

	label := feat.Label
	if label == "" {
		label = fmt.Sprintf("BUCKET_%s", feat.Field)
	}

	bounds := append([]float64(nil), p.Boundaries...)
	sort.Float64s(bounds)
	return &bucketize{label: label, boundaries: bounds, quantiles: p.Quantiles}, nil
}

func (c *bucketize) Compute(records []Record, field string) (map[string]Output, error) {
	values := make([]float64, len(records))
	nulls := make([]bool, len(records))

	bounds := c.boundaries
	if c.quantiles > 0 {
		bounds = quantileBoundaries(records, field, c.quantiles)
	}

	for i, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			nulls[i] = true
			continue
		}
		values[i] = float64(bucketIndex(v, bounds))
	}
	return map[string]Output{c.label: {Values: values, Nulls: nulls}}, nil
}

// PrePass collects non-null inputs in quantile mode so Finalize can derive
// boundaries. Explicit mode is stateless: the boundaries are already
// fixed at construction time.
func (c *bucketize) PrePass(r Record, field string) error {
	if c.quantiles <= 0 {
		return nil
	}
	if v, ok := r.NumericValue(field); ok {
		c.quantileVals = append(c.quantileVals, v)
	}
	return nil
}

// Finalize converts accumulated quantile inputs into N-1 boundary
// cutpoints. In explicit mode it is a no-op.
func (c *bucketize) Finalize() error {
	if c.quantiles <= 0 {
		return nil
	}
	c.boundaries = quantileBoundariesFromValues(c.quantileVals, c.quantiles)
	c.quantileVals = nil
	return nil
}

// EmitRow returns the bucket index for the current record using the
// boundaries fixed at construction (explicit) or computed in Finalize
// (quantile).
func (c *bucketize) EmitRow(r Record, field string) (map[string]Output, error) {
	v, ok := r.NumericValue(field)
	if !ok {
		return map[string]Output{c.label: {Values: []float64{0}, Nulls: []bool{true}}}, nil
	}
	return map[string]Output{c.label: {Values: []float64{float64(bucketIndex(v, c.boundaries))}}}, nil
}

// bucketIndex returns the index of the bucket v belongs to, given sorted
// boundaries. A value v lands at index i when bounds[i-1] < v <= bounds[i]
// (treating bounds[-1] as -inf and bounds[len] as +inf).
func bucketIndex(v float64, bounds []float64) int {
	idx := sort.SearchFloat64s(bounds, v)
	// SearchFloat64s returns the first index where v <= bounds[idx]. That
	// matches our convention: v at exactly bounds[i] lands in bucket i.
	return idx
}

// quantileBoundaries returns N-1 quantile cutpoints over the non-null values
// of field. Returns an empty slice when fewer than 2 non-null values exist;
// every record then maps to bucket 0.
func quantileBoundaries(records []Record, field string, n int) []float64 {
	if n < 2 {
		return nil
	}
	vals := make([]float64, 0, len(records))
	for _, r := range records {
		if v, ok := r.NumericValue(field); ok {
			vals = append(vals, v)
		}
	}
	return quantileBoundariesFromValues(vals, n)
}

// quantileBoundariesFromValues returns N-1 quantile cutpoints from a
// pre-collected slice of non-null inputs. Shared between Compute (which
// scans records) and Finalize (which consumes the streaming PrePass
// accumulator). Returns nil when there are fewer than 2 inputs.
func quantileBoundariesFromValues(vals []float64, n int) []float64 {
	if n < 2 || len(vals) < 2 {
		return nil
	}
	sort.Float64s(vals)
	bounds := make([]float64, n-1)
	for i := 1; i < n; i++ {
		pos := float64(i) * float64(len(vals)) / float64(n)
		idx := int(pos)
		if idx >= len(vals) {
			idx = len(vals) - 1
		}
		bounds[i-1] = vals[idx]
	}
	return bounds
}
