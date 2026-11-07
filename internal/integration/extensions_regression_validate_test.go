package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// An extension regression's opt-in, spec-only Validate hook
// (RegressionRegistration.Validate, an extend.SpecValidator) is judged
// at predict exactly as at runtime, where it runs before the factory
// (U67 E1-S1): a coded error keeps its code, an uncoded one or a panic
// is PULSE_EXTENSION_SPEC_REFUSED {category, type, slot[, name][,
// panic]}, byte-identical on both sides.

const regValidatedName types.RegressionType = "REG_ACME_SLOPE_VALID"

// slopeK reads params.k off spec (absent: ok false).
func slopeK(spec *types.RegressionSpec) (int, bool) {
	if len(spec.Params) == 0 {
		return 0, false
	}
	var p struct {
		K *int `json:"k"`
	}
	if err := json.Unmarshal(spec.Params, &p); err != nil || p.K == nil {
		return 0, false
	}
	return *p.K, true
}

// validateSlopeK refuses params.k by value: negative — an uncoded
// error; zero — a wrapped coded one; 99 — a panic. Anything else (and
// no k, the probe spec) is accepted.
func validateSlopeK(spec *types.RegressionSpec) error {
	k, ok := slopeK(spec)
	switch {
	case !ok:
		return nil
	case k < 0:
		return fmt.Errorf("params.k must be non-negative, got %d", k)
	case k == 0:
		return fmt.Errorf("wrapped: %w", perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG,
			"regression "+string(spec.Type)+" params.k must not be zero", map[string]any{"regression": string(spec.Type), "key": "k"}))
	case k == 99:
		panic("k is 99")
	}
	return nil
}

// validatedRegression is a streaming slope registration carrying
// validate, counting its factory calls in built.
func validatedRegression(validate extend.SpecValidator[types.RegressionSpec], built *atomic.Int64) pulse.Extensions {
	return pulse.Extensions{Regressions: []pulse.RegressionRegistration{{
		Name: regValidatedName, Description: "slope with a spec validator", Streamable: true, PValues: true,
		OutputSchema: slopeOutputs, Validate: validate,
		Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
			built.Add(1)
			return &streamSlope{acc: newSlopeAcc(spec), calls: &regCalls{}}, nil
		},
	}}}
}

