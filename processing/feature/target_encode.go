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
// targetStat tallies sum and count for one category during PrePass.
// Promoted to a package-level type so per-record streaming state has a
// stable place to live alongside Compute's local-only equivalent.
type targetStat struct {
	sum   float64
	count int
}

type targetEncode struct {
	label     string
	target    string
	smoothing float64
	// Streaming state. per is populated during PrePass; Finalize
	// captures globalMean (or marks empty=true when no non-null target
	// rows were observed) so EmitRow is O(1) per row.
	per         map[string]*targetStat
	globalSum   float64
	globalCount int
	globalMean  float64
	empty       bool
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

	per := make(map[string]*targetStat, 16)
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
			st = &targetStat{}
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
		values[i] = encodedValue(per, s, globalMean, c.smoothing)
	}
	return map[string]Output{c.label: {Values: values, Nulls: nulls}}, nil
}

// PrePass folds the record's (category, target) pair into the running
// per-category and global stats. Rows with a null category or null
// target contribute nothing — same as Compute.
func (c *targetEncode) PrePass(r Record, field string) error {
	if c.per == nil {
		c.per = make(map[string]*targetStat, 16)
	}
	s, sOk := r.StringValue(field)
	t, tOk := r.NumericValue(c.target)
	if !sOk || !tOk {
		return nil
	}
	st := c.per[s]
	if st == nil {
		st = &targetStat{}
		c.per[s] = st
	}
	st.sum += t
	st.count++
	c.globalSum += t
	c.globalCount++
	return nil
}

// Finalize freezes globalMean. When no non-null target rows were
// observed every emitted row is null.
func (c *targetEncode) Finalize() error {
	if c.globalCount == 0 {
		c.empty = true
		return nil
	}
	c.globalMean = c.globalSum / float64(c.globalCount)
	return nil
}

// EmitRow returns the encoded mean for the record's category, applying
// smoothing when configured. Mirrors Compute's per-row branch.
func (c *targetEncode) EmitRow(r Record, field string) (map[string]Output, error) {
	if c.empty {
		return map[string]Output{c.label: {Values: []float64{0}, Nulls: []bool{true}}}, nil
	}
	s, ok := r.StringValue(field)
	if !ok {
		return map[string]Output{c.label: {Values: []float64{0}, Nulls: []bool{true}}}, nil
	}
	return map[string]Output{c.label: {Values: []float64{encodedValue(c.per, s, c.globalMean, c.smoothing)}}}, nil
}

// encodedValue is the shared smoothing math: returns globalMean for an
// unseen category, the per-cat mean otherwise, with optional additive
// smoothing toward globalMean. Used by both Compute and EmitRow.
func encodedValue(per map[string]*targetStat, s string, globalMean, smoothing float64) float64 {
	st, found := per[s]
	if !found || st.count == 0 {
		return globalMean
	}
	mean := st.sum / float64(st.count)
	if smoothing > 0 {
		return (float64(st.count)*mean + smoothing*globalMean) / (float64(st.count) + smoothing)
	}
	return mean
}
