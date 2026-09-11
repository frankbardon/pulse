package synth

import (
	"strings"
	"testing"
)

// firingFixture compiles the rules of a ruleSpecFixture-shaped spec into
// the applier the per-row pass actually runs, so the counter can be
// driven one row at a time without a whole generate().
func firingFixture(t *testing.T, rules ...RuleSpec) *ruleApplier {
	t.Helper()
	spec := ruleSpecFixture(rules...)
	_, wfs, err := buildSchema(spec)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	applier, _, err := compileRules(spec.Rules, wfs)
	if err != nil {
		t.Fatalf("compileRules: %v", err)
	}
	if applier == nil {
		t.Fatal("compileRules returned no applier for a spec declaring rules")
	}
	return applier
}

// firingRow is one row the counter can be driven over. `aware` is the
// only gate the cases below use.
func firingRow(aware float64) (map[string]any, map[string]bool) {
	return map[string]any{
			"id": 1.0, "nps": 9.0, "score": 0.5, "aware": aware,
			"region": "east", "freeform": "abc",
			"brands": map[string]bool{"acme": true}, "cents": 1.25,
		},
		map[string]bool{}
}

// TestRuleFirings_CountedPerAcceptedRow is the counter's contract.
//
// Three rules over the same rows, each a different answer to "did this
// rule do anything": one gated on a value every row carries, one gated
// on a value no row carries, one ungated. Asserting all three in ONE
// test is deliberate — a counter that counts nothing satisfies the zero
// case and a counter that counts every rule satisfies the always case.
func TestRuleFirings_CountedPerAcceptedRow(t *testing.T) {
	a := firingFixture(t,
		RuleSpec{When: "aware == 1", SetNull: []string{"score"}},
		RuleSpec{When: "aware == 2", SetNull: []string{"score"}},
		RuleSpec{Set: map[string]any{"score": 1.5}},
	)

	// Ten rows, six of them gated.
	for i := 0; i < 10; i++ {
		aware := 0.0
		if i < 6 {
			aware = 1
		}
		row, mask := firingRow(aware)
		if err := a.apply(row, mask); err != nil {
			t.Fatalf("apply row %d: %v", i, err)
		}
		a.commitRow()
	}

	got := a.firingCounts()
	if len(got) != 3 {
		t.Fatalf("firingCounts() = %d entries, want 3", len(got))
	}
	want := []int{6, 0, 10}
	for i, f := range got {
		if f.index != i {
			t.Errorf("firingCounts()[%d].index = %d, want %d", i, f.index, i)
		}
		if f.rows != want[i] {
			t.Errorf("rule %d fired on %d row(s), want %d", i, f.rows, want[i])
		}
	}
	if got[0].when != "aware == 1" {
		t.Errorf("firingCounts()[0].when = %q, want the predicate carried through", got[0].when)
	}

	ws := a.neverFiredWarnings(10)
	if len(ws) != 1 {
		t.Fatalf("neverFiredWarnings = %v, want exactly one (the rule nothing matched)", ws)
	}
	if !strings.Contains(ws[0], "rule 1 never fired") || !strings.Contains(ws[0], `"aware == 2"`) {
		t.Errorf("warning does not name the rule index and its when: %q", ws[0])
	}
	if !strings.Contains(ws[0], "0 of 10") {
		t.Errorf("warning does not reconcile against the generated row count: %q", ws[0])
	}
}

// TestRuleFirings_RejectedRowDoesNotCount pins WHICH rows the count is
// over: the rows that reached the file, not the draws that were made.
//
// generate() re-draws a whole row when a constraint rejects it, so a
// rule firing during a rejected attempt did nothing to the cohort. A
// count including those cannot be divided by the row count to get a
// rate, which is the one arithmetic a reader will do with it, and a rule
// firing ONLY on rejected rows would report as applied while the file
// shows no trace of it.
func TestRuleFirings_RejectedRowDoesNotCount(t *testing.T) {
	a := firingFixture(t, RuleSpec{When: "aware == 1", SetNull: []string{"score"}})

	for i := 0; i < 5; i++ {
		row, mask := firingRow(1)
		if err := a.apply(row, mask); err != nil {
			t.Fatalf("apply: %v", err)
		}
		// No commitRow: the constraint pass rejected this row.
	}
	if got := a.firingCounts()[0].rows; got != 0 {
		t.Fatalf("rejected rows counted as firings: %d, want 0", got)
	}

	row, mask := firingRow(1)
	if err := a.apply(row, mask); err != nil {
		t.Fatalf("apply: %v", err)
	}
	a.commitRow()
	if got := a.firingCounts()[0].rows; got != 1 {
		t.Fatalf("accepted row not counted: %d, want 1", got)
	}
	if ws := a.neverFiredWarnings(1); len(ws) != 0 {
		t.Fatalf("a rule that fired warns: %v", ws)
	}
}

