package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// brownForsytheRow implements TEST_BROWN_FORSYTHE as a buffered row test:
// homogeneity-of-variance check by running one-way ANOVA on the absolute
// deviation from each group's median.
//
// Algorithm:
//   1. Buffer values per group.
//   2. For each group, compute median, then z_ij = |x_ij − median|.
//   3. Run standard one-way ANOVA on z values across groups.
//
// p-value via the existing fSurvival on (k−1, N−k) degrees of freedom.
// Median-based residuals make the test robust against non-normality —
// the conventional preferred variant over Levene's mean-based residuals.
type brownForsytheRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	alpha   float64

	values map[string][]float64
	order  []string
}

func newBrownForsytheRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_BROWN_FORSYTHE requires field")
	}
	if spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_BROWN_FORSYTHE requires split_by")
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
				fmt.Sprintf("TEST_BROWN_FORSYTHE field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("TEST_BROWN_FORSYTHE split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
				map[string]any{"split_by": spec.SplitBy, "field_type": f.Type.String()})
		}
	}
	return &brownForsytheRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		values:  make(map[string][]float64),
	}, nil
}

func (b *brownForsytheRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(b.field)
	if !ok {
		return nil
	}
	key, ok := record.StringValue(b.splitBy)
	if !ok {
		return nil
	}
	if _, exists := b.values[key]; !exists {
		b.order = append(b.order, key)
	}
	b.values[key] = append(b.values[key], v)
	return nil
}

func (b *brownForsytheRow) Finalize() (*types.TestResult, error) {
	defer b.reset()
	keys := append([]string(nil), b.order...)
	sort.Strings(keys)
	k := len(keys)
	if k < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_BROWN_FORSYTHE requires ≥ 2 groups, got %d", k),
			map[string]any{"groups": keys, "min_required": 2})
	}
	// Compute median per group, then |dev| → folded into Welford buckets.
	medians := make([]float64, k)
	devBuckets := make(map[string]*welfordBucket, k)
	devOrder := make([]string, 0, k)
	for i, key := range keys {
		vs := append([]float64(nil), b.values[key]...)
		if len(vs) < 2 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_BROWN_FORSYTHE requires n ≥ 2 per group; %q has %d", key, len(vs)),
				map[string]any{"group": key, "n": len(vs), "min_required": 2})
		}
		sort.Float64s(vs)
		medians[i] = median(vs)
		bk := &welfordBucket{}
		for _, v := range vs {
			bk.add(math.Abs(v - medians[i]))
		}
		devBuckets[key] = bk
		devOrder = append(devOrder, key)
	}
	stats := summariseANOVA(devOrder, devBuckets)
	dfB := float64(k - 1)
	dfW := float64(int64(stats.N) - int64(k))
	if dfW <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			"TEST_BROWN_FORSYTHE: zero within-group degrees of freedom",
			map[string]any{"k": k, "n": stats.N})
	}
	msB := stats.SSB / dfB
	msW := stats.SSW / dfW
	var F, p float64
	if msW == 0 {
		F = 0
		p = 0
	} else {
		F = msB / msW
		p = fSurvival(F, dfB, dfW)
	}
	return &types.TestResult{
		Label:      testLabel(b.spec),
		Type:       types.TEST_BROWN_FORSYTHE,
		Variant:    "median",
		Statistic:  F,
		DF:         dfB,
		PValue:     p,
		Alpha:      b.alpha,
		RejectNull: p < b.alpha,
		Details: map[string]any{
			"groups":         keys,
			"n":              stats.Ns,
			"group_medians":  medians,
			"abs_dev_means":  stats.Means,
			"ss_between":     stats.SSB,
			"ss_within":      stats.SSW,
			"df_between":     dfB,
			"df_within":      dfW,
		},
	}, nil
}

func (b *brownForsytheRow) reset() {
	b.values = make(map[string][]float64)
	b.order = nil
}

// median returns the median of a *sorted* slice. Caller must sort.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return 0.5 * (sorted[n/2-1] + sorted[n/2])
}
