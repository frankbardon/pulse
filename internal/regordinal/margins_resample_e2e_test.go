package regordinal_test

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// margins_resample_e2e_test.go: REG_ORDINAL's per-level margins under
// the Resample modifier (U69 #639). Every replicate refit reports its
// effects again (the model's extend.MarginsRegression), and their
// replicate spread replaces the delta-method standard error: on the link
// scale the level-k effect is −β_j, so its replicate SE is the
// coefficient's bit for bit; on the response scale the estimates stay the
// full fit's and the inference is the replicate one.

// TestRegOrdinal_MarginsUnderResample: jackknife and seeded bootstrap,
// link and response scale, predict admitting each block.
func TestRegOrdinal_MarginsUnderResample(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_two")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	plain := caseSpec(c, nil)
	plain.Vcov, plain.Margins = false, &types.RegressionMargins{}
	delta := fitOne(t, p, "ord.pulse", plain).Margins
	for _, tc := range []struct {
		name, resample, scale string
	}{
		{"jackknife_link", "jackknife", types.MarginsScaleLink},
		{"bootstrap_link", "bootstrap", types.MarginsScaleLink},
		{"jackknife_response", "jackknife", types.MarginsScaleResponse},
		{"bootstrap_response", "bootstrap", types.MarginsScaleResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := caseSpec(c, nil)
			s.Vcov = false
			s.Resample, s.BootstrapIters, s.RNGSeed = tc.resample, 60, 7
			s.Margins = &types.RegressionMargins{Scale: tc.scale}
			req := &types.Request{Cohort: &types.Cohort{Filename: "ord.pulse"}, Regressions: []*types.RegressionSpec{s}}
			if pr, err := p.Predict(context.Background(), req); err != nil || !pr.Valid {
				t.Fatalf("predict refused: %v %+v", err, pr.Errors)
			}
			res := fitOne(t, p, "ord.pulse", s)
			mg := res.Margins
			if mg == nil || mg.Inference != types.MarginsInferenceResample || mg.DF != 0 || len(mg.Outcomes) == 0 {
				t.Fatalf("margins %+v", mg)
			}
			for _, lv := range mg.Outcomes {
				for _, term := range mg.Terms {
					key := lv + ":" + term
					se := mg.StdErrors[key]
					if !(se > 0) || math.IsInf(se, 0) {
						t.Errorf("%s: replicate se %v", key, se)
					}
					if tc.scale == types.MarginsScaleLink {
						if mg.Estimates[key] != -res.Coefficients[term] || math.Float64bits(se) != math.Float64bits(res.StdErrors[term]) {
							t.Errorf("%s: link effect %v se %v, want -β %v se %v", key, mg.Estimates[key], se, -res.Coefficients[term], res.StdErrors[term])
						}
						continue
					}
					if mg.Estimates[key] != delta.Estimates[key] || se == delta.StdErrors[key] {
						t.Errorf("%s: estimate %v (full fit %v), se %v (delta %v)", key, mg.Estimates[key], delta.Estimates[key], se, delta.StdErrors[key])
					}
				}
			}
		})
	}
}
