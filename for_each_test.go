package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// forEachReq is the adstock idiom over the ewma series: one plain
// WIN_LAG, then ONE WIN_EWMA item fanned out by zip over an integer
// label axis and a decay axis.
func forEachReq(t *testing.T) *types.Request {
	t.Helper()
	var r types.Request
	if err := json.Unmarshal([]byte(`{
		"cohort": {"filename": "s.pulse"},
		"windows": [
			{"type": "WIN_LAG", "field": "x", "label": "lag", "order_by": [{"field": "week"}]},
			{"for_each": {"axes": [{"name": "d", "values": [30, 50, 70]}, {"name": "decay", "values": [0.3, 0.5, 0.7]}], "mode": "zip"},
			 "type": "WIN_EWMA", "field": "x", "label": "x_ad{{d}}", "order_by": [{"field": "week"}],
			 "frame": {"mode": "rows"}, "params": {"decay": {"$var": "decay"}}}
		]
	}`), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

// handExpanded is forEachReq written out item by item.
func handExpanded() *types.Request {
	ewma := func(label, decay string) *types.Window {
		return &types.Window{Type: types.WIN_EWMA, Field: "x", Label: label,
			OrderBy: []types.OrderKey{{Field: "week"}}, Frame: &types.FrameSpec{Mode: "rows"},
			Params: json.RawMessage(`{"decay":` + decay + `}`)}
	}
	return &types.Request{
		Cohort: &types.Cohort{Filename: "s.pulse"},
		Windows: []*types.Window{
			{Type: types.WIN_LAG, Field: "x", Label: "lag", OrderBy: []types.OrderKey{{Field: "week"}}},
			ewma("x_ad30", "0.3"), ewma("x_ad50", "0.5"), ewma("x_ad70", "0.7"),
		},
	}
}

var forEachLabels = []string{"lag", "x_ad30", "x_ad50", "x_ad70"}

func codeOf(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want a coded error, got %v", err)
	}
	return ce
}

// TestForEach_ProcessMatchesHandExpanded: Process runs the expanded
// list — every column of the hand-expanded twin, bit for bit, in
// declared order — and the request it ran carries the expanded items.
func TestForEach_ProcessMatchesHandExpanded(t *testing.T) {
	p, _ := ewmaCohort(t)
	ctx := context.Background()
	req := forEachReq(t)
	got, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want, err := p.Process(ctx, handExpanded())
	if err != nil {
		t.Fatalf("Process (hand-expanded): %v", err)
	}
	if len(got.Data) != len(want.Data) || len(got.Data) == 0 {
		t.Fatalf("rows %d vs %d", len(got.Data), len(want.Data))
	}
	for _, col := range forEachLabels {
		var a, b []any
		for i := range got.Data {
			a = append(a, got.Data[i][col])
			b = append(b, want.Data[i][col])
		}
		if !sameBits(a, b) {
			t.Fatalf("column %s: %v, want %v", col, a, b)
		}
	}
	var labels []string
	for _, w := range req.Windows {
		labels = append(labels, w.Label)
		if w.ForEach != nil {
			t.Fatal("the run request still carries a for_each")
		}
	}
	if !reflect.DeepEqual(labels, forEachLabels) {
		t.Fatalf("run request windows = %v, want %v", labels, forEachLabels)
	}
	// The expanded request hashes as its hand-written twin does.
	if req.Hash() != handExpanded().Hash() {
		t.Fatal("the expanded request's canonical hash differs from the hand-expanded twin's")
	}
}

// TestForEach_MaterializeEveryExpandedColumn: every expanded item
// becomes a derived column, in declared order.
func TestForEach_MaterializeEveryExpandedColumn(t *testing.T) {
	p, _ := ewmaCohort(t)
	res, err := p.Materialize(context.Background(), forEachReq(t), "out.pulse", pulse.MaterializeOptions{})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if !reflect.DeepEqual(res.DerivedColumns, forEachLabels) {
		t.Fatalf("DerivedColumns = %v, want %v", res.DerivedColumns, forEachLabels)
	}
}

// TestForEach_PredictParity: predict judges the expanded request and
// reports each fan-out; Request stays as written. A malformed for_each
// is refused with the same code by predict and the runtime.
func TestForEach_PredictParity(t *testing.T) {
	p, _ := ewmaCohort(t)
	ctx := context.Background()
	req := forEachReq(t)
	res, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !res.Valid {
		t.Fatalf("predict invalid: %+v", res)
	}
	wantFan := []descriptor.ForEachSummary{{Path: "windows[1]", Mode: types.SweepModeZip, ExpandedCount: 3,
		Axes: []descriptor.SweepAxisSummary{{Name: "d", Count: 3}, {Name: "decay", Count: 3}}}}
	if !reflect.DeepEqual(res.ForEach, wantFan) {
		t.Fatalf("ForEach = %+v, want %+v", res.ForEach, wantFan)
	}
	if res.Request.Windows[1].ForEach == nil || len(res.Request.Windows) != 2 {
		t.Fatal("PredictResult.Request is not the request as written")
	}
	if req.Windows[1].ForEach == nil {
		t.Fatal("predict modified the caller's request")
	}

	bad := forEachReq(t)
	bad.Windows[1].Label = "x_ad{{dd}}"
	pres, err := p.Predict(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := p.Process(ctx, bad)
	ce := codeOf(t, rerr)
	if pres.Valid || ce.Code != errors.PULSE_FOR_EACH_INVALID || ce.Details["reason"] != "placeholder_unknown" {
		t.Fatalf("predict valid=%v, runtime %s/%v", pres.Valid, ce.Code, ce.Details["reason"])
	}
}

// TestForEach_ComposeAndChain: a Compose slot and a chain stage expand
// like a Process request, and predict judges the expanded slot.
func TestForEach_ComposeAndChain(t *testing.T) {
	p, _ := ewmaCohort(t)
	ctx := context.Background()
	out, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{forEachReq(t)}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, ok := out.Responses[0].Data[0]["x_ad70"]; !ok {
		t.Fatalf("compose slot missing an expanded column: %v", out.Responses[0].Data[0])
	}

	// A chain stage: the aggregation reads a column only the expansion
	// produces, so a stage that skipped it would be refused.
	stage := func() *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(`{"attributes": [{"for_each": {"axes": [{"name": "k", "values": [2, 3]}]},
			"type": "ATTR_FORMULA", "field": "week", "label": "x_by{{k}}", "expression": "week * {{k}}"}],
			"aggregations": [{"type": "AGG_SUM", "field": "x_by3", "label": "n"}]}`), &r); err != nil {
			t.Fatal(err)
		}
		return &r
	}
	chain := func() *pulse.ChainRequest {
		return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "s.pulse"}, Stages: []*types.ChainStage{{Request: stage()}}}
	}
	// The same request as a Compose slot: predict judges the expanded
	// slot (x_by3 exists only after the expansion).
	slot := stage()
	slot.Cohort = &types.Cohort{Filename: "s.pulse"}
	env, err := p.PredictCompose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{forEachReq(t), slot}})
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose: %v %+v", err, env.Errors)
	}
	// A hidden for_each in a slot is refused at the slot (details.request).
	hp, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: &pulse.FeatureProfile{
		Features: []string{"capability:compose", "capability:process", string(types.WIN_EWMA), string(types.WIN_LAG)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hp.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{handExpanded(), forEachReq(t)}})
	if ce := codeOf(t, err); ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || ce.Details["request"] != 1 || ce.Details["path"] != "windows[1]" {
		t.Fatalf("hidden compose slot = %s %v", ce.Code, ce.Details)
	}

	cres, err := p.ProcessChain(ctx, chain())
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	if got := cres.Final.Data[0]["n"]; got != float64(108) {
		t.Fatalf("chain stage sum = %v (%T), want 3 * (1+…+8) = 108", got, got)
	}
	cenv, err := p.PredictChain(ctx, chain())
	if err != nil || len(cenv.Errors) != 0 {
		t.Fatalf("PredictChain: %v %+v", err, cenv.Errors)
	}

	// A malformed stage is located by details.stage on both sides.
	badChain := func() *pulse.ChainRequest {
		c := chain()
		c.Stages[0].Request.Attributes[0].ForEach.Mode = "cross"
		return c
	}
	_, err = p.ProcessChain(ctx, badChain())
	if ce := codeOf(t, err); ce.Code != errors.PULSE_FOR_EACH_INVALID || ce.Details["stage"] != 0 {
		t.Fatalf("ProcessChain refusal = %s %v", ce.Code, ce.Details)
	}
	cenv, _ = p.PredictChain(ctx, badChain())
	if len(cenv.Errors) == 0 || cenv.Errors[0].Code != string(errors.PULSE_FOR_EACH_INVALID) || cenv.Errors[0].Details["stage"] != 0 {
		t.Fatalf("PredictChain refusal = %+v", cenv.Errors)
	}

	// A for_each inside a sweep body is refused, not expanded twice.
	sweepBody, _ := json.Marshal(forEachReq(t))
	_, err = p.Compose(ctx, &pulse.ComposedRequest{Sweep: &types.SweepSpec{
		Axes: []types.SweepAxis{{Name: "k", Values: []any{1}}}, Label: "k{{k}}", Request: sweepBody}})
	if ce := codeOf(t, err); ce.Code != errors.PULSE_FOR_EACH_INVALID || ce.Details["reason"] != "nested_in_sweep" {
		t.Fatalf("sweep body refusal = %s %v", ce.Code, ce.Details)
	}
}

