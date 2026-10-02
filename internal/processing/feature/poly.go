package feature

import (
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_POLY, newPoly)
}

// MaxPolyDegree caps the degree of FEAT_POLY expansion. Naive x^d
// overflows quickly when x is large or untransformed; callers needing
// higher degrees should standardize or center their inputs first and,
// for serious work, use an orthogonal basis (not in scope for v1).
const MaxPolyDegree = 10

// polyParams selects the expansion degree. The wire shape is
// {"degree": N} — single integer, ≥ 2.
type polyParams struct {
	Degree int `json:"degree"`
}

// poly emits Degree-1 derived columns per input row, raising the input
// to integer powers 2..Degree. The original column (degree 1) is left
// untouched so downstream OLS can include the linear term by referencing
// the original predictor name alongside the synthesized higher-order
// names. Output columns are addressable as "<prefix>_<power>" where the
// prefix defaults to "<field>_poly" (override via Label).
//
// FEAT_POLY is the operator backing polynomial regression (Indeed #8):
// combined with REG_OLS, it lifts a univariate predictor into a linear
// model over the polynomial basis {x, x², ..., x^d}.
type poly struct {
	prefix string
	degree int
}

func newPoly(feat *types.Feature, _ *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_POLY requires a field")
	}
	var p polyParams
	if len(feat.Params) > 0 {
		if err := json.Unmarshal(feat.Params, &p); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("FEAT_POLY params: %v", err))
		}
	}
	if p.Degree < 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_POLY requires Degree >= 2; use the original column for the linear term")
	}
	if p.Degree > MaxPolyDegree {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("FEAT_POLY Degree capped at %d; consider standardizing inputs first or using an orthogonal basis", MaxPolyDegree))
	}

	prefix := feat.Label
	if prefix == "" {
		prefix = fmt.Sprintf("%s_poly", feat.Field)
	}
	return &poly{prefix: prefix, degree: p.Degree}, nil
}

func (c *poly) Compute(records []Record, field string) (map[string]Output, error) {
	out := make(map[string]Output, c.degree-1)
	for k := 2; k <= c.degree; k++ {
		out[c.columnName(k)] = Output{
			Values: make([]float64, len(records)),
			Nulls:  make([]bool, len(records)),
		}
	}
	for i, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			for k := 2; k <= c.degree; k++ {
				o := out[c.columnName(k)]
				o.Nulls[i] = true
			}
			continue
		}
		power := v
		for k := 2; k <= c.degree; k++ {
			power *= v
			o := out[c.columnName(k)]
			o.Values[i] = power
		}
	}
	return out, nil
}

// PrePass is a no-op: POLY is stateless and per-row.
func (c *poly) PrePass(_ Record, _ string) error { return nil }

// Finalize is a no-op: nothing to materialize.
func (c *poly) Finalize() error { return nil }

// EmitRow returns x^2..x^degree for the current record, mirroring
// Compute's per-row semantics. Null inputs produce a null entry on
// every output column.
func (c *poly) EmitRow(r Record, field string) (map[string]Output, error) {
	out := make(map[string]Output, c.degree-1)
	v, ok := r.NumericValue(field)
	if !ok {
		for k := 2; k <= c.degree; k++ {
			out[c.columnName(k)] = Output{
				Values: []float64{0},
				Nulls:  []bool{true},
			}
		}
		return out, nil
	}
	power := v
	for k := 2; k <= c.degree; k++ {
		power *= v
		out[c.columnName(k)] = Output{Values: []float64{power}}
	}
	return out, nil
}

func (c *poly) columnName(power int) string {
	return fmt.Sprintf("%s_%d", c.prefix, power)
}
