package pulse_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// regression_holdout_test.go: RegressionSpec.Holdout through the public
// facade (transformed-predictors E3-S1) — the no_scorer refusal on a
// registered model without a scorer (predict = run), the `return`
// presets' keep / exclude of the holdout block, and the
// capability:regression_holdout gate.

// holdoutSpec is REG_OLS score ~ qty holding out every fourth qty.
func holdoutSpec(t types.RegressionType) *types.RegressionSpec {
	s := slopeSpec(t)
	s.Holdout = "qty % 4 == 0"
	return s
}

// TestHoldout_ExtensionWithoutScorerRefused: a holdout on a registered
// regression whose registration supplies no Scorer is
// PULSE_MODEL_SCORE_INVALID reason no_scorer, at predict and at run
// with the same message; the built-in REG_OLS beside it fits.
func TestHoldout_ExtensionWithoutScorerRefused(t *testing.T) {
	ctx := context.Background()
	p, cohort := openE2E(t, regressionExtensions(&regCalls{}))
	req := regressionRequest(cohort, holdoutSpec(regBufferedName))
	_, err := p.Process(ctx, req)
	ce := requireCodedError(t, err, perr.PULSE_MODEL_SCORE_INVALID)
	if ce.Details["reason"] != "no_scorer" || ce.Details["slot"] != "holdout" {
		t.Fatalf("details = %v, want reason no_scorer, slot holdout", ce.Details)
	}
	env, err := p.PredictEnvelope(ctx, req)
	if err != nil {
		t.Fatalf("PredictEnvelope: %v", err)
	}
	found := false
	for _, e := range env.Errors {
		found = found || (e.Code == string(ce.Code) && e.Message == ce.Message)
	}
	if !found {
		t.Fatalf("predict errors %+v lack %s %q", env.Errors, ce.Code, ce.Message)
	}

	resp := processE2E(t, p, regressionRequest(cohort, holdoutSpec(types.REG_OLS)))
	if r := resp.Regressions[0]; r.Error != nil || r.Holdout == nil || r.Holdout.N == 0 {
		t.Fatalf("REG_OLS holdout = %+v", r)
	}
}

// TestHoldout_ReturnPresets: `standard` and `minimal` keep the holdout
// block a spec asked for (as `full` does); excluding
// regressions[*].holdout drops it and nothing else of the fit.
func TestHoldout_ReturnPresets(t *testing.T) {
	p, cohort := openE2E(t, pulse.Extensions{})
	for _, tc := range []struct {
		name string
		ret  *types.Return
		keep bool
	}{
		{"default", nil, true},
		{"standard", &types.Return{Preset: types.ReturnPresetStandard}, true},
		{"minimal", &types.Return{Preset: types.ReturnPresetMinimal}, true},
		{"excluded", &types.Return{Preset: types.ReturnPresetStandard, Exclude: []string{"regressions[*].holdout"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := regressionRequest(cohort, holdoutSpec(types.REG_OLS))
			req.Return = tc.ret
			body, err := json.Marshal(processE2E(t, p, req))
			if err != nil {
				t.Fatal(err)
			}
			has := strings.Contains(string(body), `"holdout":{"n":`)
			if has != tc.keep {
				t.Fatalf("holdout block present = %v, want %v: %s", has, tc.keep, body)
			}
			if !strings.Contains(string(body), `"coefficients":{`) {
				t.Fatalf("coefficients dropped: %s", body)
			}
		})
	}
}

// TestHoldout_FeatureProfileGate: a profile without
// capability:regression_holdout refuses a set holdout as an unknown
// field; enabling it runs the fit; enabling it without any REG_* is a
// dependency fault at pulse.New.
func TestHoldout_FeatureProfileGate(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
	open := func(features ...string) (*pulse.Pulse, error) {
		return pulse.New(pulse.Options{FS: fsys, FeatureProfile: &pulse.FeatureProfile{Features: features}})
	}
	req := regressionRequest(&types.Cohort{Filename: "e2e.pulse"}, holdoutSpec(types.REG_OLS))

	hidden, err := open("capability:process", "REG_OLS")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = hidden.Process(ctx, req)
	ce := requireCodedError(t, err, perr.PULSE_REQUEST_UNKNOWN_FIELD)
	if ce.Details["path"] != "regressions[0].holdout" {
		t.Fatalf("details = %v", ce.Details)
	}

	enabled, err := open("capability:process", "REG_OLS", "capability:regression_holdout")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := enabled.Process(ctx, req)
	if err != nil || resp.Regressions[0].Holdout == nil {
		t.Fatalf("enabled Process: %v, %+v", err, resp)
	}

	_, err = open("capability:process", "capability:regression_holdout")
	requireCodedError(t, err, perr.PULSE_FEATURE_PROFILE_DEPENDENCY)
}
