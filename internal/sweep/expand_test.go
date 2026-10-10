package sweep

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

func num(s string) json.Number { return json.Number(s) }

// decodeComposed decodes a wire ComposedRequest the way every surface
// does (axis numbers keep their text).
func decodeComposed(t *testing.T, raw string) *types.ComposedRequest {
	t.Helper()
	var c types.ComposedRequest
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func mustExpand(t *testing.T, c *types.ComposedRequest) *Expansion {
	t.Helper()
	exp, err := Expand(c, nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	return exp
}

func requireSweepInvalid(t *testing.T, err error, reason string) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want PULSE_SWEEP_INVALID/%s, got %v", reason, err)
	}
	if ce.Code != errors.PULSE_SWEEP_INVALID || ce.Details["reason"] != reason {
		t.Fatalf("got %s reason=%v (%s), want PULSE_SWEEP_INVALID/%s", ce.Code, ce.Details["reason"], ce.Message, reason)
	}
	if _, ok := ce.Details["field"]; !ok {
		t.Fatalf("details carry no field: %v", ce.Details)
	}
	return ce
}

func labelsOf(reqs []*types.Request) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.Label
	}
	return out
}

// TestExpand_GridOrderAndDefaultLabel: grid is row-major with the FIRST
// axis slowest, and the default label is `<axis>=<value>` joined by `_`.
func TestExpand_GridOrderAndDefaultLabel(t *testing.T) {
	c := decodeComposed(t, `{"requests":[],"sweep":{
		"axes":[{"name":"a","values":[1,2]},{"name":"b","values":["x","y","z"]}],
		"request":{"cohort":{"filename":"{{b}}.pulse"},"return":{"precision":{"$var":"a"}}}}}`)
	exp := mustExpand(t, c)
	want := []string{"a=1_b=x", "a=1_b=y", "a=1_b=z", "a=2_b=x", "a=2_b=y", "a=2_b=z"}
	if got := labelsOf(exp.Composed.Requests); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels %v, want %v", got, want)
	}
	if !reflect.DeepEqual(exp.Labels, want) {
		t.Fatalf("Expansion.Labels %v, want %v", exp.Labels, want)
	}
	if exp.Composed.Requests[4].Cohort.Filename != "y.pulse" || exp.Composed.Requests[4].Return.Precision != 2 {
		t.Fatalf("slot 4 = %+v", exp.Composed.Requests[4])
	}
	if Count(c.Sweep) != 6 {
		t.Fatalf("Count = %d", Count(c.Sweep))
	}
}

// TestExpand_ZipLockstep: zip pairs the i-th value of every axis.
func TestExpand_ZipLockstep(t *testing.T) {
	c := decodeComposed(t, `{"requests":[],"sweep":{"mode":"zip","label":"{{f}}-{{n}}",
		"axes":[{"name":"f","values":["p","q","r"]},{"name":"n","values":[10,20,30]}],
		"request":{"cohort":{"filename":"{{f}}"},"return":{"precision":{"$var":"n"}}}}}`)
	exp := mustExpand(t, c)
	if got, want := labelsOf(exp.Composed.Requests), []string{"p-10", "q-20", "r-30"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("labels %v, want %v", got, want)
	}
	for i, r := range exp.Composed.Requests {
		if r.Return.Precision != (i+1)*10 || r.Cohort.Filename != []string{"p", "q", "r"}[i] {
			t.Fatalf("slot %d = %+v", i, r)
		}
	}
	if Count(c.Sweep) != 3 {
		t.Fatalf("Count = %d", Count(c.Sweep))
	}
}

