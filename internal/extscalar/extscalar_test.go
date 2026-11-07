package extscalar_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/extscalar"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/types"
)

// flagged flags "scale" on every operator named X_EXT in any category.
func flagged(cat paramread.Category, op string) []string {
	if strings.HasSuffix(op, "_EXT") {
		return []string{"scale"}
	}
	return nil
}

// staticEnv is a static judgement over params k = 3, label = "a".
func staticEnv() (scalarslot.Env, exprenv.Bound) {
	p, err := exprenv.NormalizeParams(types.Params{"k": json.Number("3"), "label": "a"})
	if err != nil {
		panic(err)
	}
	env := scalarslot.Env{Params: p}
	return env, env.Declared()
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// scaleOf is params' "scale" member.
func scaleOf(t *testing.T, params json.RawMessage) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(params, &m); err != nil {
		t.Fatalf("params %s: %v", params, err)
	}
	return string(m["scale"])
}

// TestResolve_EverySlot: every slot the request params check walks is
// resolved, an extension's flagged string written as its number, while
// a built-in, an unflagged key and a number are left alone and the
// caller's request is never mutated.
func TestResolve_EverySlot(t *testing.T) {
	t.Parallel()

	e := `{"scale":"params.k * 2","tag":"x"}`
	req := &types.Request{
		Filterers:    []*types.Filterer{{Type: "FILTER_EXT", Params: raw(e)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Params: raw(`{"scale":"params.k"}`)}, {Type: "AGG_EXT", Params: raw(e)}},
		Attributes:   []*types.Attribute{{Type: "ATTR_EXT", Params: raw(e)}},
		Groups:       []*types.Group{{Type: "GROUP_EXT", Params: raw(e)}},
		Windows:      []*types.Window{{Type: "WIN_EXT", Params: raw(e)}},
		Features:     []*types.Feature{{Type: "FEAT_EXT", Params: raw(e)}},
		Tests:        []*types.Test{{Type: "TEST_EXT", Params: raw(e)}},
		PostTests:    []*types.Test{{Type: "TEST_POST_EXT", Params: raw(e)}},
		Matrices: []types.MatrixSpec{{Type: "MAT_EXT", Params: raw(e)},
			{Type: types.MAT_DISTANCE, Params: raw(`{"between":{"type":"GROUP_EXT","field":"g","params":{"scale":"params.k + 1"}}}`)}},
		Regressions: []*types.RegressionSpec{{Type: "REG_EXT", Params: raw(e)}},
		Crosstab: &types.CrosstabSpec{
			Rows:               []*types.Group{{Type: "GROUP_EXT", Params: raw(e)}},
			Columns:            []*types.Group{{Type: "GROUP_EXT", Params: raw(e)}},
			Cell:               &types.Aggregation{Type: "AGG_EXT", Params: raw(e)},
			MarginAggregations: []*types.Aggregation{{Type: "AGG_EXT", Params: raw(e)}},
		},
	}
	before, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	res := extscalar.Resolve(req, flagged, staticEnv, false)
	if len(res.Refusals) != 0 || len(res.Unresolved) != 0 {
		t.Fatalf("refusals %v unresolved %v", res.Refusals, res.Unresolved)
	}
	after, _ := json.Marshal(req)
	if string(before) != string(after) {
		t.Fatal("Resolve mutated the caller's request")
	}
	r := res.Request
	got := map[string]json.RawMessage{
		"filterers": r.Filterers[0].Params, "aggregations": r.Aggregations[1].Params, "attributes": r.Attributes[0].Params,
		"groups": r.Groups[0].Params, "windows": r.Windows[0].Params, "features": r.Features[0].Params,
		"tests": r.Tests[0].Params, "post_tests": r.PostTests[0].Params, "matrices": r.Matrices[0].Params,
		"regressions": r.Regressions[0].Params, "crosstab.rows": r.Crosstab.Rows[0].Params,
		"crosstab.columns": r.Crosstab.Columns[0].Params, "crosstab.cell": r.Crosstab.Cell.Params,
		"crosstab.margin_aggregations": r.Crosstab.MarginAggregations[0].Params,
	}
	for slot, p := range got {
		if s := scaleOf(t, p); s != "6" {
			t.Errorf("%s scale = %s, want 6", slot, s)
		}
		if !strings.Contains(string(p), `"tag":"x"`) {
			t.Errorf("%s lost an unflagged key: %s", slot, p)
		}
	}
	if s := scaleOf(t, r.Aggregations[0].Params); s != `"params.k"` {
		t.Errorf("built-in AGG_SUM scale rewritten to %s", s)
	}
	var m struct {
		Between struct {
			Params json.RawMessage `json:"params"`
		} `json:"between"`
	}
	if err := json.Unmarshal(r.Matrices[1].Params, &m); err != nil || scaleOf(t, m.Between.Params) != "4" {
		t.Errorf("MAT_DISTANCE between params = %s (%v), want scale 4", r.Matrices[1].Params, err)
	}
}

