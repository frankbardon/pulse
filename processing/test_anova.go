package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// anovaRow implements TEST_ANOVA_F as a streaming row test: one-way
// analysis of variance via online per-group Welford buckets.
//
// State is k Welford accumulators keyed by SplitBy. Finalize computes:
//
//	grand_mean = Σ n_i μ_i / N
//	SSB        = Σ n_i (μ_i - grand_mean)²
//	SSW        = Σ M2_i
//	df_b = k-1, df_w = N-k
//	F   = (SSB/df_b) / (SSW/df_w)
//	p   = 1 - F_CDF(F; df_b, df_w)
//	η²  = SSB / (SSB + SSW)
//
// Requires N ≥ k+1 and at least two groups; surfaces
// PULSE_TEST_INSUFFICIENT_N or PULSE_TEST_SPLIT_GROUPS_LT_2 on
// violations.
type anovaRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field   string
	splitBy string
	alpha   float64

	groups map[string]*welfordBucket
	order  []string
}

func newAnovaRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_ANOVA_F requires field")
	}
	if spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_ANOVA_F requires split_by")
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
				fmt.Sprintf("TEST_ANOVA_F field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("TEST_ANOVA_F split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
				map[string]any{"split_by": spec.SplitBy, "field_type": f.Type.String()})
		}
	}
	return &anovaRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		groups:  make(map[string]*welfordBucket),
	}, nil
}

func (a *anovaRow) UpdateRow(record *Record) error {
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

func (a *anovaRow) Finalize() (*types.TestResult, error) {
	defer a.reset()
	k := len(a.order)
	if k < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_ANOVA_F requires ≥ 2 groups, got %d", k),
			map[string]any{"groups": a.order, "min_required": 2})
	}
	stats := summariseANOVA(a.order, a.groups)
	if stats.N < int64(k+1) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_ANOVA_F requires N ≥ k+1 (k=%d, N=%d)", k, stats.N),
			map[string]any{"k": k, "n": stats.N})
	}
	if stats.SSW == 0 && stats.SSB == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_ANOVA_F: all groups have zero variance and equal means",
			map[string]any{"groups": a.order})
	}
	dfBetween := float64(k - 1)
	dfWithin := float64(int64(stats.N) - int64(k))
	if dfWithin <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			"TEST_ANOVA_F: zero within-group degrees of freedom",
			map[string]any{"k": k, "n": stats.N})
	}
	msBetween := stats.SSB / dfBetween
	msWithin := stats.SSW / dfWithin
	var F, p float64
	if msWithin == 0 {
		F = 0
		p = 0
	} else {
		F = msBetween / msWithin
		p = fSurvival(F, dfBetween, dfWithin)
	}
	eta2 := 0.0
	if stats.SSB+stats.SSW > 0 {
		eta2 = stats.SSB / (stats.SSB + stats.SSW)
	}
	return &types.TestResult{
		Label:      testLabel(a.spec),
		Type:       types.TEST_ANOVA_F,
		Variant:    "one_way",
		Statistic:  F,
		DF:         dfBetween,
		PValue:     p,
		Alpha:      a.alpha,
		RejectNull: p < a.alpha,
		Details: map[string]any{
			"groups":      a.order,
			"n":           stats.Ns,
			"group_means": stats.Means,
			"ss_between":  stats.SSB,
			"ss_within":   stats.SSW,
			"df_between":  dfBetween,
			"df_within":   dfWithin,
			"ms_within":   msWithin,
			"effect_size": map[string]any{
				"eta_squared": eta2,
			},
		},
	}, nil
}

func (a *anovaRow) reset() {
	a.groups = make(map[string]*welfordBucket)
	a.order = nil
}

// anovaStats is the per-group breakdown consumed by ANOVA-style tests
// (tier 1 row test + tier 2 post test, plus future Tukey post-hoc).
type anovaStats struct {
	Ns    []int64
	Means []float64
	SSB   float64
	SSW   float64
	N     int64
}

// summariseANOVA folds k Welford buckets into the per-group counts,
// means, SSB, SSW, and total N. The post-test variant on tier 2 reuses
// this helper by constructing per-group buckets from (mean, variance, n)
// triples in the materialized result rows.
func summariseANOVA(order []string, groups map[string]*welfordBucket) anovaStats {
	ns := make([]int64, len(order))
	means := make([]float64, len(order))
	var totalN int64
	for i, key := range order {
		b := groups[key]
		ns[i] = b.n
		means[i] = b.mean
		totalN += b.n
	}
	if totalN == 0 {
		return anovaStats{Ns: ns, Means: means, N: 0}
	}
	var grandMean float64
	for _, b := range bucketsInOrder(order, groups) {
		grandMean += float64(b.n) * b.mean / float64(totalN)
	}
	var ssb, ssw float64
	for _, b := range bucketsInOrder(order, groups) {
		diff := b.mean - grandMean
		ssb += float64(b.n) * diff * diff
		ssw += b.m2
	}
	return anovaStats{
		Ns:    ns,
		Means: means,
		SSB:   ssb,
		SSW:   ssw,
		N:     totalN,
	}
}

func bucketsInOrder(order []string, groups map[string]*welfordBucket) []*welfordBucket {
	out := make([]*welfordBucket, len(order))
	for i, key := range order {
		out[i] = groups[key]
	}
	return out
}
