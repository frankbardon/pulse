package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// regression_holdout_test.go: the no-execute half of RegressionSpec.
// Holdout (transformed-predictors E3-S1) — predict's streamability
// verdict and reason, the no_scorer rule and the capability gate.

func holdoutPredictSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64, Description: "Outcome value of the row"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Predictor value of the row"},
		{Name: "t", Type: encoding.FieldTypeF64, Description: "Week index of the row"},
	}}
}

// TestHoldout_PredictStreamableReason: a REG_OLS that streams alone
// buffers once it carries a holdout — predict says so with the holdout
// reason and the runtime gate (processing.CanStreamRequest) agrees.
func TestHoldout_PredictStreamableReason(t *testing.T) {
	schema := holdoutPredictSchema()
	data := buildTestPulseFile(t, schema)
	for _, holdout := range []string{"", "t >= 8"} {
		req := &types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Holdout: holdout}}}
		env := predictFromBytes(data, req, nil)
		if len(env.Errors) != 0 {
			t.Fatalf("holdout %q: predict errors %+v", holdout, env.Errors)
		}
		res := env.Data.(*descriptor.PredictResult)
		runtime := processing.CanStreamRequest(req, schema)
		if res.Streamable != runtime {
			t.Fatalf("holdout %q: predict Streamable %v, runtime %v", holdout, res.Streamable, runtime)
		}
		reason := HoldoutStreamableReason(types.REG_OLS)
		if holdout == "" {
			if !res.Streamable {
				t.Fatalf("plain REG_OLS buffers: %v", res.StreamableReasons)
			}
			continue
		}
		if res.Streamable || !slices.Contains(res.StreamableReasons, reason) {
			t.Fatalf("holdout: Streamable %v, reasons %v; want false with %q", res.Streamable, res.StreamableReasons, reason)
		}
	}
}

// TestHoldout_NoScorerRefused: a holdout on a regression the instance
// cannot score is PULSE_MODEL_SCORE_INVALID reason no_scorer (the walk's
// model.HoldoutScorerRefusal); a scorable one passes.
func TestHoldout_NoScorerRefused(t *testing.T) {
	if cerr := model.HoldoutScorerRefusal(&types.RegressionSpec{Type: types.REG_OLS, Holdout: "x > 1"}, nil); cerr != nil {
		t.Fatalf("REG_OLS refused: %v", cerr)
	}
	spec := &types.RegressionSpec{Type: "REG_ACME_NOSCORE", Name: "m", Holdout: "x > 1"}
	cerr := model.HoldoutScorerRefusal(spec, func(types.RegressionType) bool { return false })
	if cerr == nil || cerr.Code != errors.PULSE_MODEL_SCORE_INVALID || cerr.Details["reason"] != model.ReasonNoScorer ||
		cerr.Details["slot"] != "holdout" || cerr.Details["name"] != "m" {
		t.Fatalf("refusal = %+v", cerr)
	}
	spec.Holdout = ""
	if cerr := model.HoldoutScorerRefusal(spec, func(types.RegressionType) bool { return false }); cerr != nil {
		t.Fatalf("no holdout refused: %v", cerr)
	}
}

// TestHoldout_HiddenCapabilityRefused: on an instance without
// capability:regression_holdout a set holdout is an unknown field at
// its regression (details.path), and the key is absent from the
// visible keys; enabled, it passes the gate.
func TestHoldout_HiddenCapabilityRefused(t *testing.T) {
	req := &types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Holdout: "t > 1"}}}
	hidden := scopedOnly(featProcess, "REG_OLS")
	err := SlotRefusal(req, hidden)
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || ce.Details["path"] != "regressions[0].holdout" {
		t.Fatalf("hidden: err = %v", err)
	}
	if slices.Contains(VisibleSlotKeys(req.Regressions[0], hidden), "holdout") {
		t.Fatal("hidden instance lists holdout as a valid key")
	}
	if err := SlotRefusal(req, scopedOnly(featProcess, "REG_OLS", featRegHoldout)); err != nil {
		t.Fatalf("enabled: err = %v", err)
	}
}