// TestExpand_ScalarRendering: `{{}}` renders numbers exactly as written
// and booleans/strings as text; `$var` keeps the JSON type; a
// placeholder may sit inside a field-name value.
func TestExpand_ScalarRendering(t *testing.T) {
	c := decodeComposed(t, `{"requests":[],"sweep":{
		"label":"tv{{tv}}_{{flag}}_{{tag}}",
		"axes":[{"name":"tv","values":[10,0.1,1.50]},{"name":"flag","values":[true]},{"name":"tag","values":["s"]}],
		"request":{"cohort":{"filename":"c.pulse"},
			"aggregations":[{"type":"AGG_SUM","field":"tv_ad{{tv}}","label":"m_{{tag}}_{{flag}}"}],
			"tests":[{"type":"TEST_T","params":{"q":{"$var":"tv"}}}],
			"disable_components":{"$var":"flag"}}}}`)
	exp := mustExpand(t, c)
	if got, want := exp.Labels, []string{"tv10_true_s", "tv0.1_true_s", "tv1.50_true_s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("labels %v, want %v", got, want)
	}
	for i, field := range []string{"tv_ad10", "tv_ad0.1", "tv_ad1.50"} {
		r := exp.Composed.Requests[i]
		if r.Aggregations[0].Field != field || r.Aggregations[0].Label != "m_s_true" {
			t.Fatalf("slot %d aggregation %+v", i, r.Aggregations[0])
		}
		if r.DisableComponents == nil || !*r.DisableComponents {
			t.Fatalf("slot %d: $var boolean not spliced as a boolean", i)
		}
	}
	raw := string(exp.Composed.Requests[2].Tests[0].Params)
	if raw != `{"q":1.50}` {
		t.Fatalf("$var number not spliced as a number: %s", raw)
	}
}

// TestExpand_GoValues: a Go-built sweep (ints, floats, strings, bools,
// mixed kinds on one axis) renders like its wire twin.
func TestExpand_GoValues(t *testing.T) {
	c := &types.ComposedRequest{Sweep: &types.SweepSpec{
		Axes:    []types.SweepAxis{{Name: "v", Values: []any{3, 0.25, "x", false}}},
		Request: json.RawMessage(`{"cohort":{"filename":"c{{v}}.pulse"}}`),
	}}
	exp := mustExpand(t, c)
	want := []string{"c3.pulse", "c0.25.pulse", "cx.pulse", "cfalse.pulse"}
	for i, r := range exp.Composed.Requests {
		if r.Cohort.Filename != want[i] {
			t.Fatalf("slot %d filename %q, want %q", i, r.Cohort.Filename, want[i])
		}
	}
	if got := exp.Labels; !reflect.DeepEqual(got, []string{"v=3", "v=0.25", "v=x", "v=false"}) {
		t.Fatalf("labels %v", got)
	}
}

// TestExpand_ExplicitFirstAndOverlays: explicit slots keep their place
// and pointers, sweep slots follow, sweep overlays are substituted per
// slot and appended after the explicit specs; the input is untouched
// and the effective request is sweep-free.
func TestExpand_ExplicitFirstAndOverlays(t *testing.T) {
	base := &types.Request{Label: "base", Cohort: &types.Cohort{Filename: "c.pulse"}}
	explicitOverlay := types.ComposeOverlaySpec{Kind: types.OverlayKind("OVERLAY_INDEX_VS_REF"), Reference: "base", Targets: []string{"base"}}
	c := &types.ComposedRequest{
		Requests: []*types.Request{base, nil},
		Overlays: []types.ComposeOverlaySpec{explicitOverlay},
		Sweep: &types.SweepSpec{
			Axes:     []types.SweepAxis{{Name: "k", Values: []any{num("1"), num("2")}}},
			Label:    "k{{k}}",
			Request:  json.RawMessage(`{"cohort":{"filename":"c.pulse"},"return":{"precision":{"$var":"k"}}}`),
			Overlays: json.RawMessage(`[{"name":"d_{{k}}","kind":"OVERLAY_DELTA_VS_REF","reference":"base","targets":["k{{k}}"]}]`),
		},
	}
	before, _ := json.Marshal(c)
	exp := mustExpand(t, c)
	eff := exp.Composed
	if eff == c || eff.Sweep != nil {
		t.Fatal("effective request must be a sweep-free clone")
	}
	if exp.Explicit != 2 || len(eff.Requests) != 4 || eff.Requests[0] != base || eff.Requests[1] != nil {
		t.Fatalf("explicit slots not first/preserved: explicit=%d %v", exp.Explicit, eff.Requests)
	}
	if got := []string{eff.Requests[2].Label, eff.Requests[3].Label}; !reflect.DeepEqual(got, []string{"k1", "k2"}) {
		t.Fatalf("sweep labels %v", got)
	}
	if len(eff.Overlays) != 3 || !reflect.DeepEqual(eff.Overlays[0], explicitOverlay) {
		t.Fatalf("overlays %+v", eff.Overlays)
	}
	for i, want := range []string{"k1", "k2"} {
		ov := eff.Overlays[i+1]
		if ov.Kind != "OVERLAY_DELTA_VS_REF" || ov.Reference != "base" || !reflect.DeepEqual(ov.Targets, []string{want}) || ov.Name != "d_"+want[1:] {
			t.Fatalf("sweep overlay %d = %+v", i, ov)
		}
	}
	after, _ := json.Marshal(c)
	if string(before) != string(after) || base.Label != "base" {
		t.Fatal("Expand mutated its input")
	}
}