// TestNeverFiredWarnings_BoundedWithACountedRemainder keeps this kind
// inside the volume discipline the rest of the package follows: a
// listing capped at maxNeverFiredRuleWarnings plus one counted roll-up,
// never one line per rule. A spec whose rules all reference a column
// that turned out constant produces one finding per rule, and the
// terminal summary is a screen every kind shares.
func TestNeverFiredWarnings_BoundedWithACountedRemainder(t *testing.T) {
	rules := make([]RuleSpec, 0, maxNeverFiredRuleWarnings+5)
	for i := 0; i < maxNeverFiredRuleWarnings+5; i++ {
		rules = append(rules, RuleSpec{When: "aware == 2", SetNull: []string{"score"}})
	}
	a := firingFixture(t, rules...)
	row, mask := firingRow(0)
	if err := a.apply(row, mask); err != nil {
		t.Fatalf("apply: %v", err)
	}
	a.commitRow()

	ws := a.neverFiredWarnings(1)
	if len(ws) != maxNeverFiredRuleWarnings+1 {
		t.Fatalf("neverFiredWarnings = %d lines, want %d named plus one roll-up",
			len(ws), maxNeverFiredRuleWarnings+1)
	}
	last := ws[len(ws)-1]
	if !strings.HasPrefix(last, "+5 further rule(s) never fired") {
		t.Errorf("roll-up line = %q, want a counted remainder naming the 5 suppressed rules", last)
	}
	// Both the named lines and the roll-up must land in the same group,
	// or the cap would split one finding across two counts.
	for _, w := range ws {
		kind, attention := classifyWarning(w)
		if kind != "rule never fired" || !attention {
			t.Errorf("classifyWarning(%q) = (%q, %v), want the attention-needing rule-never-fired kind", w, kind, attention)
		}
	}
}

// TestRuleNeverFiredWarning_NamesRoundNotInt pins the remedy the message
// recommends, because it is the only place the engine itself gives
// authoring advice and a wrong recommendation there is worse than none —
// a reader who follows it fixes the gate and silently corrupts the
// column the gate reads.
//
// The remedy is round(), not int(). An integer field is stored as
// floor(v+0.5) (writeFieldValueForField), so round(v) is the one
// expression that reproduces the stored value: the gate then selects
// exactly the rows the file shows and the column does not move. int(v)
// truncates and widens the gate by moving VALUES down instead — measured
// on the motivating 122-field profile at 20,000 rows, a `familiarity <= 1`
// gate fires on 1,843 rows raw, on 2,739 behind round() with the column
// unchanged, and on 3,824 behind int(), which gets there by dropping
// 1,085 respondents a point.
//
// The behavioural half of that claim is
// TestRulesCoherence_RoundNormalisesTheBandIntMovesTheScore (E2-S4); this
// case only guards the advice from drifting away from it.
func TestRuleNeverFiredWarning_NamesRoundNotInt(t *testing.T) {
	w := ruleNeverFiredWarning(0, "familiarity == 99", 20000)
	if !strings.Contains(w, "round(field)") {
		t.Errorf("the never-fired advice does not name round(): %q", w)
	}
	if strings.Contains(w, "int(field)") {
		t.Errorf("the never-fired advice still recommends int(), which widens the gate by rewriting "+
			"the stored value rather than by reading it correctly: %q", w)
	}
	// Guard the guard: the advice is only reachable on the `when` arm.
	if strings.Contains(ruleNeverFiredWarning(0, "", 20000), "round(field)") {
		t.Error("the no-`when` arm carries pre-rounding advice it has no use for")
	}
}