// TestResolve_IdentityWithoutExpressions: a request carrying no flagged
// expression — a number, a built-in, a nil flagged — comes back as
// itself, the same pointer.
func TestResolve_IdentityWithoutExpressions(t *testing.T) {
	t.Parallel()

	req := &types.Request{Aggregations: []*types.Aggregation{{Type: "AGG_EXT", Params: raw(`{"scale":2}`)}}}
	built := false
	env := func() (scalarslot.Env, exprenv.Bound) { built = true; return staticEnv() }
	if res := extscalar.Resolve(req, flagged, env, false); res.Request != req {
		t.Fatal("a number-only request was copied")
	}
	if built {
		t.Fatal("the environment was built for a request without an expression")
	}
	req.Aggregations[0].Params = raw(`{"scale":"1"}`)
	if res := extscalar.Resolve(req, nil, env, false); res.Request != req {
		t.Fatal("a nil flagged copied the request")
	}
}

// TestResolve_StaticUnresolvedAndRefusals: a static judgement lists a
// request-bound call Unresolved (left as written) and refuses each bad
// expression with PULSE_REQUEST_PARAM_INVALID reason param_*, located.
func TestResolve_StaticUnresolvedAndRefusals(t *testing.T) {
	t.Parallel()

	env := func() (scalarslot.Env, exprenv.Bound) {
		e, _ := staticEnv()
		e.Lookup = true
		return e, e.Declared()
	}
	req := &types.Request{Aggregations: []*types.Aggregation{
		{Type: "AGG_EXT", Params: raw(`{"scale":"lookup(\"t\", \"a\")"}`)},
		{Type: "AGG_EXT", Params: raw(`{"scale":"params.k +"}`)},
		{Type: "AGG_EXT", Params: raw(`{"scale":"params.label"}`)},
		{Type: "AGG_EXT", Params: raw(`{"scale":"params.k / 0"}`)},
	}}
	res := extscalar.Resolve(req, flagged, env, false)
	want := []extscalar.Unresolved{{Path: "aggregations[0].params.scale", Expression: `lookup("t", "a")`, Reads: []string{"lookup"}}}
	if !reflect.DeepEqual(res.Unresolved, want) {
		t.Fatalf("unresolved = %+v", res.Unresolved)
	}
	if s := scaleOf(t, res.Request.Aggregations[0].Params); s != `"lookup(\"t\", \"a\")"` {
		t.Fatalf("an unresolved param was rewritten to %s", s)
	}
	reasons := []string{extscalar.ReasonParamInvalid, extscalar.ReasonParamNotNumeric, extscalar.ReasonParamNotFinite}
	if len(res.Refusals) != len(reasons) {
		t.Fatalf("refusals = %v", res.Refusals)
	}
	for i, ce := range res.Refusals {
		path := "aggregations[" + string(rune('1'+i)) + "].params.scale"
		if ce.Code != errors.PULSE_REQUEST_PARAM_INVALID || ce.Details["reason"] != reasons[i] || ce.Details["path"] != path ||
			ce.Details["operator"] != "AGG_EXT" || ce.Details["param"] != "scale" {
			t.Errorf("refusal %d = %s %v", i, ce.Code, ce.Details)
		}
	}
}

