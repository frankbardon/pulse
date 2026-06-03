package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// zTestRow implements TEST_Z_TWO_SAMPLE as a streaming row test.
//
// The test compares two group means under the large-sample normal
// approximation: same Welch standard error as TEST_WELCH, but the p-value
// is the two-sided tail of the standard normal CDF Φ rather than the
// Student-t T_df. Use when n is large and survey conventions call for
// z-based inference on weighted-mean / proportion-style measures whose
// per-group std_dev is treated as known. Compare to TEST_WELCH for
// small-sample work where the Student-t correction matters.
//
// State is one Welford bucket per split key; identical to the two-sample
// path of tTestRow. Finalize diverges only in the CDF used to derive p.
type zTestRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field   string
	splitBy string
	alpha   float64

	groups map[string]*welfordBucket
	order  []string
}

func newZTestRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_Z_TWO_SAMPLE requires field (numeric measurement column)")
	}
	if spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_Z_TWO_SAMPLE requires split_by (categorical two-group column)")
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
		if f := schema.Field(spec.Field); f != nil && f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_Z_TWO_SAMPLE field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "type": f.Type.String()})
		}
		if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("TEST_Z_TWO_SAMPLE split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
				map[string]any{"split_by": spec.SplitBy, "type": f.Type.String()})
		}
	}
	return &zTestRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		groups:  make(map[string]*welfordBucket),
	}, nil
}

func (zt *zTestRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(zt.field)
	if !ok {
		return nil
	}
	key, kOk := record.StringValue(zt.splitBy)
	if !kOk {
		return nil
	}
	b, exists := zt.groups[key]
	if !exists {
		b = &welfordBucket{}
		zt.groups[key] = b
		zt.order = append(zt.order, key)
	}
	b.add(v)
	return nil
}

func (zt *zTestRow) Finalize() (*types.TestResult, error) {
	defer zt.reset()
	keys := append([]string(nil), zt.order...)
	sort.Strings(keys)
	if len(keys) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_Z_TWO_SAMPLE requires 2 split groups, got %d", len(keys)),
			map[string]any{"groups": keys, "min_required": 2})
	}
	if len(keys) > 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_Z_TWO_SAMPLE sees %d groups; specify a two-group SplitBy or use TEST_ANOVA_F", len(keys)),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	a, b := zt.groups[keys[0]], zt.groups[keys[1]]
	if a.n < 2 || b.n < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_Z_TWO_SAMPLE requires n ≥ 2 per group, got %d / %d", a.n, b.n),
			map[string]any{"n": []int64{a.n, b.n}, "min_required": 2})
	}
	va := a.sampleVariance()
	vb := b.sampleVariance()
	if va == 0 && vb == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_Z_TWO_SAMPLE: both groups have zero variance",
			map[string]any{"groups": keys, "means": []float64{a.mean, b.mean}})
	}
	na := float64(a.n)
	nb := float64(b.n)
	se := math.Sqrt(va/na + vb/nb)
	if se == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_Z_TWO_SAMPLE: standard error is zero",
			map[string]any{"groups": keys})
	}
	diff := a.mean - b.mean
	zstat := diff / se
	p := 2 * (1 - standardNormalCDF(math.Abs(zstat)))
	zcrit := math.Sqrt2 * inverseErf(1-zt.alpha)
	ciLow := diff - zcrit*se
	ciHigh := diff + zcrit*se
	pooled := math.Sqrt(((na-1)*va + (nb-1)*vb) / (na + nb - 2))
	var cohensD float64
	if pooled > 0 {
		cohensD = diff / pooled
	}
	return &types.TestResult{
		Label:      testLabel(zt.spec),
		Type:       types.TEST_Z_TWO_SAMPLE,
		Variant:    "two_sample_normal",
		Statistic:  zstat,
		PValue:     p,
		Alpha:      zt.alpha,
		RejectNull: p < zt.alpha,
		Details: map[string]any{
			"groups":   keys,
			"n":        []int64{a.n, b.n},
			"mean":     []float64{a.mean, b.mean},
			"variance": []float64{va, vb},
			"diff":     diff,
			"ci_low":   ciLow,
			"ci_high":  ciHigh,
			"effect_size": map[string]any{
				"cohens_d": cohensD,
			},
		},
	}, nil
}

func (zt *zTestRow) reset() {
	zt.groups = make(map[string]*welfordBucket)
	zt.order = nil
}
