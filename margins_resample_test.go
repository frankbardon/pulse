package pulse

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// margins_resample_test.go covers marginal effects under the Resample
// modifier (U69 #639) end to end: predict admits every margins-capable
// built-in under jackknife and bootstrap and counts the effects'
// p-values the run emits (a multiplicity block corrects them as their own
// family), the blocks carry inference resample, REG_MULTINOMIAL's
// link-scale effects are its coefficients' replicate figures, and the
// resampled blocks answer bit for bit the same under every DecodeWorkers
// / ShardWorkers count. The figures are pinned to base-R replicate loops
// by internal/processing/regression/margins_resample_oracle_test.go.

func marginsResampleSpecs() []*types.RegressionSpec {
	xs := []string{"x1", "x2"}
	return []*types.RegressionSpec{
		{Type: types.REG_OLS, Name: "ols_jk", Target: "y", Predictors: xs, Resample: "jackknife", Margins: &types.RegressionMargins{}},
		{Type: types.REG_GLM, Name: "logit_boot", Family: "binomial", Target: "b", Predictors: xs, Resample: "bootstrap", BootstrapIters: 80,
			Margins: &types.RegressionMargins{}, Multiplicity: &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}},
		{Type: types.REG_GLM, Name: "poisson_mem_groups", Family: "poisson", Target: "c", Predictors: xs, Resample: "jackknife",
			JackknifeGroups: 6, RNGSeed: 2, Margins: &types.RegressionMargins{Type: types.MarginsMEM}},
	}
}

func multinomialResampleSpecs() []*types.RegressionSpec {
	return []*types.RegressionSpec{
		mnSpec(func(s *types.RegressionSpec) {
			s.Name, s.Resample, s.Margins = "mn_jk_link", "jackknife", &types.RegressionMargins{Scale: types.MarginsScaleLink}
		}),
		mnSpec(func(s *types.RegressionSpec) {
			s.Name, s.Resample, s.BootstrapIters, s.Margins = "mn_boot", "bootstrap", 40, &types.RegressionMargins{}
		}),
	}
}

// countEmitted counts a response's coefficient (non-intercept) and
// margins p-values, and the uncorrected ones.
func countEmitted(resp *Response) (emitted, uncorrected int) {
	for _, r := range resp.Regressions {
		coefs := 0
		for k := range r.PValues {
			if k != "(intercept)" && !strings.HasSuffix(k, ":(intercept)") {
				coefs++
			}
		}
		effects := len(r.Margins.PValues)
		emitted += coefs + effects
		if r.Multiplicity == nil {
			uncorrected += coefs
		}
		if r.Margins.Multiplicity == nil {
			uncorrected += effects
		}
	}
	return emitted, uncorrected
}

// TestMarginsResample_PredictMatchesRuntime: margins beside jackknife
// (leave-one-out, grouped) and bootstrap on REG_OLS, REG_GLM and
// REG_MULTINOMIAL pass predict and run alike; every block says
// inference resample; predict counts exactly the p-values the run
// emits (the holm member's effects corrected as their own family).
func TestMarginsResample_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	mnSingle, _ := mnModes(t)
	for _, c := range []struct {
		name string
		p    *Pulse
		req  *Request
	}{
		{"builtin", single[0].p, marginsReq(marginsCohort, marginsResampleSpecs()...)},
		{"multinomial", mnSingle[0].p, mnReq(mnCohort, multinomialResampleSpecs()...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			pr, err := c.p.Predict(ctx, c.req)
			if err != nil || !pr.Valid {
				t.Fatalf("predict: %v %+v", err, pr.Errors)
			}
			resp, err := c.p.Process(ctx, c.req)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range resp.Regressions {
				if r.Error != nil || r.Margins == nil || r.Margins.Inference != types.MarginsInferenceResample || r.Resample == "" {
					t.Fatalf("%s: error %v margins %+v", r.Name, r.Error, r.Margins)
				}
			}
			emitted, uncorrected := countEmitted(resp)
			if pr.PValues == nil || pr.PValues.Total != emitted || pr.PValues.Uncorrected != uncorrected {
				t.Fatalf("predict p_values %+v, the run emits %d (%d uncorrected)", pr.PValues, emitted, uncorrected)
			}
		})
	}
}

// TestMarginsResample_MultinomialLinkIsTheCoefficientTable: a resampled
// REG_MULTINOMIAL link-scale effect is its "<level>:<term>"
// coefficient, so its replicate SE, p-value and interval are the
// coefficient table's bit for bit.
func TestMarginsResample_MultinomialLinkIsTheCoefficientTable(t *testing.T) {
	t.Parallel()

	mnSingle, _ := mnModes(t)
	resp, err := mnSingle[0].p.Process(context.Background(), mnReq(mnCohort, multinomialResampleSpecs()[0]))
	if err != nil {
		t.Fatal(err)
	}
	r := resp.Regressions[0]
	for key, est := range r.Margins.Estimates {
		for _, f := range []struct {
			what      string
			got, want float64
		}{
			{"estimate", est, r.Coefficients[key]}, {"std_error", r.Margins.StdErrors[key], r.StdErrors[key]},
			{"p_value", r.Margins.PValues[key], r.PValues[key]}, {"ci_upper", r.Margins.CIUpper[key], r.CIUpper[key]},
		} {
			if math.Float64bits(f.got) != math.Float64bits(f.want) {
				t.Errorf("%s[%s] = %v, coefficient table %v", f.what, key, f.got, f.want)
			}
		}
	}
	if len(r.Margins.Estimates) == 0 {
		t.Fatal("no effects")
	}
}

// TestMarginsResample_WorkerInvariant: the resampled fits — margins
// included — are byte-identical across decode workers (one file) and
// shard workers (the archive), the derived-seed bootstrap too.
func TestMarginsResample_WorkerInvariant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, sharded := marginsModes(t)
	mnSingle, mnSharded := mnModes(t)
	for _, c := range []struct {
		groups [][]scorerParityMode
		req    func(cohort string) *Request
	}{
		{[][]scorerParityMode{single, sharded}, func(cohort string) *Request { return marginsReq(cohort, marginsResampleSpecs()...) }},
		{[][]scorerParityMode{mnSingle, mnSharded}, func(cohort string) *Request { return mnReq(cohort, multinomialResampleSpecs()...) }},
	} {
		for _, group := range c.groups {
			var want []byte
			for _, m := range group {
				resp, err := m.p.Process(ctx, c.req(m.cohort))
				if err != nil {
					t.Fatalf("%s: %v", m.name, err)
				}
				got, err := json.Marshal(resp.Regressions)
				if err != nil {
					t.Fatal(err)
				}
				if want == nil {
					want = got
				} else if string(got) != string(want) {
					t.Errorf("%s differs from %s:\n%s\nvs\n%s", m.name, group[0].name, got, want)
				}
			}
		}
	}
}
