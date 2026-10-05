package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
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

	groups map[string]*weighting.Welford
	order  []string

	// testWeight: weighted, each group's moments are read on w* (N*_g).
	testWeight
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
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		splitBy:    spec.SplitBy,
		alpha:      alpha,
		groups:     make(map[string]*weighting.Welford),
		testWeight: newTestWeight(spec),
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
	w, ok := zt.rowWeight(record)
	if !ok {
		return nil
	}
	b, exists := zt.groups[key]
	if !exists {
		b = &weighting.Welford{}
		zt.groups[key] = b
		zt.order = append(zt.order, key)
	}
	b.Add(v, w)
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
			fmt.Sprintf("TEST_Z_TWO_SAMPLE sees %d groups", len(keys))+remedyTwoGroupsOrAnova(nil),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	a, b := zt.groups[keys[0]], zt.groups[keys[1]]
	if a.N < 2 || b.N < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_Z_TWO_SAMPLE requires n ≥ 2 per group, got %d / %d", a.N, b.N),
			map[string]any{"n": []int64{a.N, b.N}, "min_required": 2})
	}
	va := a.Variance(zt.basis)
	vb := b.Variance(zt.basis)
	if va == 0 && vb == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_Z_TWO_SAMPLE: both groups have zero variance",
			map[string]any{"groups": keys, "means": []float64{a.Mean, b.Mean}})
	}
	na := a.NStar(zt.basis)
	nb := b.NStar(zt.basis)
	se := math.Sqrt(va/na + vb/nb)
	if se == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_Z_TWO_SAMPLE: standard error is zero",
			map[string]any{"groups": keys})
	}
	diff := a.Mean - b.Mean
	zstat := diff / se
	p := normalTwoSidedP(zstat)
	zcrit := math.Sqrt2 * inverseErf(1-zt.alpha)
	ciLow := diff - zcrit*se
	ciHigh := diff + zcrit*se
	res := &types.TestResult{
		Label:      testLabel(zt.spec),
		Type:       types.TEST_Z_TWO_SAMPLE,
		Variant:    "two_sample_normal",
		Statistic:  zstat,
		PValue:     p,
		Alpha:      zt.alpha,
		RejectNull: p < zt.alpha,
		Details: map[string]any{
			"groups":   keys,
			"n":        []int64{a.N, b.N},
			"mean":     []float64{a.Mean, b.Mean},
			"variance": []float64{va, vb},
			"diff":     diff,
			"ci_low":   ciLow,
			"ci_high":  ciHigh,
		},
	}
	setEffectSize(res.Details, "cohens_d", cohensDTwoSample(diff, na, va, nb, vb))
	zt.noteGroups(res.Details, []*weighting.Welford{a, b})
	zt.checkNEff(zt.spec, true, keys[0], a.NEff(), 2)
	zt.checkNEff(zt.spec, true, keys[1], b.NEff(), 2)
	return res, nil
}

func (zt *zTestRow) reset() {
	zt.groups = make(map[string]*weighting.Welford)
	zt.order = nil
}
