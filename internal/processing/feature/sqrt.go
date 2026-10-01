package feature

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_SQRT, newSqrt)
}

// sqrt applies sqrt(x) to a numeric field, propagating nulls and tagging
// negative inputs as null (sqrt of negative is undefined for real-valued
// features).
type sqrt struct{ label string }

func newSqrt(feat *types.Feature, _ *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_SQRT requires a field")
	}
	label := feat.Label
	if label == "" {
		label = fmt.Sprintf("SQRT_%s", feat.Field)
	}
	return &sqrt{label: label}, nil
}

func (c *sqrt) Compute(records []Record, field string) (map[string]Output, error) {
	values := make([]float64, len(records))
	nulls := make([]bool, len(records))
	for i, r := range records {
		values[i], nulls[i] = sqrtValue(r, field)
	}
	return map[string]Output{c.label: {Values: values, Nulls: nulls}}, nil
}

func (c *sqrt) PrePass(_ Record, _ string) error { return nil }

func (c *sqrt) Finalize() error { return nil }

func (c *sqrt) EmitRow(r Record, field string) (map[string]Output, error) {
	v, isNull := sqrtValue(r, field)
	return map[string]Output{c.label: {Values: []float64{v}, Nulls: []bool{isNull}}}, nil
}

func sqrtValue(r Record, field string) (float64, bool) {
	v, ok := r.NumericValue(field)
	if !ok || v < 0 {
		return 0, true
	}
	return math.Sqrt(v), false
}