// TestExtensions_RegressionValidatePredictMatchesRuntime is the parity
// gate: every refusal a Validate hook raises — uncoded, coded (wrapped),
// panic, and under a resample modifier (judged before the modifier
// engine builds a refit) — is predict's only error and Process's error,
// code, message and details identical, and the runtime never reaches
// the factory. An accepted spec runs on both sides; a spec an engine
// rule refuses first is that rule's refusal on both sides, the hook not
// consulted at predict.
func TestExtensions_RegressionValidatePredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	var built atomic.Int64
	p, cohort := openE2E(t, validatedRegression(validateSlopeK, &built))
	withK := func(k int, mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := slopeSpec(regValidatedName)
		s.Params = json.RawMessage(fmt.Sprintf(`{"k":%d}`, k))
		if mut != nil {
			mut(s)
		}
		return s
	}
	unnamed := func(s *types.RegressionSpec) { s.Name = "" }
	resample := func(s *types.RegressionSpec) { s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 20, 7 }
	refusals := []struct {
		name    string
		spec    *types.RegressionSpec
		code    perr.Code
		message string
		details map[string]any
	}{
		{name: "uncoded", spec: withK(-2, nil), code: perr.PULSE_EXTENSION_SPEC_REFUSED,
			message: "regression REG_ACME_SLOPE_VALID refused its spec: params.k must be non-negative, got -2",
			details: map[string]any{"category": "regression", "type": string(regValidatedName), "slot": "regressions", "name": "fit"}},
		{name: "uncoded_unnamed", spec: withK(-1, unnamed), code: perr.PULSE_EXTENSION_SPEC_REFUSED,
			message: "regression REG_ACME_SLOPE_VALID refused its spec: params.k must be non-negative, got -1",
			details: map[string]any{"category": "regression", "type": string(regValidatedName), "slot": "regressions"}},
		{name: "coded_kept", spec: withK(0, nil), code: perr.PROCESSING_CONFIG,
			message: "regression REG_ACME_SLOPE_VALID params.k must not be zero",
			details: map[string]any{"regression": string(regValidatedName), "key": "k"}},
		{name: "panic", spec: withK(99, nil), code: perr.PULSE_EXTENSION_SPEC_REFUSED,
			message: "regression REG_ACME_SLOPE_VALID Validate panicked: k is 99",
			details: map[string]any{"category": "regression", "type": string(regValidatedName), "slot": "regressions", "name": "fit", "panic": "k is 99"}},
		{name: "under_resample", spec: withK(-3, resample), code: perr.PULSE_EXTENSION_SPEC_REFUSED,
			message: "regression REG_ACME_SLOPE_VALID refused its spec: params.k must be non-negative, got -3",
			details: map[string]any{"category": "regression", "type": string(regValidatedName), "slot": "regressions", "name": "fit"}},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			req := regressionRequest(cohort, tc.spec)
			before := built.Load()
			_, err := p.Process(context.Background(), req)
			ce := requireCodedError(t, err, tc.code)
			if ce.Message != tc.message || !reflect.DeepEqual(ce.Details, tc.details) {
				t.Errorf("runtime = %q %v, want %q %v", ce.Message, ce.Details, tc.message, tc.details)
			}
			if n := built.Load() - before; n != 0 {
				t.Errorf("runtime built the model %d times past a Validate refusal, want 0", n)
			}
			pr, perrv := p.Predict(context.Background(), req)
			if perrv != nil {
				t.Fatalf("Predict: %v", perrv)
			}
			if len(pr.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly the Validate refusal", pr.Errors)
			}
			if got := pr.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
				t.Errorf("predict = %+v, runtime %s %q %v", got, ce.Code, ce.Message, ce.Details)
			}
		})
	}

	t.Run("accepted", func(t *testing.T) {
		req := regressionRequest(cohort, withK(4, nil))
		resp := processE2E(t, p, req)
		if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
			t.Fatalf("accepted spec did not fit: %+v", resp.Regressions)
		}
		pr, err := p.Predict(context.Background(), req)
		if err != nil || len(pr.Errors) != 0 {
			t.Fatalf("predict refused an accepted spec: %v %+v", err, pr.Errors)
		}
	})

	// conf_level 2 is the engine's own refusal, judged before the hook
	// at runtime; predict reports it alone (the hook is not consulted
	// on a spec an engine rule refused).
	t.Run("engine_rule_first", func(t *testing.T) {
		req := regressionRequest(cohort, withK(-1, func(s *types.RegressionSpec) { s.ConfLevel = 2 }))
		_, err := p.Process(context.Background(), req)
		ce := requireCodedError(t, err, perr.PROCESSING_CONFIG)
		pr, perrv := p.Predict(context.Background(), req)
		if perrv != nil {
			t.Fatalf("Predict: %v", perrv)
		}
		if len(pr.Errors) != 1 || pr.Errors[0].Code != string(ce.Code) || pr.Errors[0].Message != ce.Message {
			t.Errorf("predict = %+v, runtime %s %q", pr.Errors, ce.Code, ce.Message)
		}
	})
}

// TestExtensions_RegressionValidateProbed: pulse.New runs Validate on
// the probe spec under recover() — a hook that panics there, or refuses
// it, fails registration with PULSE_EXTENSION_FACTORY_PANIC
// (details.stage validate, the hook's verdict under validate_code /
// validate_error, details.panic on a panic) before the factory is
// probed; a hook accepting the probe spec registers.
func TestExtensions_RegressionValidateProbed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		validate extend.SpecValidator[types.RegressionSpec]
		wantErr  bool
		details  map[string]any
	}{
		{name: "panics", validate: func(*types.RegressionSpec) error { panic("always") }, wantErr: true,
			details: map[string]any{"category": "regression", "name": string(regValidatedName), "stage": "validate",
				"validate_code": string(perr.PULSE_EXTENSION_SPEC_REFUSED), "panic": "always",
				"validate_error": "regression REG_ACME_SLOPE_VALID Validate panicked: always"}},
		{name: "refuses", validate: func(*types.RegressionSpec) error { return fmt.Errorf("never") }, wantErr: true,
			details: map[string]any{"category": "regression", "name": string(regValidatedName), "stage": "validate",
				"validate_code":  string(perr.PULSE_EXTENSION_SPEC_REFUSED),
				"validate_error": "regression REG_ACME_SLOPE_VALID refused its spec: never"}},
		{name: "accepts", validate: validateSlopeK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var built atomic.Int64
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: validatedRegression(tc.validate, &built)})
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("pulse.New: %v", err)
				}
				if built.Load() == 0 {
					t.Error("the probe never built the factory past an accepting Validate")
				}
				return
			}
			ce := requireCodedError(t, err, perr.PULSE_EXTENSION_FACTORY_PANIC)
			if !reflect.DeepEqual(ce.Details, tc.details) {
				t.Errorf("details = %v, want %v", ce.Details, tc.details)
			}
			if built.Load() != 0 {
				t.Errorf("the probe built the factory %d times past a Validate refusal, want 0", built.Load())
			}
		})
	}
}
