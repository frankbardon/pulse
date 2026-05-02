package feature

import (
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_TARGET_ENCODE, newTargetEncode)
}

// targetEncodeParams configures the operator. Target is required.
//
//	{"target": "label", "smoothing": 0.0}
//
// Smoothing applies the standard additive prior toward the global mean:
// encoded = (count_cat * mean_cat + smoothing * global_mean) / (count_cat + smoothing).
// Smoothing 0 reproduces the unsmoothed mean. Higher values pull rare
// categories toward the global mean to fight overfitting.
type targetEncodeParams struct {
	Target    string  `json:"target"`
	Smoothing float64 `json:"smoothing"`
}

// targetEncode replaces a categorical value with the mean of a numeric
// target field over rows sharing that category. Two passes: pass one
// computes per-category sums and counts; pass two writes the per-row mean.
//
// LEAKAGE WARNING: this operator computes statistics on every row in the
// stream. If applied before FEAT_TRAIN_TEST_SPLIT, target information from
// validation/test rows leaks into the training feature. The skill flags
// this loudly; the warning gate (PULSE_FEAT_TARGET_LEAKAGE_RISK) lands in a
// follow-up commit and runs at orchestration time.
type targetEncode struct {
	label     string
	target    string
	smoothing float64
}

func newTargetEncode(feat *types.Feature, schema *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_TARGET_ENCODE requires a field")
	}
	if len(feat.Params) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_TARGET_ENCODE requires params with a 'target' field")
	}
	var p targetEncodeParams
	if err := json.Unmarshal(feat.Params, &p); err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			"parsing FEAT_TARGET_ENCODE params")
	}
	if p.Target == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_TARGET_ENCODE: 'target' is required in params")
	}
	if p.Smoothing < 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_TARGET_ENCODE: 'smoothing' must be >= 0")
	}
	if schema != nil {
		if f := schema.Field(feat.Field); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("FEAT_TARGET_ENCODE: field %q must be categorical, got %s",
					feat.Field, f.Type))
		}
		if f := schema.Field(p.Target); f != nil && f.Type.IsCategorical() {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("FEAT_TARGET_ENCODE: target %q must be numeric, got categorical %s",
					p.Target, f.Type))
		}
	}

	label := feat.Label
	if label == "" {
		label = fmt.Sprintf("TARGET_%s", feat.Field)
	}
	return &targetEncode{label: label, target: p.Target, smoothing: p.Smoothing}, nil
}

func (c *targetEncode) Compute(records []Record, field string) (map[string]Output, error) {
	if len(records) == 0 {
		return map[string]Output{c.label: {Values: []float64{}}}, nil
	}

	type stat struct {
		sum   float64
		count int
	}
	per := make(map[string]*stat, 16)
	var globalSum float64
	var globalCount int

	for _, r := range records {
		s, sOk := r.StringValue(field)
		t, tOk := r.NumericValue(c.target)
		if !sOk || !tOk {
			continue
		}
		st := per[s]
		if st == nil {
			st = &stat{}
			per[s] = st
		}
		st.sum += t
		st.count++
		globalSum += t
		globalCount++
	}

	values := make([]float64, len(records))
	nulls := make([]bool, len(records))

	if globalCount == 0 {
		for i := range nulls {
			nulls[i] = true
		}
		return map[string]Output{c.label: {Values: values, Nulls: nulls}}, nil
	}
	globalMean := globalSum / float64(globalCount)

	for i, r := range records {
		s, ok := r.StringValue(field)
		if !ok {
			nulls[i] = true
			continue
		}
		st, found := per[s]
		if !found || st.count == 0 {
			values[i] = globalMean
			continue
		}
		mean := st.sum / float64(st.count)
		if c.smoothing > 0 {
			values[i] = (float64(st.count)*mean + c.smoothing*globalMean) /
				(float64(st.count) + c.smoothing)
		} else {
			values[i] = mean
		}
	}
	return map[string]Output{c.label: {Values: values, Nulls: nulls}}, nil
}
