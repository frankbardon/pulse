package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSlotWeight_JSONRoundTrip pins the three wire states of a per-slot
// weight and that each survives decode → encode: absent (omitted),
// null (opt out) and set (string or {field, kind} object).
func TestSlotWeight_JSONRoundTrip(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantOut  string
		zero     bool
		null     bool
		field    string
		kind     WeightKind
		effKind  WeightKind
		hasSpec  bool
		hasField bool
	}{
		{name: "absent", in: `{"type":"AGG_SUM","field":"x"}`, wantOut: `{"type":"AGG_SUM","field":"x"}`, zero: true},
		{name: "null", in: `{"type":"AGG_SUM","field":"x","weight":null}`, wantOut: `{"type":"AGG_SUM","field":"x","weight":null}`, null: true},
		{name: "string", in: `{"type":"AGG_SUM","field":"x","weight":"w"}`, wantOut: `{"type":"AGG_SUM","field":"x","weight":"w"}`, hasSpec: true, field: "w", effKind: WeightKindProbability},
		{name: "object", in: `{"type":"AGG_SUM","field":"x","weight":{"field":"w","kind":"frequency"}}`, wantOut: `{"type":"AGG_SUM","field":"x","weight":{"field":"w","kind":"frequency"}}`, hasSpec: true, field: "w", kind: WeightKindFrequency, effKind: WeightKindFrequency},
		{name: "object no kind", in: `{"type":"AGG_SUM","field":"x","weight":{"field":"w"}}`, wantOut: `{"type":"AGG_SUM","field":"x","weight":"w"}`, hasSpec: true, field: "w", effKind: WeightKindProbability},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var a Aggregation
			if err := json.Unmarshal([]byte(tc.in), &a); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if a.Weight.IsZero() != tc.zero {
				t.Errorf("IsZero = %v, want %v", a.Weight.IsZero(), tc.zero)
			}
			if a.Weight.IsNull() != tc.null {
				t.Errorf("IsNull = %v, want %v", a.Weight.IsNull(), tc.null)
			}
			spec := a.Weight.Spec()
			if (spec != nil) != tc.hasSpec {
				t.Fatalf("Spec = %v, want present=%v", spec, tc.hasSpec)
			}
			if spec != nil {
				if spec.Field != tc.field || spec.Kind != tc.kind || spec.EffectiveKind() != tc.effKind {
					t.Errorf("Spec = %+v (eff %s), want field %q kind %q eff %q", *spec, spec.EffectiveKind(), tc.field, tc.kind, tc.effKind)
				}
			}
			out, err := json.Marshal(&a)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(out) != tc.wantOut {
				t.Errorf("marshal = %s, want %s", out, tc.wantOut)
			}
		})
	}
}

// TestSlotWeight_RejectsOtherShapes: a number, a bool, an array, or an
// object with an unknown key is a decode error — never a silent
// absent weight.
func TestSlotWeight_RejectsOtherShapes(t *testing.T) {
	for _, in := range []string{`1`, `true`, `["w"]`, `{"field":"w","kind":"frequency","extra":1}`} {
		var w SlotWeight
		if err := json.Unmarshal([]byte(in), &w); err == nil {
			t.Errorf("Unmarshal(%s): want error, got %+v", in, w)
		}
	}
}

// TestSlotWeight_GoConstructors: the Go spellings build exactly the
// states the JSON forms decode into.
func TestSlotWeight_GoConstructors(t *testing.T) {
	if !(SlotWeight{}).IsZero() {
		t.Error("zero SlotWeight must be absent")
	}
	if n := NullSlotWeight(); n.IsZero() || !n.IsNull() || n.Spec() != nil {
		t.Errorf("NullSlotWeight = %+v", n)
	}
	f := SlotWeightField("w")
	if f.IsZero() || f.IsNull() || f.Spec() == nil || f.Spec().Field != "w" {
		t.Errorf("SlotWeightField = %+v", f)
	}
	o := SlotWeightOf(WeightSpec{Field: "w", Kind: WeightKindFrequency})
	if s := o.Spec(); s == nil || s.Kind != WeightKindFrequency {
		t.Errorf("SlotWeightOf = %+v", o)
	}
	// Spec returns a copy: mutating it never changes the slot.
	o.Spec().Field = "mutated"
	if o.Spec().Field != "w" {
		t.Error("Spec must return a copy")
	}
}