// TestExpand_SweepFree: no sweep → the input pointer, preflight with the
// explicit count.
func TestExpand_SweepFree(t *testing.T) {
	c := &types.ComposedRequest{Requests: []*types.Request{{}, {}}}
	var seen []int
	exp, err := Expand(c, func(n int) error { seen = append(seen, n); return nil })
	if err != nil || exp.Composed != c || exp.Explicit != 2 || exp.Labels != nil {
		t.Fatalf("exp=%+v err=%v", exp, err)
	}
	if !reflect.DeepEqual(seen, []int{2}) {
		t.Fatalf("preflight saw %v", seen)
	}
	if exp, err := Expand(nil, nil); err != nil || exp.Composed != nil {
		t.Fatalf("nil: %+v %v", exp, err)
	}
}

// TestExpand_PreflightBeforeRender: the limit sees explicit + expanded
// slots, and its refusal returns before any slot is rendered — a body
// that could never decode is never reached.
func TestExpand_PreflightBeforeRender(t *testing.T) {
	c := &types.ComposedRequest{
		Requests: []*types.Request{{}},
		Sweep: &types.SweepSpec{
			Axes:    []types.SweepAxis{{Name: "a", Values: []any{1, 2, 3}}, {Name: "b", Values: []any{1, 2}}},
			Request: json.RawMessage(`{"not_a_slot":"{{a}}{{b}}"}`),
		},
	}
	l := limits.Limits{MaxComposeSlots: 6}
	var seen int
	_, err := Expand(c, func(n int) error {
		seen = n
		if ce := limits.CheckComposeSlots(l, n); ce != nil {
			return ce
		}
		return nil
	})
	if seen != 7 {
		t.Fatalf("preflight saw %d slots, want 7 (1 explicit + 3x2)", seen)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("want PULSE_LIMIT_EXCEEDED, got %v", err)
	}
	// Under the limit the same body reaches the strict decode.
	_, err = Expand(c, func(int) error { return nil })
	requireSweepInvalid(t, err, ReasonRequestDecode)
}

// TestExpand_448UnderDefaultLimit: a 7x4x4x4 sweep (448 slots) expands
// under the default MaxComposeSlots.
func TestExpand_448UnderDefaultLimit(t *testing.T) {
	axis := func(name string, n int) types.SweepAxis {
		ax := types.SweepAxis{Name: name}
		for i := range n {
			ax.Values = append(ax.Values, (i+1)*10)
		}
		return ax
	}
	c := &types.ComposedRequest{Sweep: &types.SweepSpec{
		Axes:    []types.SweepAxis{axis("tv", 7), axis("search", 4), axis("social", 4), axis("display", 4)},
		Label:   "tv{{tv}}_se{{search}}_so{{social}}_di{{display}}",
		Request: json.RawMessage(`{"cohort":{"filename":"m.pulse"},"aggregations":[{"type":"AGG_SUM","field":"tv_ad{{tv}}","label":"a"},{"type":"AGG_SUM","field":"se{{search}}_so{{social}}_di{{display}}","label":"b"}]}`),
	}}
	def := limits.Defaults()
	exp, err := Expand(c, func(n int) error {
		if ce := limits.CheckComposeSlots(def, n); ce != nil {
			return ce
		}
		return nil
	})
	if err != nil {
		t.Fatalf("448-slot sweep refused under the default limit: %v", err)
	}
	if len(exp.Composed.Requests) != 448 || exp.Labels[0] != "tv10_se10_so10_di10" || exp.Labels[447] != "tv70_se40_so40_di40" {
		t.Fatalf("got %d slots, first %q last %q", len(exp.Composed.Requests), exp.Labels[0], exp.Labels[len(exp.Labels)-1])
	}
	seen := map[string]bool{}
	for _, l := range exp.Labels {
		if seen[l] {
			t.Fatalf("duplicate label %q", l)
		}
		seen[l] = true
	}
}

