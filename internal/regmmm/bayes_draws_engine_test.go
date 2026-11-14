package regmmm_test

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// bayes_draws_engine_test.go holds params.return_draws to its contract
// through the engine on a stock instance: Limits.MaxMMMDrawValues is
// predict's Certain finding and refusal on one market (the identical
// error the runtime pre-flight raises, before any sampling) and a
// Possible finding on a geo panel (the runtime's refusal over the geos
// it read); and the standard return preset (the MCP default) keeps
// outputs.draws exactly when the spec asked for it.

// tinyDraws is a one-market fit small enough to run the path.
const tinyDraws = `{"channels":["tv"],"time":"week","chains":1,"warmup":10,"draws":6,"return_draws":true,"draws_thin":2}`

// TestMMMBayes_DrawLimitPredictMatchesRuntime: one market, 1 chain x 3
// kept draws x (7 coefficients + 1 roi entry) = 24 values: under a
// limit of 23 predict reports a certain max_mmm_draw_values finding and
// the PULSE_LIMIT_EXCEEDED error the runtime refuses with (code,
// message, details), never sampling; at 24 both clear. A two-market
// panel under a limit below its payload is possible at predict (the
// geo dictionary bounds it) and refused at runtime with the same figure.
func TestMMMBayes_DrawLimitPredictMatchesRuntime(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	c := &countingSampler{}
	withSampler(t, c)
	ctx := context.Background()
	for _, tc := range []struct {
		name, file string
		spec       *types.RegressionSpec
		grade      string
		geo        bool
	}{
		{"one market", regmmm.BayesCohort, regmmm.BayesSpec(tinyDraws), "certain", false},
		{"geo panel", "panel.pulse", regmmm.BayesSpec(`{"channels":["tv"],"time":"week","geo":"geo","chains":1,"warmup":10,"draws":5,"return_draws":true}`), "possible", true},
	} {
		want, _, ok := regmmm.BayesDrawValues(tc.spec, 2)
		if !ok {
			t.Fatalf("%s: no draws payload", tc.name)
		}
		p := bayesInstance(t, func(o *pulse.Options) { o.Limits.MaxMMMDrawValues = want - 1 })
		writeBayesCohort(t, p, "panel.pulse", encoding.FieldTypeU16, panelRows())
		req := bayesRequest(tc.file, tc.spec)
		pr, err := p.Predict(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, f := range pr.LimitFindings {
			if f.Limit == "max_mmm_draw_values" {
				found = true
				if string(f.Grade) != tc.grade || f.Estimated != want || f.Configured != want-1 {
					t.Errorf("%s: finding %+v, want %s, estimated %d", tc.name, f, tc.grade, want)
				}
			}
		}
		if !found {
			t.Fatalf("%s: predict findings %s lack max_mmm_draw_values", tc.name, mustJSON(t, pr.LimitFindings))
		}
		runs := c.runs.Load()
		_, rerr := p.Process(ctx, req)
		var ce *perr.CodedError
		if !stderrors.As(rerr, &ce) || ce.Code != perr.PULSE_LIMIT_EXCEEDED || ce.Details["limit"] != "max_mmm_draw_values" ||
			ce.Details["observed"] != want || ce.Details["configured"] != want-1 {
			t.Fatalf("%s: runtime %v, want PULSE_LIMIT_EXCEEDED max_mmm_draw_values observed %d", tc.name, rerr, want)
		}
		if n := c.runs.Load() - runs; n != 0 {
			t.Errorf("%s: a refused fit sampled %d times", tc.name, n)
		}
		switch {
		case !tc.geo && (len(pr.Errors) != 1 || pr.Errors[0].Code != string(ce.Code) || pr.Errors[0].Message != ce.Message ||
			mustJSON(t, pr.Errors[0].Details) != mustJSON(t, ce.Details)):
			t.Errorf("%s: predict errors %s, runtime %s %q %s", tc.name, mustJSON(t, pr.Errors), ce.Code, ce.Message, mustJSON(t, ce.Details))
		case tc.geo && len(pr.Errors) != 0:
			t.Errorf("%s: a possible finding is an error: %s", tc.name, mustJSON(t, pr.Errors))
		}

		at := bayesInstance(t, func(o *pulse.Options) { o.Limits.MaxMMMDrawValues = want })
		writeBayesCohort(t, at, "panel.pulse", encoding.FieldTypeU16, panelRows())
		pr, err = at.Predict(ctx, req)
		if err != nil || len(pr.LimitFindings) != 0 || len(pr.Errors) != 0 {
			t.Fatalf("%s at the limit: predict %v %s %s", tc.name, err, mustJSON(t, pr.LimitFindings), mustJSON(t, pr.Errors))
		}
		if r := process(t, at, req).Regressions[0]; r.Error != nil || r.Outputs["draws"] == nil {
			t.Fatalf("%s at the limit: %s", tc.name, mustJSON(t, r))
		}
	}
}

// TestMMMBayes_DrawsUnderReturnPresets: under the standard and minimal
// presets (standard is the MCP default) outputs.draws is on the wire
// when return_draws asked for it and absent when it did not, and an
// exclude of regressions[*].outputs.draws drops it alone.
func TestMMMBayes_DrawsUnderReturnPresets(t *testing.T) {
	t.Parallel()

	p := bayesInstance(t, nil)
	run := func(params string, ret *types.Return) map[string]any {
		req := bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(params))
		req.Return = ret
		r := process(t, p, req).Regressions[0]
		if r.Error != nil {
			t.Fatalf("%s: %s", params, mustJSON(t, r))
		}
		return r.Outputs
	}
	const without = `{"channels":["tv"],"time":"week","chains":1,"warmup":10,"draws":6}`
	for _, preset := range []types.ReturnPreset{types.ReturnPresetStandard, types.ReturnPresetMinimal} {
		ret := &types.Return{Preset: preset}
		if out := run(tinyDraws, ret); out["draws"] == nil || out["roi"] == nil {
			t.Errorf("%s with return_draws: outputs keys %v, want draws", preset, keysOf(out))
		}
		if out := run(without, ret); out["draws"] != nil || out["roi"] == nil {
			t.Errorf("%s without return_draws: outputs keys %v, want no draws", preset, keysOf(out))
		}
	}
	out := run(tinyDraws, &types.Return{Preset: types.ReturnPresetStandard, Exclude: []string{"regressions[*].outputs.draws"}})
	if out["draws"] != nil || out["roi"] == nil {
		t.Errorf("exclude draws: outputs keys %v, want roi without draws", keysOf(out))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
