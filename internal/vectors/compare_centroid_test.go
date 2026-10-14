package vectors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestComparisonParams_CentroidPopulation pins the centroid and
// population reference forms (U30 E3-S1) through the one decode predict
// and the runtime share: the decoded operands of each valid form, the
// reason of each refusal, and the canonical round trip the factory
// decodes back.
func TestComparisonParams_CentroidPopulation(t *testing.T) {
	members := Resolved{Name: "vb", Members: []string{"b1", "b2"}, Labels: []string{"x", "y"}}
	cases := []struct {
		name   string
		ref    string
		form   string
		filter int
		by     string
		pop    string
		static bool
		reason string
	}{
		{name: "centroid of every filtered row", ref: `{"centroid":{}}`, form: ReferenceCentroid, static: true},
		{name: "centroid filter", ref: `{"centroid":{"filter":[{"type":"FILTER_INCLUDE","field":"region","values":["n"]}]}}`, form: ReferenceCentroid, filter: 1, static: true},
		{name: "centroid by", ref: `{"centroid":{"by":"region"}}`, form: ReferenceCentroid, by: "region"},
		{name: "centroid by unsigned integer", ref: `{"centroid":{"by":"a2"}}`, form: ReferenceCentroid, by: "a2"},
		{name: "centroid filter and by", ref: `{"centroid":{"by":"region","filter":[{"type":"FILTER_RANGE","field":"a1","params":{"min":0}}]}}`, form: ReferenceCentroid, filter: 1, by: "region"},
		{name: "population", ref: `{"population":"brand"}`, form: ReferencePopulation, pop: "brand", static: true},
		// A label named after a reserved key stays a label map: its
		// value is a number, never an object / string.
		{name: "label map keyed centroid", ref: `{"x":1,"y":2}`, form: ReferenceLabelMap, static: true},
		{name: "centroid beside another key", ref: `{"centroid":{},"x":1}`, reason: "bad_reference"},
		{name: "centroid unknown inner key", ref: `{"centroid":{"group":"region"}}`, reason: "bad_reference"},
		{name: "centroid empty filter", ref: `{"centroid":{"filter":[]}}`, reason: "bad_reference"},
		{name: "centroid filter not an array", ref: `{"centroid":{"filter":"filtered"}}`, reason: "bad_reference"},
		{name: "centroid filter without type", ref: `{"centroid":{"filter":[{"field":"a1"}]}}`, reason: "bad_reference"},
		{name: "centroid empty by", ref: `{"centroid":{"by":""}}`, reason: "bad_reference"},
		{name: "centroid by unknown", ref: `{"centroid":{"by":"nope"}}`, reason: "by_unknown"},
		{name: "centroid by float", ref: `{"centroid":{"by":"a1"}}`, reason: "by_type"},
		{name: "centroid by packed_bool", ref: `{"centroid":{"by":"flag"}}`, reason: "by_type"},
		{name: "population empty", ref: `{"population":""}`, reason: "bad_reference"},
		{name: "population beside another key", ref: `{"population":"brand","table":1}`, reason: "bad_reference"},
	}
	for _, typ := range ComparisonTypes() {
		for _, c := range cases {
			t.Run(string(typ)+"/"+c.name, func(t *testing.T) {
				metric := `"cosine"`
				if typ == types.ATTR_VEC_DISTANCE {
					metric = `"euclidean"`
				}
				raw := json.RawMessage(`{"metric":` + metric + `,"reference":` + c.ref + `}`)
				cfg, err := ComparisonParams("attributes[0]", typ, raw, members, testInputSchema())
				if c.reason != "" {
					if err == nil || err.Code != errors.SERVICE_VALIDATION || err.Details["reason"] != c.reason {
						t.Fatalf("got %v (%+v), want SERVICE_VALIDATION %s", err, cfg.Reference, c.reason)
					}
					return
				}
				if err != nil {
					t.Fatalf("refused: %s", err.Message)
				}
				ref := cfg.Reference
				if ref.Form != c.form || len(ref.Filter) != c.filter || ref.By != c.by || ref.Population != c.pop || ref.Static() != c.static {
					t.Fatalf("reference %+v (static %v), want form %s filter %d by %q population %q static %v", ref, ref.Static(), c.form, c.filter, c.by, c.pop, c.static)
				}
				if ref.KeyColumn() != c.by {
					t.Errorf("KeyColumn %q, want %q", ref.KeyColumn(), c.by)
				}
				back, berr := ComparisonParams("attributes[0]", typ, cfg.CanonicalParams(), Resolved{Members: members.Members, Labels: members.Members}, testInputSchema())
				if berr != nil {
					t.Fatalf("canonical %s refused: %s", cfg.CanonicalParams(), berr.Message)
				}
				want := ref
				if want.Form == ReferenceLabelMap {
					want.Form = ReferenceValues
				}
				if !reflect.DeepEqual(back.Reference, want) || !reflect.DeepEqual(back.Labels, cfg.Labels) {
					t.Errorf("canonical %s decodes to %+v, want %+v", cfg.CanonicalParams(), back.Reference, want)
				}
				// The table judgement binds nothing for these forms.
				if c.form != ReferenceLabelMap {
					if b, terr := ComparisonTableIssue("attributes[0]", cfg, nil); terr != nil || b.Static != nil {
						t.Errorf("table issue %v / %+v, want none", terr, b)
					}
				}
			})
		}
	}
}