func TestCount_Saturates(t *testing.T) {
	big := make([]any, 1<<20)
	for i := range big {
		big[i] = i
	}
	spec := &types.SweepSpec{Axes: []types.SweepAxis{{Name: "a", Values: big}, {Name: "b", Values: big}, {Name: "c", Values: big}, {Name: "d", Values: big}}}
	if got := Count(spec); got != math.MaxInt {
		t.Fatalf("Count = %d, want saturation at MaxInt", got)
	}
	if Count(nil) != 0 {
		t.Fatal("Count(nil) != 0")
	}
}

// TestExpand_References drives FR-3: every axis referenced by the body,
// the AUTHORED label or the overlays; every placeholder names an axis.
func TestExpand_References(t *testing.T) {
	const body = `{"cohort":{"filename":"c.pulse"},"return":{"precision":{"$var":"a"}}}`
	cases := []struct {
		name    string
		sweep   string
		reason  string // "" = valid
		details map[string]any
	}{
		{"all referenced", `{"axes":[{"name":"a","values":[1]}],"request":` + body + `}`, "", nil},
		{"referenced by label only", `{"axes":[{"name":"a","values":[1]},{"name":"b","values":[1]}],"label":"x{{b}}{{a}}","request":` + body + `}`, "", nil},
		{"referenced by overlays only", `{"axes":[{"name":"a","values":[1]},{"name":"b","values":[1]}],"label":"l{{a}}","request":` + body +
			`,"overlays":[{"kind":"OVERLAY_DELTA_VS_REF","reference":"r{{b}}","targets":["l{{a}}"]}]}`, "", nil},
		{"referenced by guard", `{"axes":[{"name":"a","values":[1]},{"name":"b","values":[1]}],"request":{"cohort":{"filename":"c.pulse"},"return":{"$when":"b","precision":{"$var":"a"}}}}`, "", nil},
		{"unreferenced", `{"axes":[{"name":"a","values":[1]},{"name":"b","values":[1,2]}],"request":` + body + `}`,
			ReasonAxisUnreferenced, map[string]any{"field": "sweep.axes[1].name", "axis": "b"}},
		{"default label does not count", `{"axes":[{"name":"b","values":[1]}],"request":{"cohort":{"filename":"c.pulse"}}}`,
			ReasonAxisUnreferenced, map[string]any{"axis": "b"}},
		{"unknown in body", `{"axes":[{"name":"a","values":[1]}],"request":{"cohort":{"filename":"{{zz}}"},"return":{"precision":{"$var":"a"}}}}`,
			ReasonPlaceholderUnknown, map[string]any{"field": "sweep.request.cohort.filename", "placeholder": "zz"}},
		{"unknown marker", `{"axes":[{"name":"a","values":[1]}],"request":{"cohort":{"filename":"{{a}}"},"return":{"precision":{"$var":"zz"}}}}`,
			ReasonPlaceholderUnknown, map[string]any{"field": "sweep.request.return.precision", "placeholder": "zz"}},
		{"unknown in label", `{"axes":[{"name":"a","values":[1]}],"label":"{{a}}_{{zz}}","request":` + body + `}`,
			ReasonPlaceholderUnknown, map[string]any{"field": "sweep.label", "placeholder": "zz"}},
		{"unknown in overlays", `{"axes":[{"name":"a","values":[1]}],"request":` + body + `,"overlays":[{"kind":"OVERLAY_DELTA_VS_REF","reference":"{{zz}}"}]}`,
			ReasonPlaceholderUnknown, map[string]any{"field": "sweep.overlays[0].reference", "placeholder": "zz"}},
		{"unterminated token", `{"axes":[{"name":"a","values":[1]}],"request":{"cohort":{"filename":"{{a"},"return":{"precision":{"$var":"a"}}}}`,
			ReasonPlaceholderInvalid, map[string]any{"field": "sweep.request.cohort.filename"}},
		{"token in key", `{"axes":[{"name":"a","values":[1]}],"request":{"cohort":{"filename":"x"},"x{{a}}":1}}`,
			ReasonPlaceholderInvalid, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := decodeComposed(t, `{"requests":[],"sweep":`+tc.sweep+`}`)
			_, err := Expand(c, nil)
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			ce := requireSweepInvalid(t, err, tc.reason)
			for k, v := range tc.details {
				if !reflect.DeepEqual(ce.Details[k], v) {
					t.Errorf("details[%s] = %#v, want %#v (%v)", k, ce.Details[k], v, ce.Details)
				}
			}
		})
	}
}

