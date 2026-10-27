package vectors

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func scs(params string, mod func(*types.Attribute)) *types.Attribute {
	a := &types.Attribute{Type: types.ATTR_SCALE_SCORE, Params: json.RawMessage(params)}
	mod(a)
	return a
}

func f64p(v float64) *float64 { return &v }

// TestResolveAttributes_ScaleScoreRules pins ATTR_SCALE_SCORE's slot
// rules through the one resolver predict and the runtime share: the
// decoded config of each valid slot and the code + reason of each
// refusal.
func TestResolveAttributes_ScaleScoreRules(t *testing.T) {
	t.Parallel()

	vecs := []types.VectorSpec{
		{Name: "va", Fields: []string{"a*"}},
		{Name: "vs", Fields: []string{"b1", "b2", "a1"}, Labels: []string{"x", "y", "z"}, Kind: types.VectorKindScale, ScaleMin: f64p(1), ScaleMax: f64p(5)},
	}
	cases := []struct {
		name   string
		attr   *types.Attribute
		want   ScaleScoreConfig
		code   errors.Code
		reason string
	}{
		{name: "defaults", attr: scs(``, func(a *types.Attribute) { a.Vector = "va" }),
			want: ScaleScoreConfig{Stat: ScaleStatMean, MinValid: 3}},
		{name: "sum min_valid", attr: scs(`{"stat": "sum", "min_valid": 2}`, func(a *types.Attribute) { a.Fields = []string{"b1", "b2"} }),
			want: ScaleScoreConfig{Stat: ScaleStatSum, MinValid: 2}},
		{name: "kind scale bounds fall back", attr: scs(`{"reverse": ["y"]}`, func(a *types.Attribute) { a.Vector = "vs" }),
			want: ScaleScoreConfig{Stat: ScaleStatMean, MinValid: 3, HasScale: true, Key: ScaleKey{Lo: 1, Hi: 5, Reverse: []int{1}}}},
		{name: "params bounds win", attr: scs(`{"scale_min": 0, "scale_max": 10, "reverse": ["z", "x"]}`, func(a *types.Attribute) { a.Vector = "vs" }),
			want: ScaleScoreConfig{Stat: ScaleStatMean, MinValid: 3, HasScale: true, Key: ScaleKey{Lo: 0, Hi: 10, Reverse: []int{0, 2}}}},
		{name: "inline reverse by member name", attr: scs(`{"scale_min": 1, "scale_max": 7, "reverse": ["b2"]}`, func(a *types.Attribute) { a.Fields = []string{"b1", "b2"} }),
			want: ScaleScoreConfig{Stat: ScaleStatMean, MinValid: 2, HasScale: true, Key: ScaleKey{Lo: 1, Hi: 7, Reverse: []int{1}}}},
		{name: "reverse without range", attr: scs(`{"reverse": ["a1"]}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "reverse_needs_scale"},
		{name: "reverse by name not label", attr: scs(`{"reverse": ["b1"]}`, func(a *types.Attribute) { a.Vector = "vs" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "reverse twice", attr: scs(`{"reverse": ["x", "x"]}`, func(a *types.Attribute) { a.Vector = "vs" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "min_valid zero", attr: scs(`{"min_valid": 0}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "min_valid beyond p", attr: scs(`{"min_valid": 4}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "min_valid fractional", attr: scs(`{"min_valid": 1.5}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "bad stat", attr: scs(`{"stat": "total"}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "half range", attr: scs(`{"scale_min": 1}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "min not below max", attr: scs(`{"scale_min": 5, "scale_max": 5}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "unknown key", attr: scs(`{"fit_on": "filtered"}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "field set", attr: scs(``, func(a *types.Attribute) { a.Field, a.Vector = "a1", "va" }), code: errors.SERVICE_VALIDATION, reason: "field_on_vector_attribute"},
		{name: "neither", attr: scs(``, func(*types.Attribute) {}), code: errors.SERVICE_VALIDATION, reason: "no_vector_or_fields"},
		{name: "unknown vector", attr: scs(``, func(a *types.Attribute) { a.Vector = "nope" }), code: errors.PULSE_VECTOR_UNKNOWN},
		{name: "categorical member", attr: scs(``, func(a *types.Attribute) { a.Fields = []string{"a1", "region"} }), code: errors.PULSE_VECTOR_MEMBER_TYPE},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Vectors: vecs, Attributes: []*types.Attribute{c.attr}}
			got, err := ResolveAttributes(req, testInputSchema(), nil, nil, nil)
			if c.code != "" {
				if err == nil {
					t.Fatalf("want %s, resolved %+v", c.code, got)
				}
				if err.Code != c.code {
					t.Fatalf("code %s, want %s (%s)", err.Code, c.code, err.Message)
				}
				if c.reason != "" && err.Details["reason"] != c.reason {
					t.Fatalf("reason %v, want %s (%s)", err.Details["reason"], c.reason, err.Message)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal %s: %s", err.Code, err.Message)
			}
			if !got[0].Has() {
				t.Fatal("no members resolved")
			}
			if got[0].FitOn != nil {
				t.Errorf("row-local attribute decoded a fit_on %v", got[0].FitOn)
			}
			cfg, cerr := ScaleScoreParams("at", c.attr.Params, got[0].Members)
			if cerr != nil {
				t.Fatal(cerr)
			}
			if !reflect.DeepEqual(cfg, c.want) {
				t.Errorf("config %+v, want %+v", cfg, c.want)
			}
			// The canonical rewrite over the literal members (labels =
			// names) decodes back to the same config.
			canon := cfg.CanonicalParams(got[0].Members.Members)
			lit := Resolved{Members: got[0].Members.Members, Labels: got[0].Members.Members}
			back, berr := ScaleScoreParams("at", canon, lit)
			if berr != nil {
				t.Fatalf("canonical %s refused: %s", canon, berr.Message)
			}
			if !reflect.DeepEqual(back, c.want) {
				t.Errorf("canonical %s decodes to %+v, want %+v", canon, back, c.want)
			}
		})
	}
}

// TestScaleKey pins the shared range check and flip.
func TestScaleKey(t *testing.T) {
	t.Parallel()

	k := ScaleKey{Lo: 1, Hi: 5, Reverse: []int{0, 2}}
	for _, c := range []struct {
		v   float64
		out bool
	}{{1, false}, {5, false}, {0.5, true}, {5.5, true}, {math.NaN(), false}} {
		if got := k.OutOfRange(c.v); got != c.out {
			t.Errorf("OutOfRange(%v) = %v, want %v", c.v, got, c.out)
		}
	}
	if i := k.FirstOutOfRange([]float64{1, math.NaN(), 9, 0}); i != 2 {
		t.Errorf("FirstOutOfRange = %d, want 2", i)
	}
	if i := k.FirstOutOfRange([]float64{1, 5, math.NaN()}); i != -1 {
		t.Errorf("FirstOutOfRange in range = %d, want -1", i)
	}
	x := []float64{2, 3, math.NaN()}
	k.ApplyReverse(x)
	if x[0] != 4 || x[1] != 3 || !math.IsNaN(x[2]) {
		t.Errorf("ApplyReverse = %v, want [4 3 NaN]", x)
	}
}

// TestVectorAttributeTypes_ScaleScoreNotFitted: ATTR_SCALE_SCORE is a
// vector-input attribute but never a fitted one.
func TestVectorAttributeTypes_ScaleScoreNotFitted(t *testing.T) {
	t.Parallel()

	if !IsVectorAttribute(types.ATTR_SCALE_SCORE) {
		t.Error("ATTR_SCALE_SCORE is not a vector-input attribute")
	}
	if IsFittedAttribute(types.ATTR_SCALE_SCORE) {
		t.Error("ATTR_SCALE_SCORE is listed as fitted")
	}
	for _, f := range FittedAttributeTypes() {
		if !IsVectorAttribute(f) {
			t.Errorf("fitted %s is not a vector-input attribute", f)
		}
	}
}
