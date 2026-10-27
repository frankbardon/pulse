package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// conf_level on a registered regression (owner decision #3): every
// extension model accepts it, and the engine writes Wald intervals
// coefficient ∓ q·SE — q the Student-t quantile on the model's reported
// df, else the standard normal (the normal on the replicate SE under
// resample; the parity suites pin those two against REG_OLS). A NaN SE
// is a null interval, key kept.

const regNoDF types.RegressionType = "REG_PARITY_NODF"

// noDFOLS is the buffered REG_OLS clone reporting no residual df and no
// standard error on the intercept.
type noDFOLS struct{ bufferedOLS }

func (m noDFOLS) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	out, err := m.bufferedOLS.Fit(rows)
	if err != nil {
		return out, err
	}
	out.Fit.DF = nil
	for i := range out.Coefficients {
		if out.Coefficients[i].Name == "(intercept)" {
			out.Coefficients[i].StdErr = nil
		}
	}
	return out, nil
}

func noDFExtensions() pulse.Extensions {
	return pulse.Extensions{Regressions: []pulse.RegressionRegistration{{
		Name: regNoDF, Description: "Test-only REG_OLS clone reporting no df.", PValues: true,
		Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
			return noDFOLS{bufferedOLS{spec: spec}}, nil
		},
	}}}
}

// TestExtensions_RegressionConfLevelNormalWithoutDF: a model reporting
// no df gets the normal interval at conf_level (0.95 unset), a
// coefficient without a standard error a null interval (key kept on
// the wire), and its df stays unreported.
func TestExtensions_RegressionConfLevelNormalWithoutDF(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, noDFExtensions())
	for _, conf := range []float64{0, 0.9} {
		spec := &types.RegressionSpec{Type: regNoDF, Name: "fit", Target: "score", Predictors: []string{"qty", "day"}, ConfLevel: conf}
		resp := processE2E(t, p, regressionRequest(cohort, spec))
		res := resp.Regressions[0]
		level := conf
		if level == 0 {
			level = 0.95
		}
		if res == nil || res.Error != nil || res.ConfLevel != level || res.DF != 0 || len(res.CILower) != 3 {
			t.Fatalf("conf %v: result = %+v", conf, res)
		}
		z := statdist.NormalQuantile(0.5 + level/2)
		for _, k := range []string{"qty", "day"} {
			half := z * res.StdErrors[k]
			if !closeRel(res.CILower[k], res.Coefficients[k]-half) || !closeRel(res.CIUpper[k], res.Coefficients[k]+half) {
				t.Errorf("conf %v %s: [%v, %v], want %v ∓ %v", conf, k, res.CILower[k], res.CIUpper[k], res.Coefficients[k], half)
			}
		}
		if !math.IsNaN(res.CILower["(intercept)"]) || !math.IsNaN(res.CIUpper["(intercept)"]) {
			t.Errorf("intercept interval = [%v, %v], want NaN (no SE)", res.CILower["(intercept)"], res.CIUpper["(intercept)"])
		}
		var wire struct {
			Regressions []struct {
				CILower map[string]*float64 `json:"ci_lower"`
			} `json:"regressions"`
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		v, ok := wire.Regressions[0].CILower["(intercept)"]
		if !ok || v != nil {
			t.Errorf("wire ci_lower[(intercept)] = %v (present %v), want null with the key kept", v, ok)
		}
	}
}

// TestExtensions_RegressionConfLevelRefusedPredictMatchesRuntime: a
// conf_level outside (0, 1) on an extension model is PROCESSING_CONFIG
// with the built-ins' message and details at predict and at runtime.
func TestExtensions_RegressionConfLevelRefusedPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, cohort := openE2E(t, noDFExtensions())
	for _, conf := range []float64{1, -0.5, 1.5} {
		req := regressionRequest(cohort, &types.RegressionSpec{Type: regNoDF, Name: "fit", Target: "score", Predictors: []string{"qty"}, ConfLevel: conf})
		_, err := p.Process(ctx, req)
		ce := requireCodedError(t, err, perr.PROCESSING_CONFIG)
		if ce.Details["conf_level"] != conf || ce.Details["type"] != string(regNoDF) {
			t.Fatalf("conf %v: runtime details = %v", conf, ce.Details)
		}
		pr, err := p.Predict(ctx, req)
		if err != nil || pr.Valid || len(pr.Errors) != 1 {
			t.Fatalf("conf %v: Predict = %+v / %v, want the one refusal", conf, pr, err)
		}
		e := pr.Errors[0]
		if e.Code != string(ce.Code) || e.Message != ce.Message || !reflect.DeepEqual(e.Details, ce.Details) {
			t.Fatalf("conf %v: predict %s %q %v, runtime %s %q %v", conf, e.Code, e.Message, e.Details, ce.Code, ce.Message, ce.Details)
		}
	}
	ok := regressionRequest(cohort, &types.RegressionSpec{Type: regNoDF, Name: "fit", Target: "score", Predictors: []string{"qty"}, ConfLevel: 0.5})
	if pr, err := p.Predict(ctx, ok); err != nil || !pr.Valid {
		t.Fatalf("conf 0.5: Predict = %+v / %v, want valid", pr, err)
	}
	processE2E(t, p, ok)
}

// TestExtensions_RegressionConfLevelDFFollowsInterval: df is the df of
// the t the intervals read, as on a built-in — a model's reported df
// on an unresampled fit, absent (and no longer a reported figure) under
// resample, whose intervals are normal.
func TestExtensions_RegressionConfLevelDFFollowsInterval(t *testing.T) {
	t.Parallel()

	p, cohort := modifierInstance(t)
	for _, resample := range []string{"", "jackknife", "bootstrap"} {
		spec := &types.RegressionSpec{Type: regParityOLS, Name: "fit", Target: "score", Predictors: []string{"qty", "day"},
			Resample: resample, BootstrapIters: 30, RNGSeed: 3}
		if resample != "bootstrap" {
			spec.BootstrapIters, spec.RNGSeed = 0, 0
		}
		e := fitOne(t, p, cohort, spec)
		reported := slices.Contains(e.ReportedFigures, "df")
		if resample == "" && (e.DF <= 0 || !reported) || resample != "" && (e.DF != 0 || reported) {
			t.Errorf("resample %q: df %v reported %v", resample, e.DF, reported)
		}
	}
}
