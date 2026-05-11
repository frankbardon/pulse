package processing

import (
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// anovaWelchRow implements TEST_ANOVA_WELCH as a streaming row test:
// heteroscedasticity-robust one-way ANOVA. Per-group online Welford
// state matches TEST_ANOVA_F; the finalization formula differs.
//
// Statistic (Welch 1951):
//
//	w_i = n_i / s²_i
//	W   = Σ w_i
//	μ̃   = Σ w_i μ_i / W
//	num = Σ w_i (μ_i − μ̃)² / (k − 1)
//	den = 1 + 2(k−2)/(k²−1) · Σ ((1 − w_i/W)² / (n_i − 1))
//	F*  = num / den
//	df1 = k − 1
//	df2 = (k² − 1) / (3 · Σ ((1 − w_i/W)² / (n_i − 1)))
//	p   = fSurvival(F*, df1, df2)
//
// Streamable: yes. The per-group buckets are identical to TEST_ANOVA_F;
// canStream sees Streamable()==true and routes through the single-pass
// path.
type anovaWelchRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	alpha   float64

	groups map[string]*welfordBucket
	order  []string
}

func newAnovaWelchRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_ANOVA_WELCH requires field")
	}
	if spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_ANOVA_WELCH requires split_by")
	}
	alpha := spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	if alpha <= 0 || alpha >= 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INVALID_ALPHA,
			fmt.Sprintf("alpha %g not in (0, 1)", spec.Alpha),
			map[string]any{"alpha": spec.Alpha})
	}
	if schema != nil {
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical() || f.Type.IsGeo()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_ANOVA_WELCH field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("TEST_ANOVA_WELCH split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
				map[string]any{"split_by": spec.SplitBy, "field_type": f.Type.String()})
		}
	}
	return &anovaWelchRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		groups:  make(map[string]*welfordBucket),
	}, nil
}

func (a *anovaWelchRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(a.field)
	if !ok {
		return nil
	}
	key, ok := record.StringValue(a.splitBy)
	if !ok {
		return nil
	}
	b, exists := a.groups[key]
	if !exists {
		b = &welfordBucket{}
		a.groups[key] = b
		a.order = append(a.order, key)
	}
	b.add(v)
	return nil
}

func (a *anovaWelchRow) Finalize() (*types.TestResult, error) {
	defer a.reset()
	keys := append([]string(nil), a.order...)
	sort.Strings(keys)
	k := len(keys)
	if k < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_ANOVA_WELCH requires ≥ 2 groups, got %d", k),
			map[string]any{"groups": keys, "min_required": 2})
	}
	ns := make([]int64, k)
	means := make([]float64, k)
	vars_ := make([]float64, k)
	for i, key := range keys {
		b := a.groups[key]
		if b.n < 2 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_ANOVA_WELCH requires n ≥ 2 per group; %q has %d", key, b.n),
				map[string]any{"group": key, "n": b.n, "min_required": 2})
		}
		ns[i] = b.n
		means[i] = b.mean
		s2 := b.sampleVariance()
		if s2 == 0 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
				fmt.Sprintf("TEST_ANOVA_WELCH: group %q has zero variance", key),
				map[string]any{"group": key})
		}
		vars_[i] = s2
	}
	weights := make([]float64, k)
	var W, weightedMean float64
	for i := range k {
		weights[i] = float64(ns[i]) / vars_[i]
		W += weights[i]
		weightedMean += weights[i] * means[i]
	}
	weightedMean /= W
	var num, tailSum float64
	for i := range k {
		diff := means[i] - weightedMean
		num += weights[i] * diff * diff
		share := 1 - weights[i]/W
		tailSum += share * share / float64(ns[i]-1)
	}
	num /= float64(k - 1)
	kf := float64(k)
	den := 1 + 2*(kf-2)/(kf*kf-1)*tailSum
	F := num / den
	df1 := kf - 1
	df2 := (kf*kf - 1) / (3 * tailSum)
	p := fSurvival(F, df1, df2)
	return &types.TestResult{
		Label:      testLabel(a.spec),
		Type:       types.TEST_ANOVA_WELCH,
		Variant:    "welch_one_way",
		Statistic:  F,
		DF:         df1,
		PValue:     p,
		Alpha:      a.alpha,
		RejectNull: p < a.alpha,
		Details: map[string]any{
			"groups":          keys,
			"n":               ns,
			"group_means":     means,
			"group_variances": vars_,
			"weights":         weights,
			"weighted_mean":   weightedMean,
			"df_between":      df1,
			"df_within":       df2,
		},
	}, nil
}

func (a *anovaWelchRow) reset() {
	a.groups = make(map[string]*welfordBucket)
	a.order = nil
}
