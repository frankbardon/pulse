package integration_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// for_each_items_test.go pins the aggregation and matrix item fan-out
// (library-gaps E7-S1, G4): both kinds expand after the windows, run
// exactly as their hand-expanded twins, count toward MaxExpandedItems
// (refused identically by predict and the runtime), and stay hidden
// with capability:for_each.

// channelCohort is a weekly two-channel spend series.
func channelCohort(t *testing.T, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	opts.FS = fsys
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16, Description: "Week number within the season"},
		{Name: "tv", Type: encoding.FieldTypeF64, Description: "Weekly television spend in thousands"},
		{Name: "search", Type: encoding.FieldTypeF64, Description: "Weekly paid-search spend in thousands"},
	}}
	b, err := p.NewCohortBuilder(context.Background(), "ch.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range [][2]float64{{13.7, 2.9}, {4.1, 101.3}, {7.77, 55.1}, {0.3, 19.9}, {42.5, 8.25}, {3.3, 0.7}} {
		if err := b.Append(pulse.CohortRow{uint64(i + 1), v[0], v[1]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// lagAggReq fans a WIN_LAG out per channel and an aggregation over the
// `{{ch}}_lag` column each adds, beside plain items.
func lagAggReq(t *testing.T) *types.Request {
	t.Helper()
	var r types.Request
	if err := json.Unmarshal([]byte(`{
		"cohort": {"filename": "ch.pulse"},
		"windows": [{"for_each": {"axes": [{"name": "ch", "values": ["tv", "search"]}]},
			"type": "WIN_LAG", "field": "{{ch}}", "label": "{{ch}}_lag", "order_by": [{"field": "week"}]}],
		"aggregations": [
			{"type": "AGG_COUNT", "field": "week", "label": "n"},
			{"for_each": {"axes": [{"name": "ch", "values": ["tv", "search"]}, {"name": "op", "values": ["SUM", "AVERAGE"]}]},
			 "type": "AGG_{{op}}", "field": "{{ch}}_lag", "label": "{{ch}}_lag_{{op}}"}]
	}`), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

// lagAggTwin is lagAggReq written out by hand.
func lagAggTwin() *types.Request {
	lag := func(ch string) *types.Window {
		return &types.Window{Type: types.WIN_LAG, Field: ch, Label: ch + "_lag", OrderBy: []types.OrderKey{{Field: "week"}}}
	}
	agg := func(op types.AggregationType, ch, suffix string) *types.Aggregation {
		return &types.Aggregation{Type: op, Field: ch + "_lag", Label: ch + "_lag_" + suffix}
	}
	return &types.Request{
		Cohort:  &types.Cohort{Filename: "ch.pulse"},
		Windows: []*types.Window{lag("tv"), lag("search")},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "week", Label: "n"},
			agg(types.AGG_SUM, "tv", "SUM"), agg(types.AGG_AVERAGE, "tv", "AVERAGE"),
			agg(types.AGG_SUM, "search", "SUM"), agg(types.AGG_AVERAGE, "search", "AVERAGE"),
		},
	}
}

// TestForEachItems_AggregationOverLagWindows: an aggregation fanned out
// over the `{{ch}}_lag` windows a window for_each adds runs as its
// hand-expanded twin (bit for bit), the run request carries the
// expanded items, and predict judges it valid and reports both
// fan-outs in expansion order.
func TestForEachItems_AggregationOverLagWindows(t *testing.T) {
	t.Parallel()

	p := channelCohort(t, pulse.Options{})
	ctx := context.Background()
	req := lagAggReq(t)
	got, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want, err := p.Process(ctx, lagAggTwin())
	if err != nil {
		t.Fatalf("Process (twin): %v", err)
	}
	if len(got.Data) != 1 || len(want.Data) != 1 {
		t.Fatalf("rows %d vs %d", len(got.Data), len(want.Data))
	}
	for _, label := range []string{"n", "tv_lag_SUM", "tv_lag_AVERAGE", "search_lag_SUM", "search_lag_AVERAGE"} {
		if !sameBits([]any{got.Data[0][label]}, []any{want.Data[0][label]}) || got.Data[0][label] == nil {
			t.Fatalf("%s = %v, want %v", label, got.Data[0][label], want.Data[0][label])
		}
	}
	if len(req.Aggregations) != 5 || req.Aggregations[1].ForEach != nil || req.Aggregations[4].Label != "search_lag_AVERAGE" {
		t.Fatalf("the run request's aggregations are not the expanded list: %+v", req.Aggregations)
	}
	if req.Hash() != lagAggTwin().Hash() {
		t.Fatal("the expanded request's canonical hash differs from the twin's")
	}

	res, err := p.Predict(ctx, lagAggReq(t))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("predict invalid: %+v", res)
	}
	var paths []string
	for _, f := range res.ForEach {
		paths = append(paths, f.Path)
	}
	if !reflect.DeepEqual(paths, []string{"windows[0]", "aggregations[1]"}) || res.ForEach[1].ExpandedCount != 4 {
		t.Fatalf("predict for_each = %+v", res.ForEach)
	}
}

// TestForEachItems_RefusalParity: predict and the runtime refuse a
// malformed aggregation or matrix fan-out, a misplaced one (a crosstab
// cell) and an axis spliced into an AGG_CONSTANT expression with the
// same code, reason and field.
func TestForEachItems_RefusalParity(t *testing.T) {
	t.Parallel()

	p := channelCohort(t, pulse.Options{})
	ctx := context.Background()
	const fe = `"for_each": {"axes": [{"name": "k", "values": [2, 3]}]}`
	for name, c := range map[string]struct{ body, code, reason, field string }{
		"aggregation placeholder": {`"aggregations": [{` + fe + `, "type": "AGG_SUM", "field": "tv", "label": "s{{kk}}"}]`,
			string(errors.PULSE_FOR_EACH_INVALID), "placeholder_unknown", "aggregations[0].label"},
		"matrix unreferenced": {`"matrices": [{` + fe + `, "type": "MAT_COVARIANCE", "name": "m", "fields": ["tv", "search"]}]`,
			string(errors.PULSE_FOR_EACH_INVALID), "axis_unreferenced", "matrices[0].for_each.axes[0].name"},
		"crosstab cell": {`"crosstab": {"rows": [{"type": "GROUP_CATEGORY", "field": "week"}], "cell": {` + fe + `, "type": "AGG_SUM", "field": "tv", "label": "s{{k}}"}}`,
			string(errors.PULSE_FOR_EACH_INVALID), "position_unsupported", "crosstab.cell.for_each"},
		"constant expr splice": {`"aggregations": [{` + fe + `, "type": "AGG_CONSTANT", "label": "c{{k}}", "params": {"expr": "{{k}} * 2"}}]`,
			string(errors.PULSE_FOR_EACH_EXPR_SPLICE), "", "aggregations[0].params.expr"},
	} {
		t.Run(name, func(t *testing.T) {
			decode := func() *types.Request {
				var r types.Request
				if err := json.Unmarshal([]byte(`{"cohort": {"filename": "ch.pulse"}, `+c.body+`}`), &r); err != nil {
					t.Fatal(err)
				}
				return &r
			}
			_, rerr := p.Process(ctx, decode())
			ce := codeOf(t, rerr)
			if string(ce.Code) != c.code || (c.reason != "" && ce.Details["reason"] != c.reason) || ce.Details["field"] != c.field {
				t.Fatalf("runtime = %s %v", ce.Code, ce.Details)
			}
			env, err := p.PredictEnvelope(ctx, decode())
			if err != nil {
				t.Fatal(err)
			}
			res := env.Data.(*descriptor.PredictResult)
			if res.Valid || len(env.Errors) == 0 || env.Errors[0].Code != c.code ||
				!reflect.DeepEqual(env.Errors[0].Details, ce.Details) || env.Errors[0].Message != ce.Message {
				t.Fatalf("predict valid=%v errors=%+v, runtime %s %v", res.Valid, env.Errors, ce.Message, ce.Details)
			}
		})
	}
}

// TestForEachItems_SEMInvarianceLadder: the measurement-invariance
// ladder written once — ONE multi-group MAT_SEM spec zipped over the
// step name and the parameters held equal — runs as its three
// hand-written specs, bit for bit, each result named by its step.
func TestForEachItems_SEMInvarianceLadder(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ladder := semGroupRequest(t, "brand_ms", map[string]any{"group_equal": map[string]any{"$var": "eq"}})
	ladder.Matrices[0].Name = "{{step}}"
	ladder.Matrices[0].ForEach = &types.ForEach{Mode: types.SweepModeZip, Axes: []types.SweepAxis{
		{Name: "step", Values: []any{"configural", "metric", "scalar"}},
		{Name: "eq", Values: []any{"", "loadings", "loadings,intercepts"}},
	}}
	twin := semGroupRequest(t, "brand_ms", nil)
	spec := twin.Matrices[0]
	twin.Matrices = nil
	for _, s := range []struct{ name, eq string }{{"configural", ""}, {"metric", "loadings"}, {"scalar", "loadings,intercepts"}} {
		var params map[string]any
		if err := json.Unmarshal(spec.Params, &params); err != nil {
			t.Fatal(err)
		}
		params["group_equal"] = s.eq
		m := spec
		m.Name, m.Params = s.name, json.RawMessage(mustMarshal(t, params))
		twin.Matrices = append(twin.Matrices, m)
	}

	ctx := context.Background()
	res, err := p.Predict(ctx, ladder)
	if err != nil {
		t.Fatal(err)
	}
	wantFan := []descriptor.ForEachSummary{{Path: "matrices[0]", Mode: types.SweepModeZip, ExpandedCount: 3,
		Axes: []descriptor.SweepAxisSummary{{Name: "step", Count: 3}, {Name: "eq", Count: 3}}}}
	if !res.Valid || !reflect.DeepEqual(res.ForEach, wantFan) {
		t.Fatalf("predict valid=%v for_each=%+v", res.Valid, res.ForEach)
	}
	got, err := p.Process(ctx, ladder)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want, err := p.Process(ctx, twin)
	if err != nil {
		t.Fatalf("Process (twin): %v", err)
	}
	if len(got.Matrices) != 3 || len(want.Matrices) != 3 {
		t.Fatalf("%d / %d matrices", len(got.Matrices), len(want.Matrices))
	}
	var dofs []float64
	for i, g := range got.Matrices {
		w := want.Matrices[i]
		if g.Name != w.Name || g.Error != nil || w.Error != nil {
			t.Fatalf("matrix %d: %s / %s (errors %+v / %+v)", i, g.Name, w.Name, g.Error, w.Error)
		}
		sameFigures(t, g.Name+" estimates", g.Auxiliary["estimates"].Values, w.Auxiliary["estimates"].Values)
		if mustMarshal(t, g.Scalars) != mustMarshal(t, w.Scalars) {
			t.Fatalf("%s scalars differ:\n%s\n%s", g.Name, mustMarshal(t, g.Scalars), mustMarshal(t, w.Scalars))
		}
		dofs = append(dofs, g.Scalars["dof"])
	}
	if !(dofs[0] < dofs[1] && dofs[1] < dofs[2]) {
		t.Fatalf("dof does not rise up the ladder: %v", dofs)
	}
}

// TestForEachItems_MaxExpandedItems: the aggregations and matrices the
// fan-outs add count toward MaxExpandedItems — plain items never do,
// and the windows count toward MaxDerivedColumns only — refused by
// predict (a Certain finding) and the runtime with identical figures;
// Unlimited lifts it.
func TestForEachItems_MaxExpandedItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	req := func() *types.Request {
		r := lagAggReq(t)
		r.Matrices = []types.MatrixSpec{
			{Type: types.MAT_COVARIANCE, Name: "plain", Fields: []string{"tv", "search"}},
			{Type: types.MAT_COVARIANCE, Name: "cov{{d}}", Fields: []string{"tv", "search"}, Params: json.RawMessage(`{"ddof": {"$var": "d"}}`),
				ForEach: &types.ForEach{Axes: []types.SweepAxis{{Name: "d", Values: []any{0, 1}}}}},
		}
		return r
	}
	// 4 aggregations + 2 matrices added; the plain AGG_COUNT and matrix
	// do not count, nor do the 2 windows.
	p := channelCohort(t, pulse.Options{Limits: pulse.Limits{MaxExpandedItems: 5, MaxDerivedColumns: 2}})
	_, err := p.Process(ctx, req())
	ce := codeOf(t, err)
	if ce.Code != errors.PULSE_LIMIT_EXCEEDED || ce.Details["limit"] != "max_expanded_items" ||
		ce.Details["observed"] != int64(6) || ce.Details["configured"] != int64(5) {
		t.Fatalf("runtime = %s %v", ce.Code, ce.Details)
	}
	res, err := p.Predict(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	want := []descriptor.LimitFinding{{Limit: "max_expanded_items", Configured: 5, Estimated: 6, Grade: descriptor.LimitGradeCertain}}
	if res.Valid || !reflect.DeepEqual(res.LimitFindings, want) {
		t.Fatalf("predict valid=%v findings=%+v", res.Valid, res.LimitFindings)
	}
	for _, l := range []pulse.Limits{{MaxExpandedItems: 6, MaxDerivedColumns: 2}, {MaxExpandedItems: pulse.Unlimited}} {
		if _, err := channelCohort(t, pulse.Options{Limits: l}).Process(ctx, req()); err != nil {
			t.Fatalf("limits %+v: %v", l, err)
		}
	}
	// A for_each-free request never counts, however many items it has.
	plain := lagAggTwin()
	if _, err := channelCohort(t, pulse.Options{Limits: pulse.Limits{MaxExpandedItems: 1}}).Process(ctx, plain); err != nil {
		t.Fatalf("a for_each-free request tripped the limit: %v", err)
	}
}

// TestForEachItems_HiddenCapability: an instance hiding
// capability:for_each refuses an aggregation or matrix `for_each` as an
// unknown key at the item, at runtime and in predict.
func TestForEachItems_HiddenCapability(t *testing.T) {
	t.Parallel()

	p := channelCohort(t, pulse.Options{FeatureProfile: &pulse.FeatureProfile{Features: []string{
		"capability:process", "capability:matrices", string(types.AGG_SUM), string(types.MAT_COVARIANCE)}}})
	ctx := context.Background()
	fe := &types.ForEach{Axes: []types.SweepAxis{{Name: "f", Values: []any{"tv", "search"}}}}
	for path, r := range map[string]*types.Request{
		"aggregations[0].for_each": {Cohort: &types.Cohort{Filename: "ch.pulse"},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "{{f}}", Label: "s_{{f}}", ForEach: fe}}},
		"matrices[0].for_each": {Cohort: &types.Cohort{Filename: "ch.pulse"},
			Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "m_{{f}}", Fields: []string{"{{f}}", "week"}, ForEach: fe}}},
	} {
		t.Run(path, func(t *testing.T) {
			_, err := p.Process(ctx, r)
			if ce := codeOf(t, err); ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || ce.Details["path"] != path {
				t.Fatalf("runtime = %s %v", ce.Code, ce.Details)
			}
			env, err := p.PredictEnvelope(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if env.Data.(*descriptor.PredictResult).Valid || len(env.Errors) == 0 ||
				env.Errors[0].Code != string(errors.PULSE_REQUEST_UNKNOWN_FIELD) || env.Errors[0].Details["path"] != path {
				t.Fatalf("predict errors = %+v", env.Errors)
			}
		})
	}
}