// TestRequestWeight_AbsentByteIdentical: a request carrying no weight
// anywhere marshals and hashes exactly as before the weight slots
// existed (the captured baseline is TestCanonicalHash_RequestLabelEmpty
// ByteIdentical's), while null and set slot weights each change the
// hash — null is not absent.
func TestRequestWeight_AbsentByteIdentical(t *testing.T) {
	const captured = "a4e259f7ed18dcbcb3e78b76066bcbee"
	mk := func(w SlotWeight) *Request {
		return &Request{
			Cohort:       &Cohort{Filename: "a.pulse"},
			Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x", Weight: w}},
		}
	}
	absent := mk(SlotWeight{})
	if got := absent.Hash(); got != captured {
		t.Fatalf("absent weight drifted from the captured baseline: got %q want %q", got, captured)
	}
	b, _ := json.Marshal(absent)
	if strings.Contains(string(b), "weight") {
		t.Fatalf("absent weight must not marshal: %s", b)
	}
	null, set := mk(NullSlotWeight()).Hash(), mk(SlotWeightField("w")).Hash()
	if null == captured || set == captured || null == set {
		t.Fatalf("null (%s), set (%s) and absent (%s) must hash apart", null, set, captured)
	}
	reqLevel := mk(SlotWeight{})
	reqLevel.Weight = &WeightSpec{Field: "w"}
	if reqLevel.Hash() == captured {
		t.Fatal("a request-level weight must change the hash")
	}

	// Round trip through JSON keeps null distinct from absent.
	var back Request
	nb, _ := json.Marshal(mk(NullSlotWeight()))
	if err := json.Unmarshal(nb, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Aggregations[0].Weight.IsNull() || back.Hash() != null {
		t.Fatalf("null weight lost in round trip: %s", nb)
	}
}

// TestSlotWeight_EverySlotFamily: every weight-bearing slot decodes a
// `weight` key — crosstab cell and margin aggregations (Aggregation),
// tests, regressions, attributes, overlays — and the request root its
// WeightSpec.
func TestSlotWeight_EverySlotFamily(t *testing.T) {
	body := `{
		"weight": {"field": "w", "kind": "frequency"},
		"aggregations": [{"type":"AGG_SUM","field":"x","weight":null}],
		"tests": [{"type":"TEST_T","field":"x","weight":null}],
		"post_tests": [{"type":"TEST_T","field":"x","weight":"w"}],
		"regressions": [{"type":"REG_OLS","target":"x","weight":null}],
		"attributes": [{"type":"ATTR_ZSCORE","field":"x","weight":null}],
		"overlays": [{"kind":"OVERLAY_INDEX_VS_MARGIN","scope":"CELL","ref":{},"weight":null}],
		"crosstab": {"rows":[],"columns":[],"cell":{"type":"AGG_COUNT","field":"x","weight":"w"},
			"margin_aggregations":[{"type":"AGG_COUNT","field":"x","weight":null}]}
	}`
	var r Request
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if r.Weight == nil || r.Weight.Field != "w" || r.Weight.Kind != WeightKindFrequency {
		t.Errorf("request weight = %+v", r.Weight)
	}
	for name, w := range map[string]SlotWeight{
		"aggregation": r.Aggregations[0].Weight,
		"test":        r.Tests[0].Weight,
		"regression":  r.Regressions[0].Weight,
		"attribute":   r.Attributes[0].Weight,
		"overlay":     r.Overlays[0].Weight,
		"margin":      r.Crosstab.MarginAggregations[0].Weight,
	} {
		if !w.IsNull() {
			t.Errorf("%s weight: want null, got %+v", name, w)
		}
	}
	for name, w := range map[string]SlotWeight{"post_test": r.PostTests[0].Weight, "cell": r.Crosstab.Cell.Weight} {
		if s := w.Spec(); s == nil || s.Field != "w" {
			t.Errorf("%s weight: want field w, got %+v", name, w)
		}
	}
}
