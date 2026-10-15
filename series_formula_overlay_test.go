package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sfxSpec is a Request-host OVERLAY_FORMULA over a SERIES host with
// params (formula merged in).
func sfxSpec(t *testing.T, formula string, extra map[string]any) types.OverlaySpec {
	t.Helper()
	m := map[string]any{"formula": formula}
	for k, v := range extra {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return types.OverlaySpec{Name: "fx", Kind: types.OverlayKindFormula, Scope: types.OverlayScopeGroup, Params: b}
}

// sfxStats reads the one layer's per-row statistics (NaN for a row
// with no statistic).
func sfxStats(t *testing.T, resp *types.Response, layer int) []float64 {
	t.Helper()
	if resp == nil || len(resp.Overlays) <= layer || resp.Overlays[layer].Payload.Series == nil {
		t.Fatalf("want a series layer %d, got %+v", layer, resp)
	}
	entries := resp.Overlays[layer].Payload.Series.Entries
	if len(entries) != len(resp.Data) {
		t.Fatalf("%d entries for %d rows", len(entries), len(resp.Data))
	}
	out := make([]float64, len(entries))
	for i, e := range entries {
		out[i] = math.NaN()
		if e.Summary.Statistic != nil {
			out[i] = *e.Summary.Statistic
		}
	}
	return out
}

// sfxSame reports whether a and b agree, NaN matching NaN.
func sfxSame(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return a == b
}

// TestSeriesFormula_Namespace (U35 #432): OVERLAY_FORMULA on a
// single-grouper Process (SERIES) host evaluates once per row against
// value (the scored aggregation), total (the sum of every row's value),
// prior (the previous row's value, null on the first) and the opt-in
// baseline (the value at params.baseline_position). Before, the runtime
// had no SERIES FORMULA handler and both sides refused the host.
func TestSeriesFormula_Namespace(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	req := mgRequest(f, []*types.Group{mgRegion()}, psYSum())
	req.Overlays = []types.OverlaySpec{
		sfxSpec(t, "value", nil),
		sfxSpec(t, "total", nil),
		sfxSpec(t, "prior", nil),
		sfxSpec(t, "baseline", map[string]any{"baseline_position": 1}),
		sfxSpec(t, "(value - prior) / total * 100", nil),
	}
	if env := predictEnvelope(t, f.p, f.fs, f.cohort, req); len(env.Errors) != 0 {
		t.Fatalf("predict: %+v", env.Errors)
	}
	resp, err := f.p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) < 3 {
		t.Fatalf("%d rows; the fixture must carry a series", len(resp.Data))
	}
	ys := make([]float64, len(resp.Data))
	total := 0.0
	for i, row := range resp.Data {
		ys[i] = row["ys"].(float64)
		total += ys[i]
	}
	value, tot, prior, base, combo := sfxStats(t, resp, 0), sfxStats(t, resp, 1), sfxStats(t, resp, 2), sfxStats(t, resp, 3), sfxStats(t, resp, 4)
	for i := range ys {
		wantPrior := math.NaN()
		if i > 0 {
			wantPrior = ys[i-1]
		}
		if !sfxSame(value[i], ys[i]) || !sfxSame(tot[i], total) || !sfxSame(prior[i], wantPrior) || !sfxSame(base[i], ys[1]) {
			t.Errorf("row %d: value %v total %v prior %v baseline %v, want %v %v %v %v",
				i, value[i], tot[i], prior[i], base[i], ys[i], total, wantPrior, ys[1])
		}
		if want := (ys[i] - wantPrior) / total * 100; !sfxSame(combo[i], want) {
			t.Errorf("row %d: combined %v, want %v", i, combo[i], want)
		}
	}
	if s := resp.Overlays[4].Summary; s == nil || s.Count == nil || *s.Count != len(ys)-1 {
		t.Errorf("summary %+v, want count %d (the first row's null left out)", s, len(ys)-1)
	}
}

