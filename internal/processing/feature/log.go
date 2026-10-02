package feature

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_LOG, newLog)
}

// log applies log(x + 1) to a numeric field, propagating nulls. The +1 shift
// is the standard "log1p" convention: it lets x=0 produce 0 instead of -Inf
// while preserving the curve's shape. Negative inputs (x <= -1) are not
// representable; those rows go null with no error so feature engineering
// continues for the rest of the cohort.
type log struct{ label string }

func newLog(feat *types.Feature, _ *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_LOG requires a field")
	}
	label := feat.Label
	if label == "" {
		label = fmt.Sprintf("LOG_%s", feat.Field)
	}
	return &log{label: label}, nil
}

func (c *log) Compute(records []Record, field string) (map[string]Output, error) {
	values := make([]float64, len(records))
	nulls := make([]bool, len(records))
	for i, r := range records {
		values[i], nulls[i] = logValue(r, field)
	}
	return map[string]Output{c.label: {Values: values, Nulls: nulls}}, nil
}

// PrePass is a no-op: LOG is stateless and per-row.
func (c *log) PrePass(_ Record, _ string) error { return nil }

// Finalize is a no-op: nothing to materialize.
func (c *log) Finalize() error { return nil }

// EmitRow returns log1p(field) for the current record, mirroring Compute's
// per-row semantics.
func (c *log) EmitRow(r Record, field string) (map[string]Output, error) {
	v, isNull := logValue(r, field)
	return map[string]Output{c.label: {Values: []float64{v}, Nulls: []bool{isNull}}}, nil
}

// logValue is the shared inner math: log1p with null propagation for
// missing values and v <= -1.
func logValue(r Record, field string) (float64, bool) {
	v, ok := r.NumericValue(field)
	if !ok || v <= -1 {
		return 0, true
	}
	return math.Log1p(v), false
}