// TestExpand_StrictDecode: a substituted body decodes strictly — an
// unknown key (top level or nested) or a mistyped value is refused.
func TestExpand_StrictDecode(t *testing.T) {
	cases := map[string]struct {
		body  string
		field string
	}{
		"unknown top-level": {`{"cohort":{"filename":"c"},"aggregatoins":[],"return":{"precision":{"$var":"a"}}}`, "aggregatoins"},
		"unknown nested":    {`{"cohort":{"filename":"c","fielname":"x"},"return":{"precision":{"$var":"a"}}}`, "fielname"},
		"mistyped":          {`{"cohort":{"filename":"c"},"return":{"precision":"{{a}}"}}`, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := decodeComposed(t, `{"requests":[],"sweep":{"axes":[{"name":"a","values":[1,2]}],"request":`+tc.body+`}}`)
			_, err := Expand(c, nil)
			ce := requireSweepInvalid(t, err, ReasonRequestDecode)
			if ce.Details["slot"] != 0 || !reflect.DeepEqual(ce.Details["values"], map[string]any{"a": num("1")}) {
				t.Fatalf("slot details %v", ce.Details)
			}
			if tc.field != "" && ce.Details["unknown_field"] != tc.field {
				t.Fatalf("unknown_field = %v, want %s", ce.Details["unknown_field"], tc.field)
			}
			if tc.field != "" && !strings.Contains(ce.Message, tc.field) {
				t.Fatalf("message does not name the field: %s", ce.Message)
			}
		})
	}
}

// TestExpand_LabelFaults: the body may not set `label`, and a pattern
// rendering to "" is refused.
func TestExpand_LabelFaults(t *testing.T) {
	c := decodeComposed(t, `{"requests":[],"sweep":{"axes":[{"name":"a","values":[1]}],"request":{"label":"x","return":{"precision":{"$var":"a"}}}}}`)
	_, err := Expand(c, nil)
	requireSweepInvalid(t, err, ReasonRequestLabelSet)

	c = decodeComposed(t, `{"requests":[],"sweep":{"axes":[{"name":"a","values":["", "y"]}],"label":"{{a}}","request":{"cohort":{"filename":"{{a}}"}}}}`)
	_, err = Expand(c, nil)
	requireSweepInvalid(t, err, ReasonLabelEmpty)
}

// TestExpand_ValidateFirst: a structurally invalid sweep is refused by
// Validate's rules before the reference scan or preflight runs.
func TestExpand_ValidateFirst(t *testing.T) {
	c := decodeComposed(t, `{"requests":[],"sweep":{"axes":[],"request":{"x":"{{zz}}"}}}`)
	called := false
	_, err := Expand(c, func(int) error { called = true; return nil })
	requireSweepInvalid(t, err, ReasonAxesEmpty)
	if called {
		t.Fatal("preflight ran on an invalid sweep")
	}
}

// TestExpand_HashAsWritten: ComposedRequest.Hash covers the sweep as
// written — not its expansion — while the explicit slots keep the
// `request_<i+1>` label default the expansion leaves them (sweep slots
// are appended, so explicit indices never shift).
func TestExpand_HashAsWritten(t *testing.T) {
	build := func(label string) *types.ComposedRequest {
		return decodeComposed(t, `{"requests":[{"label":"`+label+`","cohort":{"filename":"c"}}],
			"sweep":{"axes":[{"name":"a","values":[1,2]}],"request":{"cohort":{"filename":"c"},"return":{"precision":{"$var":"a"}}}}}`)
	}
	unlabeled, labeled := build(""), build("request_1")
	if unlabeled.Hash() != labeled.Hash() {
		t.Fatal("explicit label default diverges from the expansion's namespace in the hash")
	}
	exp := mustExpand(t, unlabeled)
	if exp.Composed.Hash() == unlabeled.Hash() {
		t.Fatal("hash must cover the as-written sweep, not its expansion")
	}
	other := build("")
	other.Sweep.Axes[0].Values = []any{num("1"), num("3")}
	if other.Hash() == unlabeled.Hash() {
		t.Fatal("an axis value change must move the hash")
	}
	// Sweep-free: byte-identical to the pre-sweep shape.
	free := &types.ComposedRequest{Requests: []*types.Request{{Cohort: &types.Cohort{Filename: "c"}}}}
	raw, _ := json.Marshal(free)
	if strings.Contains(string(raw), "sweep") {
		t.Fatalf("sweep-free wire carries sweep: %s", raw)
	}
}
