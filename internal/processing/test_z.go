package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
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
	// reference is params.reference ("" = alphabetical order).
	reference string
	// cuped is the params.covariate adjustment's state (nil without a
	// covariate): it replaces groups as the fold target.
	cuped *cupedArms

	groups map[string]*weighting.Welford
	order  []string

	// testWeight: weighted, each group's moments are read on w* (N*_g).
	testWeight
}

func newZTestRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if cerr := fieldaccept.RequiredSlotsRefusal(spec.Type, false, fieldaccept.TestSlotsOf(spec)); cerr != nil {
		return nil, cerr
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
		if cerr := testFieldTypeRefusal(spec, schema); cerr != nil {
			return nil, cerr
		}
		if cerr := fieldaccept.Check(string(spec.Type), fieldaccept.SlotSplitBy, spec.SplitBy, schema); cerr != nil {
			return nil, cerr
		}
	}
	params, err := decodeMeanTestParams(spec, schema)
	if err != nil {
		return nil, err
	}
	return &zTestRow{
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		splitBy:    spec.SplitBy,
		alpha:      alpha,
		reference:  params.Reference,
		cuped:      newCupedArms(params.Covariate),
		groups:     make(map[string]*weighting.Welford),
		testWeight: newTestWeight(spec),
	}, nil
}

func (zt *zTestRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(zt.field)
	if !ok {
		return nil
	}
	key, kOk := record.LevelValue(zt.splitBy)
	if !kOk {
		return nil
	}
	if zt.cuped != nil {
		x, ok := zt.cuped.covariateValue(record)
		if !ok {
			return nil
		}
		w, ok := zt.rowWeight(record)
		if !ok {
			return nil
		}
		if zt.cuped.add(key, x, v, w) {
			zt.order = append(zt.order, key)
		}
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
	keys, err := referenceOrder(types.TEST_Z_TWO_SAMPLE, zt.reference, keys)
	if err != nil {
		return nil, err
	}
	a, b := zt.groups[keys[0]], zt.groups[keys[1]]
	// Under params.covariate the test runs on the adjusted outcome's
	// moments (test_cuped.go).
	var cf *cupedFacts
	if zt.cuped != nil {
		adjA, adjB, f := zt.cuped.adjust(zt.cuped.groups[keys[0]], zt.cuped.groups[keys[1]])
		a, b, cf = &adjA, &adjB, &f
	}
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
	se2 := va/na + vb/nb
	se := math.Sqrt(se2)
	if se == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_Z_TWO_SAMPLE: standard error is zero",
			map[string]any{"groups": keys})
	}
	diff := a.Mean - b.Mean
	zstat := diff / se
	p := normalTwoSidedP(zstat)
	zcrit := normalCriticalTwoSided(zt.alpha)
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
	noteReference(res.Details, zt.reference)
	if cf != nil {
		diffRaw, seRaw2 := cf.rawWelch(zt.basis)
		cf.note(res.Details, diffRaw, se2, seRaw2)
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
	zt.cuped.reset()
}
