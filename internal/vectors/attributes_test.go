package vectors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func pcs(params string, mod func(*types.Attribute)) *types.Attribute {
	a := &types.Attribute{Type: types.ATTR_PC_SCORE, Params: json.RawMessage(params)}
	mod(a)
	return a
}

func fsa(params string, mod func(*types.Attribute)) *types.Attribute {
	a := &types.Attribute{Type: types.ATTR_FACTOR_SCORE, Params: json.RawMessage(params)}
	mod(a)
	return a
}

func maha(mod func(*types.Attribute)) *types.Attribute {
	a := &types.Attribute{Type: types.ATTR_MAHALANOBIS}
	mod(a)
	return a
}

// TestResolveAttributes_Rules pins every vector-input attribute rule:
// the members a valid slot resolves to, its decoded fit_on, and the code
// + details.reason of each refusal.
func TestResolveAttributes_Rules(t *testing.T) {
	vecs := []types.VectorSpec{
		{Name: "va", Fields: []string{"a*"}},
		{Name: "vb", Fields: []string{"b1", "b2"}},
	}
	cases := []struct {
		name    string
		attr    *types.Attribute
		members []string
		fitOn   int
		code    errors.Code
		reason  string
	}{
		{name: "vector", attr: maha(func(a *types.Attribute) { a.Vector = "va" }), members: []string{"a1", "a2", "a3"}},
		{name: "inline glob in place", attr: maha(func(a *types.Attribute) { a.Fields = []string{"b2", "a[12]"} }), members: []string{"b2", "a1", "a2"}},
		{name: "fit_on filtered", attr: maha(func(a *types.Attribute) {
			a.Fields = []string{"a1", "a2"}
			a.Params = json.RawMessage(`{"fit_on": "filtered", "output": "p", "missing": "listwise"}`)
		}), members: []string{"a1", "a2"}},
		{name: "fit_on filterers", attr: maha(func(a *types.Attribute) {
			a.Fields = []string{"a1", "a2"}
			a.Params = json.RawMessage(`{"fit_on": [{"type": "FILTER_INCLUDE", "field": "region", "values": ["n"]}, {"type": "FILTER_RANGE", "field": "b1", "values": ["0", "9"]}]}`)
		}), members: []string{"a1", "a2"}, fitOn: 2},
		{name: "vector and fields", attr: maha(func(a *types.Attribute) { a.Vector, a.Fields = "va", []string{"a1"} }), code: errors.SERVICE_VALIDATION, reason: "vector_and_fields"},
		{name: "neither", attr: maha(func(*types.Attribute) {}), code: errors.SERVICE_VALIDATION, reason: "no_vector_or_fields"},
		{name: "field on vector attribute", attr: maha(func(a *types.Attribute) { a.Field, a.Fields = "a1", []string{"a2"} }), code: errors.SERVICE_VALIDATION, reason: "field_on_vector_attribute"},
		{name: "vector on scalar attribute", attr: &types.Attribute{Type: types.ATTR_ZSCORE, Field: "a1", Fields: []string{"a2"}}, code: errors.SERVICE_VALIDATION, reason: "vector_not_accepted"},
		{name: "unknown vector", attr: maha(func(a *types.Attribute) { a.Vector = "nope" }), code: errors.PULSE_VECTOR_UNKNOWN},
		{name: "categorical member", attr: maha(func(a *types.Attribute) { a.Fields = []string{"a1", "region"} }), code: errors.PULSE_VECTOR_MEMBER_TYPE},
		{name: "pairwise refused", attr: maha(func(a *types.Attribute) {
			a.Fields = []string{"a1", "a2"}
			a.Params = json.RawMessage(`{"missing": "pairwise"}`)
		}), code: errors.SERVICE_VALIDATION, reason: "pairwise_unsupported"},
		{name: "bad output", attr: maha(func(a *types.Attribute) {
			a.Fields = []string{"a1", "a2"}
			a.Params = json.RawMessage(`{"output": "d"}`)
		}), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "unknown key", attr: maha(func(a *types.Attribute) {
			a.Fields = []string{"a1", "a2"}
			a.Params = json.RawMessage(`{"center": [0, 0]}`)
		}), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "fit_on other string", attr: maha(func(a *types.Attribute) {
			a.Fields = []string{"a1", "a2"}
			a.Params = json.RawMessage(`{"fit_on": "all"}`)
		}), code: errors.SERVICE_VALIDATION, reason: "bad_fit_on"},
		{name: "fit_on empty list", attr: maha(func(a *types.Attribute) {
			a.Fields = []string{"a1", "a2"}
			a.Params = json.RawMessage(`{"fit_on": []}`)
		}), code: errors.SERVICE_VALIDATION, reason: "bad_fit_on"},
		{name: "fit_on untyped filterer", attr: maha(func(a *types.Attribute) {
			a.Fields = []string{"a1", "a2"}
			a.Params = json.RawMessage(`{"fit_on": [{"field": "a1"}]}`)
		}), code: errors.SERVICE_VALIDATION, reason: "bad_fit_on"},
		// ATTR_PC_SCORE: its own keys, then the member-count bounds
		// once the members resolve (va has 3 members).
		{name: "pc score", attr: pcs(`{"component": 3, "on": "covariance", "fit_on": [{"type": "FILTER_TRUE"}]}`, func(a *types.Attribute) { a.Vector = "va" }), members: []string{"a1", "a2", "a3"}, fitOn: 1},
		{name: "pc score varimax", attr: pcs(`{"component": 2, "rotation": "varimax", "components": 3}`, func(a *types.Attribute) { a.Vector = "va" }), members: []string{"a1", "a2", "a3"}},
		{name: "pc score explicit none", attr: pcs(`{"component": 1, "rotation": "none"}`, func(a *types.Attribute) { a.Vector = "va" }), members: []string{"a1", "a2", "a3"}},
		{name: "pc score no component", attr: pcs(`{"on": "covariance"}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "pc score no params", attr: pcs(``, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "pc score fractional component", attr: pcs(`{"component": 1.5}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "pc score component beyond p", attr: pcs(`{"component": 4}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "component_out_of_range"},
		{name: "pc score rotated k beyond p", attr: pcs(`{"component": 1, "rotation": "varimax", "components": 4}`, func(a *types.Attribute) { a.Fields = []string{"b1", "b2", "a1"} }), code: errors.SERVICE_VALIDATION, reason: "component_out_of_range"},
		{name: "pc score k below component", attr: pcs(`{"component": 3, "rotation": "varimax", "components": 2}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "pc score components unrotated", attr: pcs(`{"component": 1, "components": 2}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "pc score varimax without k", attr: pcs(`{"component": 1, "rotation": "varimax"}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "pc score promax", attr: pcs(`{"component": 1, "rotation": "promax", "components": 2}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "pc score bad on", attr: pcs(`{"component": 1, "on": "ranks"}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "pc score output key", attr: pcs(`{"component": 1, "output": "p"}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		// ATTR_FACTOR_SCORE: factor + method, MAT_FACTOR's knobs by
		// MAT_FACTOR's rule, then the member-count bounds (va has 3).
		{name: "factor score", attr: fsa(`{"factor": 1, "factors": 1, "method": "bartlett", "extraction": "ml", "fit_on": [{"type": "FILTER_TRUE"}]}`, func(a *types.Attribute) { a.Vector = "va" }), members: []string{"a1", "a2", "a3"}, fitOn: 1},
		{name: "factor score kaiser oblimin", attr: fsa(`{"factor": 2, "rotation": "oblimin", "gamma": 0.5}`, func(a *types.Attribute) { a.Vector = "va" }), members: []string{"a1", "a2", "a3"}},
		{name: "factor score promax kappa", attr: fsa(`{"factor": 2, "factors": 3, "rotation": "promax", "kappa": 3}`, func(a *types.Attribute) { a.Vector = "va" }), members: []string{"a1", "a2", "a3"}},
		{name: "factor score no factor", attr: fsa(`{"factors": 2}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "factor score no params", attr: fsa(``, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "factor score factor beyond factors", attr: fsa(`{"factor": 2, "factors": 1}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "factor score factor beyond p", attr: fsa(`{"factor": 4}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "factor_out_of_range"},
		{name: "factor score factors beyond p", attr: fsa(`{"factor": 1, "factors": 4}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "factor_out_of_range"},
		{name: "factor score bad method", attr: fsa(`{"factor": 1, "method": "tenberge"}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "factor score kappa without promax", attr: fsa(`{"factor": 1, "kappa": 3}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "factor score repair key", attr: fsa(`{"factor": 1, "repair": "nearest"}`, func(a *types.Attribute) { a.Vector = "va" }), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Vectors: vecs, Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "1"}, c.attr}}
			got, err := ResolveAttributes(req, testInputSchema(), nil, nil)
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
				if c.reason != "" && err.Details["attribute"] != "attributes[1]" {
					t.Errorf("attribute path %v, want attributes[1]", err.Details["attribute"])
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal %s: %s", err.Code, err.Message)
			}
			if got[0].Has() {
				t.Errorf("ATTR_FORMULA resolved a vector input %+v", got[0])
			}
			if !reflect.DeepEqual(got[1].Members.Members, c.members) {
				t.Errorf("members %v, want %v", got[1].Members.Members, c.members)
			}
			if len(got[1].FitOn) != c.fitOn {
				t.Errorf("fit_on has %d filterers, want %d", len(got[1].FitOn), c.fitOn)
			}
		})
	}
}

// TestResolveAttributes_RouteHidesType: a hidden vector attribute routes
// to an unknown name and is not judged (its absence is refused by the
// registry elsewhere).
func TestResolveAttributes_RouteHidesType(t *testing.T) {
	req := &types.Request{Attributes: []*types.Attribute{maha(func(a *types.Attribute) { a.Vector = "nope" })}}
	if _, err := ResolveAttributes(req, testInputSchema(), nil, nil); err == nil {
		t.Fatal("vacuous: the unrouted slot is not refused")
	}
	if _, err := ResolveAttributes(req, testInputSchema(), func(types.AttributeType) types.AttributeType { return "ATTR_HIDDEN" }, nil); err != nil {
		t.Fatalf("a hidden type was judged: %s", err.Message)
	}
}

func TestVectorAttributeLabel(t *testing.T) {
	cases := []struct {
		attr *types.Attribute
		want string
		ok   bool
	}{
		{maha(func(a *types.Attribute) { a.Vector = "battery" }), "ATTR_MAHALANOBIS_battery", true},
		{maha(func(a *types.Attribute) { a.Fields = []string{"q*", "x"} }), "ATTR_MAHALANOBIS_q*", true},
		{pcs(`{"component": 2}`, func(a *types.Attribute) { a.Vector = "battery" }), "ATTR_PC_SCORE_battery_PC2", true},
		{pcs(`{"component": 1, "rotation": "varimax", "components": 2}`, func(a *types.Attribute) { a.Fields = []string{"q*"} }), "ATTR_PC_SCORE_q*_PC1", true},
		// Undecodable params: the base rule (the slot is refused elsewhere).
		{pcs(`{"component": "x"}`, func(a *types.Attribute) { a.Vector = "battery" }), "ATTR_PC_SCORE_battery", true},
		{fsa(`{"factor": 2}`, func(a *types.Attribute) { a.Vector = "battery" }), "ATTR_FACTOR_SCORE_battery_F2", true},
		{fsa(`{"factor": 1, "method": "bartlett"}`, func(a *types.Attribute) { a.Fields = []string{"q*"} }), "ATTR_FACTOR_SCORE_q*_F1", true},
		{fsa(`{"factors": 2}`, func(a *types.Attribute) { a.Vector = "battery" }), "ATTR_FACTOR_SCORE_battery", true},
		{&types.Attribute{Type: types.ATTR_ZSCORE, Field: "x"}, "", false},
	}
	for _, c := range cases {
		got, ok := VectorAttributeLabel(c.attr.Type, c.attr)
		if got != c.want || ok != c.ok {
			t.Errorf("%+v: label %q ok %v, want %q %v", c.attr, got, ok, c.want, c.ok)
		}
	}
}

// TestReferenced_CountsAttributeVectors: an attribute-referenced vector
// is not unreferenced.
func TestReferenced_CountsAttributeVectors(t *testing.T) {
	req := &types.Request{
		Vectors:    []types.VectorSpec{{Name: "va", Fields: []string{"a1"}}, {Name: "vb", Fields: []string{"b1"}}},
		Attributes: []*types.Attribute{maha(func(a *types.Attribute) { a.Vector = "va" })},
	}
	if got := Unreferenced(req); !reflect.DeepEqual(got, []string{"vb"}) {
		t.Fatalf("unreferenced %v, want [vb]", got)
	}
}

func TestAttributeInputMembersAndFitOn(t *testing.T) {
	a := maha(func(a *types.Attribute) {
		a.Fields = []string{"b*"}
		a.Params = json.RawMessage(`{"fit_on": [{"type": "FILTER_NULL", "field": "a3"}]}`)
	})
	req := &types.Request{Attributes: []*types.Attribute{a}}
	got, ok := AttributeInputMembers(req, testInputSchema())
	if !ok || !reflect.DeepEqual(got, []string{"b1", "b2"}) {
		t.Fatalf("members %v ok %v, want [b1 b2] true", got, ok)
	}
	fs := AttributeFitOn(a.Type, a)
	if len(fs) != 1 || fs[0].Field != "a3" {
		t.Fatalf("fit_on %+v, want one FILTER_NULL on a3", fs)
	}
	if AttributeFitOn(types.ATTR_ZSCORE, a) != nil {
		t.Error("a non-vector route decoded fit_on")
	}
	bad := maha(func(a *types.Attribute) { a.Fields = []string{"zz"} })
	if _, ok := AttributeInputMembers(&types.Request{Attributes: []*types.Attribute{bad}}, testInputSchema()); ok {
		t.Error("an unresolvable inline member did not widen")
	}
}
