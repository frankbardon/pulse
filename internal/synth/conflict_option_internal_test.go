package synth

import (
	"strings"
	"testing"
)

// setPairSpecForClaim is a two-set fixture: `a` drives one option of `b`
// through a set-set pair, and `rules` is whatever the caller wants
// arbitrated against it.
func setPairSpecForClaim(rules []RuleSpec) *Spec {
	setField := func(name string) FieldSpec {
		return FieldSpec{
			Name: name, Type: "set_u8", Distribution: DistSetBernoulli,
			Params: map[string]any{
				"options":     []any{"x", "y"},
				"frequencies": []any{0.5, 0.5},
			},
		}
	}
	return &Spec{
		RowCount: 10,
		Fields:   []FieldSpec{setField("a"), setField("b")},
		Rules:    rules,
		SetSetPairs: []SetSetPairSpec{{
			SetA: "a", OptionA: "x", SetB: "b", OptionB: "x",
			Cells: []CategoricalPairCellSpec{{AValue: "selected", BValue: "selected", Count: 10}},
		}},
	}
}

// TestClaimTarget_OptionGranularity pins BOTH directions of the
// containment between the two levels of the claim space, in one test,
// because each is trivially satisfiable by abandoning the other — a
// lookup that always falls back to the field satisfies the first and
// breaks the second, and a plain map lookup does the reverse.
//
// Direction 1 (the one that was WRONG, and measurably so): an
// unconditional rule writing a whole set_* field must subsume every
// option of it. Without that, the per-option pair stage kept running on
// every row and had its result discarded wholesale by the rule pass —
// the wasted stage AND the false report the priority-0 pre-claim exists
// to remove, with nothing anywhere saying so.
//
// Direction 2: an option-level claim must NOT reach the whole field. A
// pair driving one option of a multi-select has said nothing about the
// field's other options, so letting it evict a whole-field claimant
// would drop structure nothing contests.
func TestClaimTarget_OptionGranularity(t *testing.T) {
	t.Run("a whole-field rule claim subsumes the field's options", func(t *testing.T) {
		spec := setPairSpecForClaim([]RuleSpec{{Set: map[string]any{"b": []any{"x"}}}})
		res := resolveConflicts(spec)
		if len(res.setSetPairs) != 0 {
			t.Errorf("set-set pair over b.x survived a rule that writes the WHOLE of b on every row: " +
				"the stage runs and its result is discarded, and the fidelity report scores it")
		}
		if len(res.warnings) != 1 || !strings.Contains(res.warnings[0], "structural rule 0 (set)") {
			t.Errorf("the dropped pair must name the rule that took it, got %v", res.warnings)
		}
		if !strings.Contains(strings.Join(res.warnings, " "), `option "x"`) {
			t.Errorf("the warning must name the OPTION that lost, got %v", res.warnings)
		}
	})

	t.Run("a set_expr rule claim subsumes the same way", func(t *testing.T) {
		// Same containment through the other claiming slot, so the two
		// cannot drift.
		spec := setPairSpecForClaim([]RuleSpec{{SetExpr: map[string]string{"b": `["x"]`}}})
		res := resolveConflicts(spec)
		if len(res.setSetPairs) != 0 {
			t.Errorf("set-set pair survived an unconditional set_expr over the whole field")
		}
	})

	t.Run("a CONDITIONAL rule claims nothing, so the pair survives", func(t *testing.T) {
		// The claim's own first exclusion, restated here because it is
		// what stops direction 1 from being over-broad: a rule with a
		// `when` writes only some rows, so the pair is exactly what
		// produces the option's value on the rest.
		spec := setPairSpecForClaim([]RuleSpec{{
			When: `a["y"]`, Set: map[string]any{"b": []any{"x"}},
		}})
		res := resolveConflicts(spec)
		if len(res.setSetPairs) != 1 {
			t.Errorf("a conditional rule took the option away from the pair that drives it on every other row")
		}
	})

	t.Run("an option-level claim does not reach the whole field", func(t *testing.T) {
		// Two pairs driving two DIFFERENT options of one multi-select
		// contest nothing and must both survive; if an option claim
		// reached the field, the second would be dropped.
		spec := setPairSpecForClaim(nil)
		spec.SetSetPairs = append(spec.SetSetPairs, SetSetPairSpec{
			SetA: "a", OptionA: "y", SetB: "b", OptionB: "y",
			Cells: []CategoricalPairCellSpec{{AValue: "selected", BValue: "selected", Count: 10}},
		})
		res := resolveConflicts(spec)
		if len(res.setSetPairs) != 2 {
			t.Errorf("two pairs driving two different options of one set field got arbitrated against "+
				"each other: %d survived, want 2; warnings %v", len(res.setSetPairs), res.warnings)
		}
		if len(res.warnings) != 0 {
			t.Errorf("unexpected warnings: %v", res.warnings)
		}
	})

	t.Run("two pairs contesting the SAME option still arbitrate", func(t *testing.T) {
		spec := setPairSpecForClaim(nil)
		spec.SetSetPairs = append(spec.SetSetPairs, SetSetPairSpec{
			SetA: "a", OptionA: "y", SetB: "b", OptionB: "x",
			Cells: []CategoricalPairCellSpec{{AValue: "selected", BValue: "selected", Count: 10}},
		})
		res := resolveConflicts(spec)
		if len(res.setSetPairs) != 1 {
			t.Errorf("two pairs writing b.x must arbitrate: %d survived, want 1", len(res.setSetPairs))
		}
		if len(res.warnings) != 1 {
			t.Errorf("the loss must be reported exactly once, got %v", res.warnings)
		}
	})
}

// TestRuleClaims_NeverEmitsAnOptionTarget is the forward half of the
// same contract, aimed at the slot that does not exist yet.
//
// Spec.Rules cannot write a single set_* option today, so every claim it
// emits is whole-field. When a per-option slot lands it MUST key on
// {field, option}: keyed on {field} it would evict every pair driving the
// field's OTHER options, silently — they would stop running and the file
// would keep a plausible marginal. This test fails the moment ruleClaims
// starts producing option targets, which is the point at which that
// decision has to be made rather than inherited.
func TestRuleClaims_NeverEmitsAnOptionTarget(t *testing.T) {
	claims := ruleClaims([]RuleSpec{
		{Set: map[string]any{"b": []any{"x"}, "n": 1.0}},
		{SetExpr: map[string]string{"c": "1"}},
	})
	if len(claims) == 0 {
		t.Fatal("fixture claims nothing")
	}
	for _, c := range claims {
		if c.target.option != "" {
			t.Errorf("ruleClaims emitted an option-keyed target %v. If a per-option rule slot has landed, "+
				"confirm it keys on {field, option} (not {field}), extend "+
				"TestClaimTarget_OptionGranularity with its arm, and update this test", c.target)
		}
	}
}