// TestResolve_SharedCodeLocated: a shared code (coef() over no bound
// model: PULSE_MODEL_UNKNOWN) keeps its code, located at the param.
func TestResolve_SharedCodeLocated(t *testing.T) {
	t.Parallel()

	req := &types.Request{Groups: []*types.Group{{Type: "GROUP_EXT", Params: raw(`{"scale":"coef(\"m\", \"x\")"}`)}}}
	res := extscalar.Resolve(req, flagged, staticEnv, false)
	if len(res.Refusals) != 1 {
		t.Fatalf("refusals = %v", res.Refusals)
	}
	ce := res.Refusals[0]
	if ce.Code != errors.PULSE_MODEL_UNKNOWN || ce.Details["path"] != "groups[0].params.scale" || ce.Details["param"] != "scale" {
		t.Fatalf("refusal = %s %v", ce.Code, ce.Details)
	}
}

// TestResolveFilterers: the filterer-only form resolves and locates at
// its prefix.
func TestResolveFilterers(t *testing.T) {
	t.Parallel()

	fs := []*types.Filterer{{Type: "FILTER_EXT", Params: raw(`{"scale":"params.k"}`)}, {Type: "FILTER_EXT", Params: raw(`{"scale":"nope"}`)}}
	out, unresolved, refs := extscalar.ResolveFilterers("filterers", fs, flagged, staticEnv, false)
	if len(unresolved) != 0 || len(refs) != 1 || refs[0].Details["path"] != "filterers[1].params.scale" {
		t.Fatalf("unresolved %v refusals %v", unresolved, refs)
	}
	if scaleOf(t, out[0].Params) != "3" || scaleOf(t, fs[0].Params) != `"params.k"` {
		t.Fatalf("resolved %s, caller's %s", out[0].Params, fs[0].Params)
	}
}

// TestNumber: an integral value is a plain integer (an int-decoding
// factory reads it), any other the shortest round-trip decimal.
func TestNumber(t *testing.T) {
	t.Parallel()

	for v, want := range map[float64]string{6: "6", 1e6: "1000000", -2: "-2", 1.5: "1.5", 0.7: "0.7", 1e300: "1e+300", 1.0 / 3: "0.3333333333333333"} {
		if got := string(extscalar.Number(v)); got != want {
			t.Errorf("Number(%v) = %s, want %s", v, got, want)
		}
		var n int
		if want == "1000000" && json.Unmarshal(extscalar.Number(v), &n) != nil {
			t.Errorf("Number(%v) does not decode into an int", v)
		}
	}
}

// TestStandIns: an expression-capable param written as an expression
// string becomes the number 0; a number, an unflagged key, non-object
// params and no keys leave raw untouched (the same bytes).
func TestStandIns(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"of":"lag","scale":"1 + 1","k":2}`)
	got := extscalar.StandIns(raw, []string{"scale", "k"})
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["scale"] != float64(0) || m["k"] != float64(2) || m["of"] != "lag" {
		t.Fatalf("StandIns = %s", got)
	}
	for _, c := range []struct {
		raw  json.RawMessage
		keys []string
	}{
		{raw, nil},
		{raw, []string{"k"}},
		{json.RawMessage(`[1]`), []string{"scale"}},
		{nil, []string{"scale"}},
	} {
		if out := extscalar.StandIns(c.raw, c.keys); string(out) != string(c.raw) {
			t.Errorf("StandIns(%s, %v) = %s, want raw", c.raw, c.keys, out)
		}
	}
}