// TestForEach_MaxDerivedColumns: the limit counts plain plus expanded
// items, refused by predict (a Certain finding) and the runtime with
// the identical figures — for a fanned-out and a plain request alike.
func TestForEach_MaxDerivedColumns(t *testing.T) {
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys, Limits: pulse.Limits{MaxDerivedColumns: 3}})
	if err != nil {
		t.Fatal(err)
	}
	copyCohort(t, fsys)
	ctx := context.Background()
	for name, build := range map[string]func() *types.Request{
		"for_each": func() *types.Request { return forEachReq(t) },
		"plain":    handExpanded,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := p.Process(ctx, build())
			ce := codeOf(t, err)
			if ce.Code != errors.PULSE_LIMIT_EXCEEDED || ce.Details["limit"] != "max_derived_columns" ||
				ce.Details["observed"] != int64(4) || ce.Details["configured"] != int64(3) {
				t.Fatalf("runtime = %s %v", ce.Code, ce.Details)
			}
			_, err = p.Materialize(ctx, build(), "o.pulse", pulse.MaterializeOptions{})
			if ce := codeOf(t, err); ce.Code != errors.PULSE_LIMIT_EXCEEDED {
				t.Fatalf("materialize = %s", ce.Code)
			}
			res, err := p.Predict(ctx, build())
			if err != nil {
				t.Fatal(err)
			}
			want := []descriptor.LimitFinding{{Limit: "max_derived_columns", Configured: 3, Estimated: 4, Grade: descriptor.LimitGradeCertain}}
			if res.Valid || !reflect.DeepEqual(res.LimitFindings, want) {
				t.Fatalf("predict valid=%v findings=%+v", res.Valid, res.LimitFindings)
			}
		})
	}
	ok := handExpanded()
	ok.Windows = ok.Windows[:3]
	if _, err := p.Process(ctx, ok); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
}

