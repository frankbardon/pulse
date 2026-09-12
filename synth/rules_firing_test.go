package synth_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// TestRules_ZeroFiringRuleIsReportedAndOthersAreNot is the story's
// central end-to-end assertion, and all three claims are made over ONE
// generated file because each is trivially satisfiable by abandoning the
// others: a run that warns about every rule satisfies the zero case, and
// a run that warns about none satisfies the other two.
//
//  1. A rule whose `when` is never true is reported, naming its index
//     and its predicate.
//  2. A rule firing on SOME rows is not reported.
//  3. A rule firing on EVERY row is not reported.
func TestRules_ZeroFiringRuleIsReportedAndOthersAreNot(t *testing.T) {
	spec := ruleModelSpec(400, []synth.RuleSpec{
		// Fires on roughly half the rows: `aware` is bernoulli(0.5).
		{When: "aware == 0", SetNull: []string{"spend"}},
		// Never fires: packed_bool carries 0 or 1, never 2.
		{When: "aware == 2", Set: map[string]any{"score": 3.0}},
		// No `when` at all — every row.
		{Set: map[string]any{"region": "east"}},
	})
	_, res, err := synth.SynthBytes(spec, synth.Options{Seed: 9})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}

	var fired []string
	for _, w := range res.Warnings {
		if strings.Contains(w, "never fired") {
			fired = append(fired, w)
		}
	}
	if len(fired) != 1 {
		t.Fatalf("never-fired warnings = %v, want exactly one (rule 1)", fired)
	}
	if !strings.HasPrefix(fired[0], "rule 1 never fired") {
		t.Errorf("warning does not name the rule's index: %q", fired[0])
	}
	if !strings.Contains(fired[0], `"aware == 2"`) {
		t.Errorf("warning does not name the rule's when: %q", fired[0])
	}
	if !strings.Contains(fired[0], "0 of 400 generated row(s)") {
		t.Errorf("warning does not reconcile against the run's row count: %q", fired[0])
	}

	groups := synth.GroupWarnings(res.Warnings)
	if len(groups) == 0 || groups[0].Kind != "rule never fired" || !groups[0].Attention {
		t.Errorf("groups[0] = %+v, want the never-fired kind first and needing attention", groups[0])
	}
}

// TestRules_FiringCountDoesNotMoveAGeneratedByte is the determinism half.
//
// Counting must be observation only: no RNG consumed, no stage order
// moved. A never-firing rule writes nothing, so a spec carrying one must
// generate the SAME bytes as the same spec without it — which is a
// stronger statement than "two runs of the same spec agree" and the one
// that fails if the counter ever reaches the row.
func TestRules_FiringCountDoesNotMoveAGeneratedByte(t *testing.T) {
	withRule, res, err := synth.SynthBytes(
		ruleModelSpec(300, []synth.RuleSpec{{When: "aware == 2", SetNull: []string{"spend"}}}),
		synth.Options{Seed: 17})
	if err != nil {
		t.Fatalf("with rule: %v", err)
	}
	withoutRule, _, err := synth.SynthBytes(ruleModelSpec(300, nil), synth.Options{Seed: 17})
	if err != nil {
		t.Fatalf("without rule: %v", err)
	}
	if !bytes.Equal(withRule, withoutRule) {
		t.Fatalf("a rule that never fires moved %d byte(s); the counter must not reach the row",
			byteDiffCount(withRule, withoutRule))
	}
	// And the run still SAID so — otherwise this test is satisfied by
	// deleting the feature.
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "rule 0 never fired") {
		t.Fatalf("warnings = %v, want the single never-fired report", res.Warnings)
	}
}

// TestRules_ConstraintRejectedRowsAreNotFirings pins the count's
// denominator on the path that can actually diverge: a constraint
// rejects a drawn row, generate() re-draws it, and the rules that
// applied to the discarded draw left nothing in the file.
//
// The rule here fires ONLY on rows the constraint then rejects, so a
// counter incrementing in apply() would report it as applied while the
// cohort contains no row it ever touched.
func TestRules_ConstraintRejectedRowsAreNotFirings(t *testing.T) {
	spec := ruleModelSpec(200, []synth.RuleSpec{{
		When: "region == \"west\"", Set: map[string]any{"score": 3.0},
	}})
	// Skew `region` so the rejected minority stays under the rejection-rate
	// threshold, then keep only eastern rows: every row the rule fires on
	// is rejected and re-drawn.
	spec.Fields[2].Params["weights"] = []any{9.0, 1.0}
	spec.Constraints = []synth.ConstraintSpec{{Expr: `region == "east"`}}
	_, res, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("fixture drew no rejected rows; the case under test never happened")
	}
	var fired []string
	for _, w := range res.Warnings {
		if strings.Contains(w, "never fired") {
			fired = append(fired, w)
		}
	}
	if len(fired) != 1 {
		t.Fatalf("never-fired warnings = %v, want one: every firing was on a rejected row", fired)
	}
}
