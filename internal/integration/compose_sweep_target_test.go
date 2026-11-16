package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compose_sweep_target_test.go — a sweep axis `target: "params.x"`
// (compose-sweep-2 E2-S1, #521): each slot's request takes the axis
// value as params.x, an expression reading params.x varies with the
// axis, and predict and the runtime agree on the slots and on every
// refusal. Fixture: openParams (x = 0..19).

// targetSweep is a compose batch whose sweep counts the rows with
// x >= params.cut over cut ∈ {5, 10, 15}, the axis writing params.cut.
// body is the sweep request body's extra keys (JSON, "" for none).
func targetSweep(t *testing.T, axis, body string) *pulse.ComposedRequest {
	t.Helper()
	raw := `{"sweep":{"axes":[` + axis + `],"request":{"cohort":{"filename":"params.pulse"},` + body +
		`"filterers":[{"type":"FILTER_EXPRESSION","expression":"x >= params.cut"}],` +
		`"aggregations":[{"type":"AGG_COUNT","field":"x","label":"c"}]}}}`
	var c pulse.ComposedRequest
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return &c
}

const cutAxis = `{"name":"cut","values":[5,10,15],"target":"params.cut"}`

// TestComposeSweepTarget_DrivesParam: the targeted value reaches the
// expression — declared (no placeholder in the body), overriding the
// body's own params.cut and the batch-level one — and predict lists the
// same slots, echoing the target.
func TestComposeSweepTarget_DrivesParam(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	cases := []struct {
		name string
		req  *pulse.ComposedRequest
	}{
		{"declares", targetSweep(t, cutAxis, "")},
		{"overrides body", targetSweep(t, cutAxis, `"params":{"cut":0,"other":1},`)},
	}
	inherited := targetSweep(t, cutAxis, "")
	inherited.Params = types.Params{"cut": json.Number("0")}
	cases = append(cases, struct {
		name string
		req  *pulse.ComposedRequest
	}{"overrides batch", inherited})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := p.Compose(ctx, c.req)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if len(resp.Responses) != 3 {
				t.Fatalf("responses = %d, want 3", len(resp.Responses))
			}
			for i, want := range []float64{15, 10, 5} {
				if got := paramsFloat(t, resp.Responses[i], "c"); got != want {
					t.Fatalf("slot %d count = %v, want %v", i, got, want)
				}
			}
			env, err := p.PredictCompose(ctx, c.req)
			if err != nil {
				t.Fatalf("PredictCompose: %v", err)
			}
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors: %+v", env.Errors)
			}
			res := env.Data.(*pulse.ComposePredictResult)
			if res.Sweep == nil || len(res.Sweep.Axes) != 1 || res.Sweep.Axes[0].Target != "params.cut" {
				t.Fatalf("sweep summary = %+v", res.Sweep)
			}
			if fmt.Sprint(res.Sweep.Labels) != "[cut=5 cut=10 cut=15]" || len(res.Slots) != 3 {
				t.Fatalf("labels %v, slots %d", res.Sweep.Labels, len(res.Slots))
			}
		})
	}
}

// TestComposeSweepTarget_MatchesVarRoute: the target route and the
// `{"$var"}` splice route run the same slots to the same figures.
func TestComposeSweepTarget_MatchesVarRoute(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	viaTarget, err := p.Compose(ctx, targetSweep(t, cutAxis, ""))
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	viaVar, err := p.Compose(ctx, targetSweep(t, `{"name":"cut","values":[5,10,15]}`, `"params":{"cut":{"$var":"cut"}},`))
	if err != nil {
		t.Fatalf("$var: %v", err)
	}
	for i := range viaTarget.Responses {
		a, b := paramsFloat(t, viaTarget.Responses[i], "c"), paramsFloat(t, viaVar.Responses[i], "c")
		if a != b {
			t.Fatalf("slot %d: target %v, $var %v", i, a, b)
		}
	}
}