// copyCohort writes the ewma series into fsys.
func copyCohort(t *testing.T, fsys afero.Fs) {
	t.Helper()
	_, src := ewmaCohort(t)
	b, err := afero.ReadFile(src, "s.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "s.pulse", b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestForEach_HiddenCapability: an instance without capability:for_each
// refuses a set `for_each` as an unknown key at the item, at runtime and
// in predict; the payload schema drops the key.
func TestForEach_HiddenCapability(t *testing.T) {
	fsys := afero.NewMemMapFs()
	copyCohort(t, fsys)
	p, err := pulse.New(pulse.Options{FS: fsys, FeatureProfile: &pulse.FeatureProfile{
		Features: []string{"capability:process", string(types.WIN_EWMA), string(types.WIN_LAG)}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = p.Process(ctx, forEachReq(t))
	ce := codeOf(t, err)
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || ce.Details["path"] != "windows[1]" {
		t.Fatalf("runtime = %s %v", ce.Code, ce.Details)
	}
	res, err := p.Predict(ctx, forEachReq(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Valid {
		t.Fatal("predict accepted a hidden for_each")
	}
	if _, err := p.Process(ctx, handExpanded()); err != nil {
		t.Fatalf("the plain twin: %v", err)
	}
	schema, err := p.PayloadSchema()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(schema), `"for_each"`) || strings.Contains(string(schema), `"ForEach"`) {
		t.Fatal("the instance payload schema names for_each or its ForEach def")
	}
}

// TestForEach_AbsentIsByteIdentical: an item without for_each marshals
// without the key, so a for_each-free request's wire form and canonical
// hash are unchanged.
func TestForEach_AbsentIsByteIdentical(t *testing.T) {
	req := handExpanded()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "for_each") {
		t.Fatalf("for_each-free request marshals the key: %s", raw)
	}
	var round types.Request
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if round.Hash() != req.Hash() {
		t.Fatal("hash moved across a round trip")
	}
}