// TestComparisonCentroid_Routing pins the per-slot readers the routing
// sites share: a centroid is two-pass for either type and any metric —
// even beside a bound fit, which it un-binds (FitBound) — its filter
// rides AttributeFitOn after fit_on and its by is the projected key
// column; a population is neither two-pass nor a key column.
func TestComparisonCentroid_Routing(t *testing.T) {
	filter := `[{"type":"FILTER_RANGE","field":"a1","params":{"min":0}}]`
	cases := []struct {
		name     string
		attr     *types.Attribute
		twoPass  bool
		bound    bool
		centroid bool
		key      string
		pop      string
		filters  int
	}{
		{name: "similarity centroid", attr: cmpAttr(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"centroid":{"filter":`+filter+`}}}`, func(*types.Attribute) {}),
			twoPass: true, centroid: true, filters: 1},
		{name: "distance centroid by", attr: cmpAttr(types.ATTR_VEC_DISTANCE, `{"metric":"euclidean","reference":{"centroid":{"by":"region"}}}`, func(*types.Attribute) {}),
			twoPass: true, centroid: true, key: "region"},
		{name: "fitted metric centroid, fit_on then filter", attr: cmpAttr(types.ATTR_VEC_DISTANCE, `{"metric":"mahalanobis","fit_on":[{"type":"FILTER_RANGE","field":"b1","params":{"min":0}}],"reference":{"centroid":{"filter":`+filter+`}}}`, func(*types.Attribute) {}),
			twoPass: true, centroid: true, filters: 2},
		{name: "bound fit beside a centroid stays two-pass", attr: cmpAttr(types.ATTR_VEC_DISTANCE, `{"metric":"mahalanobis","fit":"pop","reference":{"centroid":{}}}`, func(*types.Attribute) {}),
			twoPass: true, centroid: true},
		{name: "bound fit, literal reference is row-local", attr: cmpAttr(types.ATTR_VEC_DISTANCE, `{"metric":"mahalanobis","fit":"pop","reference":[1,2]}`, func(*types.Attribute) {}),
			bound: true},
		{name: "population row-local metric", attr: cmpAttr(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"population":"pop"}}`, func(*types.Attribute) {}),
			pop: "pop"},
		{name: "population fitted metric fits", attr: cmpAttr(types.ATTR_VEC_DISTANCE, `{"metric":"std_euclidean","reference":{"population":"pop"}}`, func(*types.Attribute) {}),
			twoPass: true, pop: "pop"},
		{name: "a label named centroid is a label map", attr: cmpAttr(types.ATTR_VEC_DISTANCE, `{"metric":"euclidean","reference":{"centroid":1,"y":2}}`, func(*types.Attribute) {})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := c.attr
			if got := ComparisonTwoPass(a.Type, a, nil, nil); got != c.twoPass {
				t.Errorf("ComparisonTwoPass = %v, want %v", got, c.twoPass)
			}
			if got := FitBound(a.Type, a); got != c.bound {
				t.Errorf("FitBound = %v, want %v", got, c.bound)
			}
			if got := ComparisonCentroid(a.Type, a); got != c.centroid {
				t.Errorf("ComparisonCentroid = %v, want %v", got, c.centroid)
			}
			if got := ComparisonKeyField(a.Type, a); got != c.key {
				t.Errorf("ComparisonKeyField = %q, want %q", got, c.key)
			}
			if got := ComparisonPopulation(a.Type, a); got != c.pop {
				t.Errorf("ComparisonPopulation = %q, want %q", got, c.pop)
			}
			fs := AttributeFitOn(a.Type, a)
			if len(fs) != c.filters {
				t.Fatalf("AttributeFitOn = %d filterers, want %d", len(fs), c.filters)
			}
			if c.filters == 2 && (fs[0].Field != "b1" || fs[1].Field != "a1") {
				t.Errorf("AttributeFitOn order %s, %s; want fit_on (b1) then the centroid filter (a1)", fs[0].Field, fs[1].Field)
			}
		})
	}
	// Projection decodes a centroid's by beside the members, and its
	// filter's columns ride the expression slots under their own path.
	req := &types.Request{Attributes: []*types.Attribute{
		{Type: types.ATTR_VEC_DISTANCE, Fields: []string{"b1", "b2"}, Params: json.RawMessage(`{"metric":"euclidean","reference":{"centroid":{"by":"region","filter":[{"type":"FILTER_EXPRESSION","expression":"a1 > 0"}]}}}`)},
	}}
	members, ok := AttributeInputMembers(req, testInputSchema())
	if !ok || !reflect.DeepEqual(members, []string{"region", "b1", "b2"}) && !reflect.DeepEqual(members, []string{"b1", "b2", "region"}) {
		t.Errorf("projection members %v (%v), want the members and the by column", members, ok)
	}
	slots := ExprSlots(req)
	if len(slots) != 1 || slots[0].Path != "attributes[0].params.reference.centroid.filter[0].expression" {
		t.Errorf("expression slots %+v, want the centroid filter's", slots)
	}
}

