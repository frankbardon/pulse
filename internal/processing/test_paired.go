package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// pairedTRow implements TEST_PAIRED_T as a streaming row test on the
// per-row difference d = Field − Field2. The test reduces to a
// one-sample t-test on d against μ₀ = 0; state is a single Welford
// bucket so the algorithm composes with the existing streaming path
// at zero extra cost compared to TEST_T.
//
// Both Field and Field2 must be present and non-null on the same row;
// rows missing either value are dropped from the analysis (drop-pairs
// semantics, matching scipy.stats.ttest_rel default).
type pairedTRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field  string
	field2 string
	alpha  float64
	diffs  *weighting.Welford

	// testWeight: the weight rides the ROW, i.e. the pair; the test is
	// the one-sample weighted t on d.
	testWeight
}

func newPairedTRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PAIRED_T requires field")
	}
	if spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PAIRED_T requires field2 (paired column)")
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
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_PAIRED_T field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.Field2); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD2_NOT_NUMERIC,
				fmt.Sprintf("TEST_PAIRED_T field2 %q has non-numeric type %s", spec.Field2, f.Type.String()),
				map[string]any{"field2": spec.Field2, "field_type": f.Type.String()})
		}
	}
	return &pairedTRow{
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		field2:     spec.Field2,
		alpha:      alpha,
		diffs:      &weighting.Welford{},
		testWeight: newTestWeight(spec),
	}, nil
}

func (p *pairedTRow) UpdateRow(record *Record) error {
	a, aOk := record.NumericValue(p.field)
	if !aOk {
		return nil
	}
	b, bOk := record.NumericValue(p.field2)
	if !bOk {
		return nil
	}
	// A null in either field dropped the pair above, before its
	// weight is judged.
	w, ok := p.rowWeight(record)
	if !ok {
		return nil
	}
	p.diffs.Add(a-b, w)
	return nil
}

func (p *pairedTRow) Finalize() (*types.TestResult, error) {
	defer p.reset()
	b := p.diffs
	if b.N < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_PAIRED_T requires n ≥ 2 complete pairs, got %d", b.N),
			map[string]any{"n": b.N, "min_required": 2})
	}
	variance := b.Variance(p.basis)
	if variance == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_PAIRED_T: sample variance of paired differences is zero",
			map[string]any{"n": b.N, "mean_diff": b.Mean})
	}
	nStar := b.NStar(p.basis)
	sd := math.Sqrt(variance)
	se := sd / math.Sqrt(nStar)
	tstat := b.Mean / se
	df := nStar - 1
	pvalue := statdist.StudentTTwoSidedP(tstat, df)
	tcrit := statdist.StudentTInverseTwoSided(p.alpha, df)
	ciLow := b.Mean - tcrit*se
	ciHigh := b.Mean + tcrit*se
	res := &types.TestResult{
		Label:      testLabel(p.spec),
		Type:       types.TEST_PAIRED_T,
		Variant:    "paired_two_sided",
		Statistic:  tstat,
		DF:         df,
		PValue:     pvalue,
		Alpha:      p.alpha,
		RejectNull: pvalue < p.alpha,
		Details: map[string]any{
			"n":         b.N,
			"mean_diff": b.Mean,
			"variance":  variance,
			"ci_low":    ciLow,
			"ci_high":   ciHigh,
		},
	}
	// Cohen's d for paired samples: mean_diff / sd_diff.
	setEffectSize(res.Details, "cohens_d", cohensDOneSample(b.Mean, 0, sd))
	p.noteScalar(res.Details, b.SumW, b.SumWSq)
	p.checkNEff(p.spec, false, "", b.NEff(), 2)
	return res, nil
}

func (p *pairedTRow) reset() {
	p.diffs = &weighting.Welford{}
}