// TestComposeSweepTarget_RefusalsMatchPredict: every target refusal —
// a conflicting {"$var"} marker (on the targeted param or anywhere
// else naming the axis), a malformed or duplicate target, an undeclared
// params read a target does not cover — carries the same code, reason
// and message at predict and at run.
func TestComposeSweepTarget_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	cases := []struct {
		name   string
		req    *pulse.ComposedRequest
		code   perr.Code
		reason string
	}{
		{"marker on target", targetSweep(t, cutAxis, `"params":{"cut":{"$var":"cut"}},`), perr.PULSE_SWEEP_INVALID, "axis_target_conflict"},
		{"marker elsewhere", targetSweep(t, cutAxis, `"time_zone":{"$var":"cut"},`), perr.PULSE_SWEEP_INVALID, "axis_target_conflict"},
		{"declared marker of other axis", targetSweep(t, cutAxis+`,{"name":"k","values":[1]}`, `"params":{"cut":{"$var":"k"}},`), perr.PULSE_SWEEP_INVALID, "axis_target_conflict"},
		{"target not params", targetSweep(t, `{"name":"cut","values":[5],"target":"cut"}`, ""), perr.PULSE_SWEEP_INVALID, "target_invalid"},
		{"target duplicate", targetSweep(t, cutAxis+`,{"name":"c2","values":[1],"target":"params.cut"}`, ""), perr.PULSE_SWEEP_INVALID, "target_duplicate"},
		{"other param undeclared", targetSweep(t, `{"name":"cut","values":[5],"target":"params.lo"}`, ""), perr.PULSE_PARAMS_UNDECLARED, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, runErr := p.Compose(ctx, c.req)
			ce := requireCodedError(t, runErr, c.code)
			if c.reason != "" && ce.Details["reason"] != c.reason {
				t.Fatalf("reason = %v, want %s (%s)", ce.Details["reason"], c.reason, ce.Message)
			}
			env, err := p.PredictCompose(ctx, c.req)
			if err != nil {
				t.Fatalf("PredictCompose: %v", err)
			}
			found := false
			for _, e := range env.Errors {
				found = found || (e.Code == string(ce.Code) && e.Message == ce.Message && e.Details["reason"] == ce.Details["reason"])
			}
			if !found {
				t.Fatalf("predict errors %+v lack the run's %s %q", env.Errors, ce.Code, ce.Message)
			}
		})
	}
}

// TestComposeSweepTarget_FeatureProfileGate: a profile hiding
// capability:params refuses an axis `target` as an unknown field at
// predict and at run (details.path at the axis key), and its payload schema
// drops the key; enabling the feature runs the sweep.
func TestComposeSweepTarget_FeatureProfileGate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	openParams(t, pulse.Options{FS: fsys})
	open := func(features ...string) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{FS: fsys, FeatureProfile: &pulse.FeatureProfile{Features: features}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return p
	}
	base := []string{"capability:process", "capability:compose", "capability:compose_sweep", "FILTER_EXPRESSION", "AGG_COUNT"}
	hidden := open(base...)
	req := targetSweep(t, cutAxis, "")
	_, err := hidden.Compose(ctx, req)
	ce := requireCodedError(t, err, perr.PULSE_REQUEST_UNKNOWN_FIELD)
	if fmt.Sprint(ce.Details["unknown_keys"]) != "[target]" || ce.Details["path"] != "sweep.axes[0].target" {
		t.Fatalf("details = %v", ce.Details)
	}
	env, err := hidden.PredictCompose(ctx, req)
	if err != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
		t.Fatalf("predict: %v %+v, want the run's %q", err, env.Errors, ce.Message)
	}
	enabled := open(append(append([]string{}, base...), "capability:params")...)
	resp, err := enabled.Compose(ctx, req)
	if err != nil || len(resp.Responses) != 3 {
		t.Fatalf("enabled: %v", err)
	}
	for _, c := range []struct {
		p    *pulse.Pulse
		want bool
	}{{hidden, false}, {enabled, true}} {
		raw, err := c.p.PayloadSchema()
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Defs map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if _, has := doc.Defs["SweepAxis"].Properties["target"]; has != c.want {
			t.Fatalf("payload schema SweepAxis.target present = %v, want %v", has, c.want)
		}
	}
}

// TestComposeSweepTarget_ForEachRefused: an item for_each axis cannot
// target params — refused PULSE_FOR_EACH_INVALID target_unsupported,
// alike at predict and at run.
func TestComposeSweepTarget_ForEachRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	req := &types.Request{
		Cohort: paramsCohort(),
		Params: types.Params{"k": 1},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f_{{k}}", Expression: "x * params.k",
			ForEach: &types.ForEach{Axes: []types.SweepAxis{{Name: "k", Values: []any{1, 2}, Target: "params.k"}}}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "c"}},
	}
	_, err := p.Process(ctx, req)
	ce := requireCodedError(t, err, perr.PULSE_FOR_EACH_INVALID)
	if ce.Details["reason"] != "target_unsupported" {
		t.Fatalf("details = %v", ce.Details)
	}
	env, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	found := false
	for _, e := range env.Errors {
		found = found || (e.Code == string(ce.Code) && e.Message == ce.Message)
	}
	if !found {
		t.Fatalf("predict errors %+v lack the run's %q", env.Errors, ce.Message)
	}
}