// TestSeriesFormula_MatchesComposeSeries: where the namespaces overlap
// (value, total, prior) the Request-host SERIES FORMULA equals the
// Compose SERIES FORMULA run over the same grouped request as its
// reference and target slots, row for row and bit for bit.
func TestSeriesFormula_MatchesComposeSeries(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	const formula = "(value - prior) / total + value * 2"
	req := mgRequest(f, []*types.Group{mgRegion()}, psYSum())
	req.Overlays = []types.OverlaySpec{sfxSpec(t, formula, nil)}
	resp, err := f.p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := sfxStats(t, resp, 0)

	slot := func(label string) *types.Request {
		r := mgRequest(f, []*types.Group{mgRegion()}, psYSum())
		r.Label = label
		return r
	}
	params := map[string]any{"formula": formula}
	cresp, err := f.p.Compose(context.Background(), &types.ComposedRequest{
		Requests: []*types.Request{slot("a"), slot("b")},
		Overlays: []types.ComposeOverlaySpec{{Name: "fx", Kind: types.OverlayKindFormula, Scope: types.OverlayScopeGroup,
			Reference: "a", Targets: []string{"b"}, Params: params}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cresp.Overlays) != 1 || cresp.Overlays[0].Payload.Series == nil {
		t.Fatalf("compose overlays %+v", cresp.Overlays)
	}
	entries := cresp.Overlays[0].Payload.Series.Entries
	if len(entries) != len(got) {
		t.Fatalf("compose %d entries, request host %d", len(entries), len(got))
	}
	for i, e := range entries {
		if e.Summary.Statistic == nil || !sfxSame(*e.Summary.Statistic, got[i]) {
			t.Errorf("row %d: compose %v, request host %v", i, e.Summary.Statistic, got[i])
		}
	}
}

// TestSeriesFormula_RefusalsMatchPredict: every refusal of a SERIES
// FORMULA is raised by predict and by the runtime with one code,
// message and details — on Process (serial and shard-parallel) and a
// Compose slot. Predict accepts what the runtime runs, an embedder
// ExprFunction included.
func TestSeriesFormula_RefusalsMatchPredict(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	ext := pulse.Extensions{ExprFunctions: []pulse.ExprFunction{{Name: "boost", Fn: func(x float64) float64 { return 2 * x }}}}
	withExt, err := pulse.New(pulse.Options{FS: f.fs, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	host := func(spec types.OverlaySpec) *types.Request {
		r := mgRequest(f, []*types.Group{mgRegion(), psScoreBy2()}, psYSum())
		r.Overlays = []types.OverlaySpec{{Name: "t", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup}, spec}
		return r
	}
	withRef := sfxSpec(t, "value", nil)
	withRef.Ref = types.OverlayRef{Prior: &types.OverlayPriorRef{}}
	cell := sfxSpec(t, "value", nil)
	cell.Scope = types.OverlayScopeCell
	missing := sfxSpec(t, "value", nil)
	missing.Params = json.RawMessage(`{}`)
	for _, c := range []struct {
		name string
		spec types.OverlaySpec
		want perr.Code
	}{
		{"unknown identifier", sfxSpec(t, "value + bogusvar", nil), perr.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"matrix identifier", sfxSpec(t, "cell", nil), perr.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"unknown function", sfxSpec(t, "bogusfn(value)", nil), perr.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"vector built-in", sfxSpec(t, "vsum(value)", nil), perr.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"baseline without its param", sfxSpec(t, "value / baseline", nil), perr.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"missing formula", missing, perr.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"parse error", sfxSpec(t, "value +", nil), perr.PULSE_OVERLAY_FORMULA_PARSE_ERROR},
		{"total() call", sfxSpec(t, "total(value)", nil), perr.PULSE_FORMULA_TOTAL_SITE},
		{"negative baseline_position", sfxSpec(t, "baseline", map[string]any{"baseline_position": -1}), perr.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"string baseline_position", sfxSpec(t, "baseline", map[string]any{"baseline_position": "first"}), perr.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"ref arm", withRef, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"cell scope", cell, perr.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"partition_by not a prefix", sfxSpec(t, "value - prior", map[string]any{"partition_by": []string{"GROUP_RANGE_score"}}), perr.PULSE_OVERLAY_PARTITION_INVALID},
		{"field unknown", sfxSpec(t, "value", map[string]any{"field": "nope"}), perr.PULSE_OVERLAY_FIELD_UNKNOWN},
	} {
		t.Run(c.name, func(t *testing.T) {
			pe := firstPredictError(t, predictEnvelope(t, f.p, f.fs, f.cohort, host(c.spec)), nil)
			if pe == nil || pe.Details["index"] != 1 {
				t.Fatalf("predict %+v, want a refusal at index 1", pe)
			}
			_, err := f.p.Process(ctx, host(c.spec))
			sameRefusal(t, "process", pe, err, c.want)
			shard := host(c.spec)
			shard.Cohort = &types.Cohort{Filename: f.archive}
			_, err = f.shard.Process(ctx, shard)
			sameRefusal(t, "process shards", pe, err, c.want)
			_, err = f.p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{host(c.spec)}})
			sameRefusal(t, "compose slot", pe, unlocated(t, err), c.want)
		})
	}

	// In reach: an embedder callee and a scalar-maths built-in run on
	// both sides; without the registration the callee is unknown on both.
	for _, formula := range []string{"boost(value)", "sqrt(value) + prior"} {
		if env := predictEnvelope(t, withExt, f.fs, f.cohort, host(sfxSpec(t, formula, nil))); len(env.Errors) != 0 {
			t.Errorf("%s: predict %+v", formula, env.Errors)
		}
		if _, err := withExt.Process(ctx, host(sfxSpec(t, formula, nil))); err != nil {
			t.Errorf("%s: process %v", formula, err)
		}
	}
	pe := firstPredictError(t, predictEnvelope(t, f.p, f.fs, f.cohort, host(sfxSpec(t, "boost(value)", nil))), nil)
	_, err = f.p.Process(ctx, host(sfxSpec(t, "boost(value)", nil)))
	sameRefusal(t, "no extension", pe, err, perr.PULSE_OVERLAY_FORMULA_INVALID_IDENT)
}

// TestSeriesFormula_BaselineOutOfRange: a baseline_position past the
// partition is the runtime's PULSE_OVERLAY_REF_UNKNOWN (the series
// length is a property of the data); a position inside every partition
// runs.
func TestSeriesFormula_BaselineOutOfRange(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	req := mgRequest(f, []*types.Group{mgRegion()}, psYSum())
	req.Overlays = []types.OverlaySpec{sfxSpec(t, "value / baseline", map[string]any{"baseline_position": 999})}
	_, err := f.p.Process(context.Background(), req)
	ce := requireCodedError(t, err, perr.PULSE_OVERLAY_REF_UNKNOWN)
	if ce.Details["baseline_position"] != 999 {
		t.Errorf("details %#v", ce.Details)
	}
}

// TestRollingOverlays_Window1 (U35 #431): OVERLAY_INDEX_VS_ROLLING_MEAN
// at window 1 indexes each row against the one before it — exactly
// OVERLAY_INDEX_VS_PRIOR — with no null past the first row (the
// carrier's eviction once divided by zero and nulled every row from the
// third on). OVERLAY_ZSCORE_VS_ROLLING at window 1 (a one-point sample
// SD: null at every row) is refused by predict and the runtime alike.
func TestRollingOverlays_Window1(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	ctx := context.Background()
	req := mgRequest(f, []*types.Group{mgRegion()}, psYSum())
	req.Overlays = []types.OverlaySpec{
		{Name: "irm", Kind: types.OverlayKindIndexVsRollingMean, Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: json.RawMessage(`{"window": 1}`)},
		{Name: "ip", Kind: types.OverlayKindIndexVsPrior, Scope: types.OverlayScopeGroup},
	}
	resp, err := f.p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	rolling, prior := sfxStats(t, resp, 0), sfxStats(t, resp, 1)
	if len(rolling) < 3 {
		t.Fatalf("%d rows; window 1 needs a third row to show the eviction", len(rolling))
	}
	for i := range rolling {
		if i > 0 && math.IsNaN(rolling[i]) {
			t.Errorf("row %d: window-1 rolling index is null", i)
		}
		if !sfxSame(rolling[i], prior[i]) {
			t.Errorf("row %d: window-1 rolling index %v, INDEX_VS_PRIOR %v", i, rolling[i], prior[i])
		}
	}

	z := mgRequest(f, []*types.Group{mgRegion()}, psYSum())
	z.Overlays = []types.OverlaySpec{{Name: "t", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup},
		{Name: "zr", Kind: types.OverlayKindZScoreVsRolling, Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: json.RawMessage(`{"window": 1}`)}}
	pe := firstPredictError(t, predictEnvelope(t, f.p, f.fs, f.cohort, z), nil)
	if pe == nil || pe.Details["index"] != 1 || pe.Details["min"] != 2 {
		t.Fatalf("predict %+v, want the window floor at index 1", pe)
	}
	_, err = f.p.Process(ctx, z)
	ce := requireCodedError(t, err, perr.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE)
	if ce.Message != pe.Message || pe.Code != string(ce.Code) {
		t.Errorf("runtime %s %q, predict %s %q", ce.Code, ce.Message, pe.Code, pe.Message)
	}
}