// sweepForEachCases is one compose sweep per request item kind
// (compose-sweep-2 E2-S2): the body fans one item out over the item axis
// `f` while the sweep axis `a` labels the slots (and, for an
// aggregation, the fanned-out item too), each beside its hand-expanded
// twin — the same slots written as explicit requests.
var sweepForEachCases = map[string]struct{ body, twin string }{
	"features": {
		`"features": [{"for_each": {"axes": [{"name": "f", "values": ["tv", "search"]}]}, "type": "FEAT_LOG", "field": "{{f}}", "label": "{{f}}_log"}],
		 "aggregations": [{"type": "AGG_SUM", "field": "tv_log", "label": "t"}, {"type": "AGG_SUM", "field": "search_log", "label": "s"}]`,
		`"features": [{"type": "FEAT_LOG", "field": "tv", "label": "tv_log"}, {"type": "FEAT_LOG", "field": "search", "label": "search_log"}],
		 "aggregations": [{"type": "AGG_SUM", "field": "tv_log", "label": "t"}, {"type": "AGG_SUM", "field": "search_log", "label": "s"}]`,
	},
	"attributes": {
		`"attributes": [{"for_each": {"axes": [{"name": "f", "values": ["tv", "search"]}]}, "type": "ATTR_ZSCORE", "field": "{{f}}", "label": "{{f}}_z"}],
		 "aggregations": [{"type": "AGG_MAX", "field": "tv_z", "label": "t"}, {"type": "AGG_MAX", "field": "search_z", "label": "s"}]`,
		`"attributes": [{"type": "ATTR_ZSCORE", "field": "tv", "label": "tv_z"}, {"type": "ATTR_ZSCORE", "field": "search", "label": "search_z"}],
		 "aggregations": [{"type": "AGG_MAX", "field": "tv_z", "label": "t"}, {"type": "AGG_MAX", "field": "search_z", "label": "s"}]`,
	},
	"windows": {
		`"windows": [{"for_each": {"axes": [{"name": "f", "values": ["tv", "search"]}]}, "type": "WIN_LAG", "field": "{{f}}", "label": "{{f}}_lag", "order_by": [{"field": "week"}]}],
		 "aggregations": [{"type": "AGG_SUM", "field": "tv_lag", "label": "t"}, {"type": "AGG_SUM", "field": "search_lag", "label": "s"}]`,
		`"windows": [{"type": "WIN_LAG", "field": "tv", "label": "tv_lag", "order_by": [{"field": "week"}]}, {"type": "WIN_LAG", "field": "search", "label": "search_lag", "order_by": [{"field": "week"}]}],
		 "aggregations": [{"type": "AGG_SUM", "field": "tv_lag", "label": "t"}, {"type": "AGG_SUM", "field": "search_lag", "label": "s"}]`,
	},
	"aggregations": {
		`"aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}, {"for_each": {"axes": [{"name": "f", "values": ["tv", "search"]}]}, "type": "AGG_SUM", "field": "{{f}}", "label": "{{f}}_{{a}}"}]`,
		`"aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}, {"type": "AGG_SUM", "field": "tv", "label": "tv_$A"}, {"type": "AGG_SUM", "field": "search", "label": "search_$A"}]`,
	},
	"matrices": {
		`"aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}],
		 "matrices": [{"for_each": {"axes": [{"name": "f", "values": ["tv", "search"]}]}, "type": "MAT_COVARIANCE", "name": "m_{{f}}", "fields": ["{{f}}", "week"]}]`,
		`"aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}],
		 "matrices": [{"type": "MAT_COVARIANCE", "name": "m_tv", "fields": ["tv", "week"]}, {"type": "MAT_COVARIANCE", "name": "m_search", "fields": ["search", "week"]}]`,
	},
}

