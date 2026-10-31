package synth

import (
	"math"
	"reflect"
	"testing"
)

// TestSetOptionEffectSize_IsPhiOfTheTwoByTwo: V of a 2x2 table is
// |ad − bc| / sqrt(r1·r2·c1·c2); a constant option has none.
func TestSetOptionEffectSize_IsPhiOfTheTwoByTwo(t *testing.T) {
	t.Parallel()

	// source 30/100 selected, synthetic 60/100: a=30 b=70 c=60 d=40.
	want := math.Abs(30.0*40-70.0*60) / math.Sqrt(100.0*100*90*110)
	es := setOptionEffectSize(30, 100, 60, 100)
	if es == nil || math.Abs(es.Value-want) > 1e-12 || es.Measure != EffectMeasureCramersV || es.Magnitude != "medium" {
		t.Fatalf("V = %+v, want %v (medium)", es, want)
	}
	if setOptionEffectSize(100, 100, 50, 50) != nil {
		t.Fatal("an option selected on every row produced a V")
	}
	if setOptionEffectSize(3, 10, 0, 0) != nil {
		t.Fatal("an empty partition produced a V")
	}
}

// TestHistogramTVD_KnownValues: identical shapes are 0, disjoint ones 1,
// and each side is normalised by its own total.
func TestHistogramTVD_KnownValues(t *testing.T) {
	t.Parallel()

	if got := histogramTVD([]int{1, 1}, []int{5, 5}, 2, 10); got != 0 {
		t.Errorf("same shape TVD = %v, want 0", got)
	}
	if got := histogramTVD([]int{4, 0}, []int{0, 7}, 4, 7); got != 1 {
		t.Errorf("disjoint TVD = %v, want 1", got)
	}
	if got := histogramTVD([]int{3, 1}, []int{1, 1}, 4, 2); math.Abs(got-0.25) > 1e-15 {
		t.Errorf("TVD = %v, want 0.25", got)
	}
}

// TestRuleWriters_DeclarationOrderSlotsAndBlockLeader: every writing
// slot counts, in declaration then slot order; a null_together block's
// first member is read, not written; a field written twice by one rule
// slot is listed once.
func TestRuleWriters_DeclarationOrderSlotsAndBlockLeader(t *testing.T) {
	t.Parallel()

	got := ruleWriters([]RuleSpec{
		{When: "a > 1", Set: map[string]any{"b": 1.0, "a": 2.0}, SetNull: []string{"c"}},
		{SetExpr: map[string]string{"b": "a"}, NullTogether: []string{"a", "c", "d"}},
		{SetNull: []string{"c", "c"}},
	})
	want := map[string][]string{
		"a": {"structural rule 0 (set)"},
		"b": {"structural rule 0 (set)", "structural rule 1 (set_expr)"},
		"c": {"structural rule 0 (set_null)", "structural rule 1 (null_together)", "structural rule 2 (set_null)"},
		"d": {"structural rule 1 (null_together)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ruleWriters = %v, want %v", got, want)
	}
}

// TestAttachRuleNotes_RulesFreeSpecTouchesNothing: no rules, no note,
// whatever the magnitude.
func TestAttachRuleNotes_RulesFreeSpecTouchesNothing(t *testing.T) {
	t.Parallel()

	r := &FidelityReport{Fields: []*FieldFidelity{{Field: "x", EffectSize: &FieldEffectSize{Magnitude: "large"}}}}
	AttachRuleNotes(r, &Spec{})
	if r.Fields[0].RuleNote != "" {
		t.Fatalf("rules-free spec wrote a note: %q", r.Fields[0].RuleNote)
	}
	AttachRuleNotes(r, &Spec{Rules: []RuleSpec{{Set: map[string]any{"x": 1.0}}}})
	if r.Fields[0].RuleNote == "" {
		t.Fatal("a large flag on a rule-written field got no note")
	}
}
