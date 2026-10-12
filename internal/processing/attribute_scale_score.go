package processing

import (
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// ATTR_SCALE_SCORE — a rating battery's per-row score (mean or prorated
// sum of the valid items) across the members of Vector / Fields.
//
// Row-local: a row's score reads only its own members, so the operator
// implements RowLocalAttribute (single pass, streamable, fusable) and
// never fits. The members and params arrive canonical from
// Processor.prepare (resolveAttributeInputs: Fields = the literal
// members, Vector cleared, params rewritten by
// vectors.ScaleScoreConfig.CanonicalParams — so a kind "scale" vector's
// bounds and a reverse list by member label survive as explicit
// params); the factory judges them through the same
// vectors.ScaleScoreParams predict calls.
//
// Per row, in member order:
//
//   - a null or NaN member is missing;
//   - with a declared range, a present value outside [lo, hi] is
//     MISSING too — never clamped, never reversed — and counted
//     (n_out_of_range values, n_rows rows), raising one
//     PULSE_ATTR_SCALE_OUT_OF_RANGE warning per slot after the run
//     (attributeWarner). MAT_RELIABILITY refuses the same value
//     (PROCESSING_CONFIG): both read the one vectors.ScaleKey;
//   - each reverse-keyed valid item is flipped lo + hi − x;
//   - k valid items below params.min_valid score null (NaN → null,
//     NullEmitter), never 0; otherwise the score is the valid-item mean,
//     or for "sum" that mean × p (prorated: a skipped item never reads
//     as 0).
//
// The weight is irrelevant (weighting class none): a row's score does
// not depend on any other row.

// scaleScoreAttribute is the ATTR_SCALE_SCORE computer.
type scaleScoreAttribute struct {
	label   string
	members []string
	cfg     vectors.ScaleScoreConfig
	x       []float64

	// nOutOfRange counts member values read as missing for lying outside
	// the declared range; nRows the rows holding at least one.
	nOutOfRange int64
	nRows       int64
}

var (
	_ AttributeComputer = (*scaleScoreAttribute)(nil)
	_ RowLocalAttribute = (*scaleScoreAttribute)(nil)
	_ NullEmitter       = (*scaleScoreAttribute)(nil)
	_ attributeWarner   = (*scaleScoreAttribute)(nil)
)

// newScaleScoreAttribute builds the computer over attr's resolved
// literal members. A registry probe (no members) builds a computer over
// none, whose every score is null.
func newScaleScoreAttribute(attr *types.Attribute, _ *encoding.Schema) (AttributeComputer, error) {
	if attr == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "ATTR_SCALE_SCORE requires an attribute spec")
	}
	if attr.Vector != "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s vector %q was not resolved before construction", attr.Type, attr.Vector),
			map[string]any{"attribute": string(attr.Type), "vector": attr.Vector})
	}
	label := attr.Label
	if label == "" {
		label = defaultAttributeLabel(attr)
	}
	a := &scaleScoreAttribute{label: label, members: append([]string(nil), attr.Fields...)}
	if len(a.members) == 0 {
		return a, nil
	}
	members := vectors.Resolved{Members: a.members, Labels: a.members}
	cfg, err := vectors.ScaleScoreParams(label, attr.Params, members)
	if err != nil {
		return nil, err
	}
	a.cfg = cfg
	a.x = make([]float64, len(a.members))
	return a, nil
}

// EmitsNull satisfies NullEmitter: a NaN score (below min_valid) is a
// null.
func (a *scaleScoreAttribute) EmitsNull() bool { return true }

// Row scores one record (the field argument is unused: the members are
// the inputs).
func (a *scaleScoreAttribute) Row(rec *Record, _ string) (float64, error) {
	p := len(a.members)
	if p == 0 {
		return math.NaN(), nil
	}
	rowOut := false
	for i, f := range a.members {
		v, ok := rec.NumericValue(f)
		if !ok {
			v = math.NaN()
		}
		if a.cfg.HasScale && a.cfg.Key.OutOfRange(v) {
			a.nOutOfRange++
			rowOut = true
			v = math.NaN()
		}
		a.x[i] = v
	}
	if rowOut {
		a.nRows++
	}
	if a.cfg.HasScale {
		a.cfg.Key.ApplyReverse(a.x)
	}
	k, sum := 0, 0.0
	for _, v := range a.x {
		if math.IsNaN(v) {
			continue
		}
		k++
		sum += v
	}
	if k == 0 || k < a.cfg.MinValid {
		return math.NaN(), nil
	}
	mean := sum / float64(k)
	if a.cfg.Stat == vectors.ScaleStatSum {
		return mean * float64(p), nil
	}
	return mean, nil
}

// Compute is Row over every record (the buffered arm).
func (a *scaleScoreAttribute) Compute(records []*Record, field string) ([]float64, error) {
	out := make([]float64, len(records))
	for i, r := range records {
		v, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// attributeWarnings satisfies attributeWarner: one
// PULSE_ATTR_SCALE_OUT_OF_RANGE warning when any member value lay
// outside the declared range.
func (a *scaleScoreAttribute) attributeWarnings() []*types.ResponseWarning {
	if a.nOutOfRange == 0 {
		return nil
	}
	return []*types.ResponseWarning{{
		Code: string(errors.PULSE_ATTR_SCALE_OUT_OF_RANGE),
		Message: a.label + ": " + strconv.FormatInt(a.nOutOfRange, 10) + " member value(s) on " + strconv.FormatInt(a.nRows, 10) +
			" row(s) lie outside the declared scale range [" + strconv.FormatFloat(a.cfg.Key.Lo, 'g', -1, 64) + ", " +
			strconv.FormatFloat(a.cfg.Key.Hi, 'g', -1, 64) + "] and were scored as missing (never clamped)",
		Details: map[string]any{
			"attribute":      a.label,
			"n_out_of_range": a.nOutOfRange,
			"n_rows":         a.nRows,
			"scale_min":      a.cfg.Key.Lo,
			"scale_max":      a.cfg.Key.Hi,
		},
	}}
}
