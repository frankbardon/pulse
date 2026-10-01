package synth

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestValidateRules_SetNullNonNullableIsRefused is FU-05: the fault that
// was a warning is now a refusal, at spec parse, naming the rule index,
// the slot and the field.
//
// The rule FIRES and the file cannot show it — encodeRow writes a null
// bit for nullable fields only — so the outcome is a plausible cohort
// with the gate silently absent, which is the class this whole layer
// exists to remove. It is knowable from FieldSpec.Nullable before a row
// exists, and the warning it replaces was capped at three examples per
// kind in the terminal summary.
func TestValidateRules_SetNullNonNullableIsRefused(t *testing.T) {
	err := validateSpec(ruleSpecFixture(
		RuleSpec{Set: map[string]any{"score": 1.0}},
		RuleSpec{When: "nps > 1", SetNull: []string{"aware"}},
	))
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("want a refusal at spec parse, got %v", err)
	}
	if coded.Code != errors.PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE {
		t.Fatalf("code = %s, want %s", coded.Code, errors.PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE)
	}
	if got := coded.Details[errors.DetailSynthRule]; got != 1 {
		t.Errorf("details[%q] = %v, want 1", errors.DetailSynthRule, got)
	}
	if got := coded.Details[errors.DetailSynthRuleSlot]; got != "set_null" {
		t.Errorf("details[%q] = %v, want \"set_null\"", errors.DetailSynthRuleSlot, got)
	}
	if got := coded.Details["field"]; got != "aware" {
		t.Errorf("details[\"field\"] = %v, want \"aware\"", got)
	}
	if !strings.Contains(coded.Message, "nullable") {
		t.Errorf("message should name the remedy: %q", coded.Message)
	}

	// The remedy is on the FIELD, and it is the whole remedy: the same
	// rule over a nullable field validates.
	spec := ruleSpecFixture(RuleSpec{When: "nps > 1", SetNull: []string{"aware"}})
	for i := range spec.Fields {
		if spec.Fields[i].Name == "aware" {
			spec.Fields[i].Nullable = true
		}
	}
	if err := validateSpec(spec); err != nil {
		t.Fatalf("declaring the field nullable must be enough: %v", err)
	}
}

// TestNullTogetherWarnings_GateIsNotReported is FU-08. The gate is the
// field the block copies FROM, so applyNullTogether never writes its
// mask and a non-nullable gate is not a field the block failed to null.
//
// Both halves matter and each is trivially satisfiable by abandoning the
// other: the gate must stop being reported AND a non-gate member the
// block can actually reach must still be.
func TestNullTogetherWarnings_GateIsNotReported(t *testing.T) {
	byName := map[string]FieldSpec{
		"gate": {Name: "gate", Type: "packed_bool"},
		"a":    {Name: "a", Type: "u4", Nullable: true},
		"b":    {Name: "b", Type: "u4"},
	}
	got := nullTogetherWarnings(3, []string{"gate", "a", "b"}, byName)
	for _, w := range got {
		if strings.Contains(w, `"gate"`) && strings.Contains(w, "non-nullable") {
			t.Fatalf("the block's gate is never nulled by the copy; warning = %q", w)
		}
	}
	found := false
	for _, w := range got {
		if strings.Contains(w, `null_together names non-nullable field "b"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("a non-gate member the copy CAN null must still be reported; warnings = %v", got)
	}

	// A block whose gate is the only non-nullable field reports nothing
	// about nullability at all — that is the gated-block idiom, and the
	// diagnostic must be silent on it.
	byName["b"] = FieldSpec{Name: "b", Type: "u4", Nullable: true}
	for _, w := range nullTogetherWarnings(3, []string{"gate", "a", "b"}, byName) {
		if strings.Contains(w, "non-nullable") {
			t.Fatalf("idiomatic never-null gate still warns: %q", w)
		}
	}
}

// TestSuggestRules_CandidatesSurviveTheNullabilityRefusal closes the loop
// FU-05's promotion could have broken, on the path the feature is FOR: a
// `--suggest-rules` candidate is a `set_null` over fields the detector
// found because they ARE null under the gate, and SpecFromProfile derives
// Nullable from the captured null rate — so the two agree by
// construction. Asserted rather than reasoned, because if they ever
// disagree the whole detection loop (`profile create --suggest-rules`
// then `synth from-profile --rules`) stops at a refusal, and the
// preceding tests build their specs by hand and cannot see it.
func TestSuggestRules_CandidatesSurviveTheNullabilityRefusal(t *testing.T) {
	prof := gateFixtureProfile(t, 400, 40)
	if len(prof.RuleCandidates) == 0 {
		t.Fatalf("fixture must produce candidates; warnings = %v", prof.Warnings)
	}
	spec, _ := SpecFromProfile(prof, 100)
	if spec == nil {
		t.Fatal("SpecFromProfile returned no spec")
	}
	sawSetNull := false
	for _, c := range prof.RuleCandidates {
		if len(c.SetNull) > 0 {
			sawSetNull = true
		}
	}
	if !sawSetNull {
		t.Fatal("fixture must produce at least one set_null candidate for this to mean anything")
	}
	spec.Rules = prof.RuleCandidates
	if err := validateRules(spec); err != nil {
		t.Fatalf("a detected candidate does not survive validation against the spec "+
			"the profile derives: %v", err)
	}
}
