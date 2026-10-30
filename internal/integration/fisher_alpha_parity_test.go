package integration_test

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFisherExact_InvalidAlphaMatchesPredict (U36 E1-S8): TEST_FISHER_EXACT's
// alpha also sets its odds-ratio interval's coverage (1 − alpha), and an
// alpha outside (0, 1) is refused PULSE_TEST_INVALID_ALPHA by the runtime
// and by predict alike; an in-range alpha runs and emits both bounds.
func TestFisherExact_InvalidAlphaMatchesPredict(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	mk := func(alpha float64) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Tests:  []*types.Test{{Type: types.TEST_FISHER_EXACT, Rows: "g", Cols: "g", Alpha: alpha}},
		}
	}
	for _, alpha := range []float64{-0.1, 1, 1.5} {
		_, err := p.Process(ctx, mk(alpha))
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_INVALID_ALPHA {
			t.Errorf("alpha %g: runtime error %v, want PULSE_TEST_INVALID_ALPHA", alpha, err)
		}
		env := predictEnvelope(t, p, fs, cohort, mk(alpha))
		if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_TEST_INVALID_ALPHA) {
			t.Errorf("alpha %g: predict errors %+v, want PULSE_TEST_INVALID_ALPHA", alpha, env.Errors)
		}
	}
	if env := predictEnvelope(t, p, fs, cohort, mk(0.1)); len(env.Errors) != 0 {
		t.Fatalf("predict refused alpha 0.1: %+v", *env.Errors[0])
	}
	resp, err := p.Process(ctx, mk(0.1))
	if err != nil {
		t.Fatalf("runtime refused alpha 0.1: %v", err)
	}
	d := resp.Tests[0].Details
	if _, ok := d["ci_low"].(float64); !ok {
		t.Errorf("details.ci_low = %v, want a float", d["ci_low"])
	}
	if _, ok := d["ci_high"].(float64); !ok {
		t.Errorf("details.ci_high = %v, want a float", d["ci_high"])
	}
}
