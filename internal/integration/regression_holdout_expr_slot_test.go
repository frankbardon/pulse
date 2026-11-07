package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// A regression holdout is one of the request expressions the shared
// reference / static checks read (vectors.ExprSlots, U67 E6-S2): the
// runtime compiles it with those checks and binds the request's
// vectors in it, so a ref() or reserved-built-in fault there is
// reported by predict — slot regressions[i].holdout — under the code,
// message and details Process and ProcessStream refuse it with.

// holdoutRequest fits qty on score, holding out the rows holdout keeps.
func holdoutRequest(holdout string) *types.Request {
	return &types.Request{
		Cohort:      &types.Cohort{Filename: "ref.pulse"},
		Vectors:     []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}, Labels: []string{"s", "q"}}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "qty", Predictors: []string{"score"}, Holdout: holdout}},
	}
}

// TestRegressionHoldout_ExprSlotRefusalParity: each fault is one
// predict error at the holdout slot, the runtime's code, message and
// details (on Process and ProcessStream); a well-formed ref() and a
// vector read in a holdout are accepted by both, and the vector it
// reads is not reported unreferenced by either.
func TestRegressionHoldout_ExprSlotRefusalParity(t *testing.T) {
	t.Parallel()

	p, data := openReferenceCohort(t, refRegionTables())
	ctx := context.Background()
	cases := map[string]struct {
		holdout string
		ref     bool // a reference issue: predict adds details.slot
	}{
		"unknown table":       {`dot(v, ref("nope", "north")) > 0`, true},
		"unknown literal key": {`dot(v, ref("w", "nowhere")) > 0`, true},
		"labels differ":       {`cosine(v, ref("other", "k")) > 0`, true},
		"vnormalize mode":     {`vsum(vnormalize(v, "l3")) > 0`, false},
		"length mismatch":     {`dot(v, [1, 2, 3]) > 0`, false},
		"arity":               {`dist(v, v, v) > 0`, false},
	}
	for name, tc := range cases {
		holdout := tc.holdout
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env, err := p.PredictBytes(ctx, data, holdoutRequest(holdout))
			if err != nil {
				t.Fatal(err)
			}
			_, rerr := p.Process(ctx, holdoutRequest(holdout))
			if rerr == nil {
				t.Fatal("Process accepted the holdout fault")
			}
			_, serr := p.ProcessStream(ctx, holdoutRequest(holdout))
			if serr == nil || serr.Error() != rerr.Error() {
				t.Fatalf("ProcessStream %v, Process %v", serr, rerr)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors %+v, want one (runtime %v)", env.Errors, rerr)
			}
			if tc.ref {
				if slot := env.Errors[0].Details["slot"]; slot != "regressions[0].holdout" {
					t.Errorf("predict slot %v, want regressions[0].holdout", slot)
				}
				delete(env.Errors[0].Details, "slot")
			}
			sameEntry(t, env, rerr)
		})
	}

	for _, ok := range []string{`dot(v, ref("w", "north")) > 0`, `vmean(v) > 1`} {
		env, err := p.PredictBytes(ctx, data, holdoutRequest(ok))
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 0 {
			t.Errorf("%s: predict refused a well-formed holdout: %+v", ok, env.Errors)
		}
		for _, w := range env.Warnings {
			if w.Code == string(errors.PULSE_VECTOR_UNREFERENCED) {
				t.Errorf("%s: predict warned %s", ok, w.Message)
			}
		}
		resp, err := p.Process(ctx, holdoutRequest(ok))
		if err != nil {
			t.Fatalf("%s: Process refused a well-formed holdout: %v", ok, err)
		}
		for _, w := range resp.Warnings {
			if w.Code == string(errors.PULSE_VECTOR_UNREFERENCED) {
				t.Errorf("%s: runtime warned %s", ok, w.Message)
			}
		}
	}
}