// TestPopulationRefusal pins the population lookup refusals both sides
// raise: an unbound name becomes PULSE_VECTOR_REFERENCE_UNKNOWN
// population_unknown listing the bound fits; a kind mismatch keeps its
// code; labels that are not the slot's are _MISMATCH naming the
// population; a good population aligns by label.
func TestPopulationRefusal(t *testing.T) {
	unknown := errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_UNKNOWN, "no fit", map[string]any{"name": "p", "available": []string{"a", "b"}})
	ce := PopulationRefusal("s", "p", unknown)
	if ce.Code != errors.PULSE_VECTOR_REFERENCE_UNKNOWN || ce.Details["reason"] != ReferenceReasonPopulationUnknown ||
		!reflect.DeepEqual(ce.Details["valid"], []string{"a", "b"}) || ce.Details["population"] != "p" || ce.Details["attribute"] != "s" ||
		ce.Details["slot"] != "s.params.reference" {
		t.Errorf("unknown = %s %+v", ce.Code, ce.Details)
	}
	kind := errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_KIND_MISMATCH, "regression", map[string]any{"name": "p"})
	if ce := PopulationRefusal("s", "p", kind); ce.Code != errors.PULSE_MODEL_KIND_MISMATCH || ce.Details["population"] != "p" {
		t.Errorf("kind = %s %+v", ce.Code, ce.Details)
	}
	if _, ce := PopulationAlign("s", "p", []string{"x", "z"}, []string{"x", "y"}); ce == nil || ce.Code != errors.PULSE_VECTOR_REFERENCE_MISMATCH ||
		ce.Details["reason"] != ReferenceReasonLabelsDiffer || ce.Details["population"] != "p" {
		t.Errorf("mismatch = %v", ce)
	}
	perm, ce2 := PopulationAlign("s", "p", []string{"y", "x"}, []string{"x", "y"})
	if ce2 != nil || !reflect.DeepEqual(perm, []int{1, 0}) {
		t.Errorf("align = %v %v, want [1 0]", perm, ce2)
	}
}