// decodeComposedRaw strictly-enough decodes one Compose request.
func decodeComposedRaw(t *testing.T, raw string) *pulse.ComposedRequest {
	t.Helper()
	var c pulse.ComposedRequest
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// predictComposeSlots reads a valid PredictCompose's slot count and the
// sweep's expanded count off the wire form.
func predictComposeSlots(t *testing.T, p *pulse.Pulse, c *pulse.ComposedRequest) (slots, expanded int) {
	t.Helper()
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose: %v %+v", err, env.Errors)
	}
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Valid bool              `json:"valid"`
		Slots []json.RawMessage `json:"slots"`
		Sweep struct {
			ExpandedCount int `json:"expanded_count"`
		} `json:"sweep"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || !out.Valid {
		t.Fatalf("predict data %s (%v)", raw, err)
	}
	return len(out.Slots), out.Sweep.ExpandedCount
}

// TestForEachItems_InSweepBody: a compose sweep body carrying a
// for_each item runs for every request item kind — the sweep is the
// OUTER loop, one slot per combination (predict's slot count equals the
// sweep count), and each slot fans its items out in place, so every
// slot answers exactly as its hand-expanded twin.
func TestForEachItems_InSweepBody(t *testing.T) {
	t.Parallel()

	p := channelCohort(t, pulse.Options{})
	ctx := context.Background()
	for kind, tc := range sweepForEachCases {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			sweep := func() *pulse.ComposedRequest {
				return decodeComposedRaw(t, `{"sweep": {"axes": [{"name": "a", "values": [1, 2]}], "label": "s{{a}}",
					"request": {"cohort": {"filename": "ch.pulse"}, `+tc.body+`}}}`)
			}
			twinSlot := func(a string) string {
				return `{"label": "s` + a + `", "cohort": {"filename": "ch.pulse"}, ` + strings.ReplaceAll(tc.twin, "$A", a) + `}`
			}
			got, err := p.Compose(ctx, sweep())
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			want, err := p.Compose(ctx, decodeComposedRaw(t, `{"requests": [`+twinSlot("1")+`, `+twinSlot("2")+`]}`))
			if err != nil {
				t.Fatalf("Compose (twin): %v", err)
			}
			if len(got.Responses) != 2 || len(want.Responses) != 2 {
				t.Fatalf("slots = %d, twin %d; want 2", len(got.Responses), len(want.Responses))
			}
			for i := range got.Responses {
				if len(got.Responses[i].Data) == 0 && len(got.Responses[i].Matrices) == 0 {
					t.Fatalf("slot %d answered nothing", i)
				}
				g, _ := json.Marshal([]any{got.Responses[i].Label, got.Responses[i].Data, got.Responses[i].Matrices})
				w, _ := json.Marshal([]any{want.Responses[i].Label, want.Responses[i].Data, want.Responses[i].Matrices})
				if string(g) != string(w) {
					t.Fatalf("slot %d = %s\nwant %s", i, g, w)
				}
			}
			if slots, expanded := predictComposeSlots(t, p, sweep()); slots != 2 || expanded != 2 {
				t.Fatalf("predict slots = %d, expanded_count = %d; want 2 (the item fan-out adds no slot)", slots, expanded)
			}
		})
	}
}

// TestForEachItems_InSweepAxisShadowed: an item axis named like a sweep
// axis is refused axis_shadowed by predict and the runtime alike — the
// same code, field and details — for every request item kind.
func TestForEachItems_InSweepAxisShadowed(t *testing.T) {
	t.Parallel()

	p := channelCohort(t, pulse.Options{})
	ctx := context.Background()
	for kind, tc := range sweepForEachCases {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			body := strings.ReplaceAll(strings.Replace(tc.body, `"name": "f"`, `"name": "a"`, 1), "{{f}}", "{{a}}")
			raw := `{"sweep": {"axes": [{"name": "a", "values": [1, 2]}], "label": "s{{a}}", "request": {"cohort": {"filename": "ch.pulse"}, ` + body + `}}}`
			_, err := p.Compose(ctx, decodeComposedRaw(t, raw))
			ce := codeOf(t, err)
			if ce.Code != errors.PULSE_FOR_EACH_INVALID || ce.Details["reason"] != "axis_shadowed" ||
				!strings.HasPrefix(ce.Details["field"].(string), "sweep.request."+kind+"[") || ce.Details["axis"] != "a" {
				t.Fatalf("runtime = %s %v", ce.Code, ce.Details)
			}
			env, err := p.PredictCompose(ctx, decodeComposedRaw(t, raw))
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_FOR_EACH_INVALID) ||
				!reflect.DeepEqual(env.Errors[0].Details, ce.Details) || env.Errors[0].Message != ce.Message {
				t.Fatalf("predict errors = %+v, runtime %v", env.Errors, ce.Details)
			}
		})
	}
}

// TestForEachItems_OptimizeBesideSweep: a Compose `optimize` block's
// for_each fans out beside a sweep — the block sits at the Compose level,
// outside the sweep body, so its axes never meet the sweep's: even a
// shared axis name is not shadowing. Predict and the runtime agree on
// the slots and the blocks.
func TestForEachItems_OptimizeBesideSweep(t *testing.T) {
	t.Parallel()

	p := channelCohort(t, pulse.Options{})
	ctx := context.Background()
	raw := `{"sweep": {"axes": [{"name": "a", "values": [1, 2]}], "label": "s{{a}}",
		"request": {"cohort": {"filename": "ch.pulse"}, "aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}]}},
		"optimize": [{"name": "o_{{a}}", "direction": "maximize", "variables": [{"name": "x", "lower": 0, "upper": 10}],
			"objective": "0 - (x - 3) * (x - 3)", "for_each": {"axes": [{"name": "a", "values": ["p", "q"]}]}}]}`
	got, err := p.Compose(ctx, decodeComposedRaw(t, raw))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(got.Responses) != 2 || len(got.Optimizations) != 2 ||
		got.Optimizations[0].Name != "o_p" || got.Optimizations[1].Name != "o_q" {
		t.Fatalf("slots %d, optimizations %+v", len(got.Responses), got.Optimizations)
	}
	if slots, expanded := predictComposeSlots(t, p, decodeComposedRaw(t, raw)); slots != 2 || expanded != 2 {
		t.Fatalf("predict slots = %d, expanded_count = %d", slots, expanded)
	}
}

// TestForEachItems_Explain: Explain describes the aggregations and
// matrices a for_each adds as they run — one fan-out step per item,
// then one step per expanded item naming its result, each slotted at
// the item as written.
func TestForEachItems_Explain(t *testing.T) {
	t.Parallel()

	p := channelCohort(t, pulse.Options{})
	req := lagAggReq(t)
	req.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov{{d}}", Fields: []string{"tv", "search"},
		Params:  json.RawMessage(`{"ddof": {"$var": "d"}}`),
		ForEach: &types.ForEach{Axes: []types.SweepAxis{{Name: "d", Values: []any{0, 1}}}}}}
	res, err := p.Explain(context.Background(), descriptor.ExplainRequest{Request: req})
	if err != nil {
		t.Fatal(err)
	}
	steps := map[string][]string{}
	for _, st := range res.Steps {
		steps[st.Slot] = append(steps[st.Slot], st.Text)
	}
	if f := steps["aggregations[1].for_each"]; len(f) != 1 || !strings.Contains(f[0], "expands it into 4 aggregations") {
		t.Fatalf("aggregation fan-out steps = %q", f)
	}
	if f := steps["matrices[0].for_each"]; len(f) != 1 || !strings.Contains(f[0], "expands it into 2 matrix results") {
		t.Fatalf("matrix fan-out steps = %q", f)
	}
	aggs := steps["aggregations[1]"]
	if len(aggs) != 4 || !strings.Contains(aggs[3], "adding the aggregation `search_lag_AVERAGE`") {
		t.Fatalf("expanded aggregation steps = %q", aggs)
	}
	if len(steps["aggregations[0]"]) != 1 {
		t.Fatalf("the plain aggregation steps = %q", steps["aggregations[0]"])
	}
	if m := steps["matrices[0]"]; len(m) != 2 || !strings.Contains(m[1], "adding the matrix result `cov1`") {
		t.Fatalf("expanded matrix steps = %q", m)
	}
}
