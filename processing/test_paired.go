package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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

	field   string
	field2  string
	alpha   float64
	diffs   *welfordBucket
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
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical() || f.Type.IsGeo()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_PAIRED_T field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.Field2); f != nil && (f.Type.IsCategorical() || f.Type.IsGeo()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD2_NOT_NUMERIC,
				fmt.Sprintf("TEST_PAIRED_T field2 %q has non-numeric type %s", spec.Field2, f.Type.String()),
				map[string]any{"field2": spec.Field2, "field_type": f.Type.String()})
		}
	}
	return &pairedTRow{
		spec:   spec,
		schema: schema,
		field:  spec.Field,
		field2: spec.Field2,
		alpha:  alpha,
		diffs:  &welfordBucket{},
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
	p.diffs.add(a - b)
	return nil
}

func (p *pairedTRow) Finalize() (*types.TestResult, error) {
	defer p.reset()
	b := p.diffs
	if b.n < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_PAIRED_T requires n ≥ 2 complete pairs, got %d", b.n),
			map[string]any{"n": b.n, "min_required": 2})
	}
	variance := b.sampleVariance()
	if variance == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_PAIRED_T: sample variance of paired differences is zero",
			map[string]any{"n": b.n, "mean_diff": b.mean})
	}
	sd := math.Sqrt(variance)
	se := sd / math.Sqrt(float64(b.n))
	tstat := b.mean / se
	df := float64(b.n - 1)
	pvalue := studentTTwoSidedP(tstat, df)
	tcrit := studentTInverseTwoSided(p.alpha, df)
	ciLow := b.mean - tcrit*se
	ciHigh := b.mean + tcrit*se
	return &types.TestResult{
		Label:      testLabel(p.spec),
		Type:       types.TEST_PAIRED_T,
		Variant:    "paired_two_sided",
		Statistic:  tstat,
		DF:         df,
		PValue:     pvalue,
		Alpha:      p.alpha,
		RejectNull: pvalue < p.alpha,
		Details: map[string]any{
			"n":          b.n,
			"mean_diff":  b.mean,
			"variance":   variance,
			"ci_low":     ciLow,
			"ci_high":    ciHigh,
			"effect_size": map[string]any{
				// Cohen's d for paired samples: mean_diff / sd_diff.
				"cohens_d": b.mean / sd,
			},
		},
	}, nil
}

func (p *pairedTRow) reset() {
	p.diffs = &welfordBucket{}
}
