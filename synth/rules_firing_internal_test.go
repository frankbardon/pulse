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

// TestRuleNeverFiredWarning_NamesTheCauseThatApplies is the diagnostic's
// contract, and all four arms are asserted in ONE table because each is
// trivially satisfiable by abandoning the others: the single message
// this replaced satisfied the pre-rounding row and got the other three
// wrong.
//
//  1. A rule that DID select rows a constraint then rejected blames the
//     CONSTRAINT, and says nothing about rounding — the arithmetic of
//     its predicate is not what removed the rows.
//  2. A rule comparing a continuously-reconstructed integer column gets
//     the pre-rounding remedy, and it is round(), not int(). An integer
//     field is stored as floor(v+0.5) (writeFieldValueForField), so
//     round(v) reproduces the value the FILE holds and the gate then
//     selects exactly the rows a reader sees. int(v) truncates and
//     widens the gate by moving VALUES down instead — measured on the
//     motivating 122-field profile at 20,000 rows, `familiarity <= 1`
//     fires on 1,843 rows raw, on 2,739 behind round() with the column
//     unchanged, and on 3,824 behind int(), which gets there by dropping
//     1,085 respondents a point. (The behavioural half of that claim is
//     TestRulesCoherence_RoundNormalisesTheBandIntMovesTheScore.)
//  3. A rule comparing a field whose row value ALREADY IS the stored
//     value — a `discrete` integer column, a `bernoulli` packed_bool —
//     must NOT get the pre-rounding remedy. Since the discrete and
//     bernoulli marginals shipped, that advice sends an author to
//     normalise a gate that is already exact while the real cause goes
//     unstated, so this row is the reason the split exists at all.
//  4. A rule reading no quantizing field at all gets neither remedy and
//     names what the predicate reads instead.
func TestRuleNeverFiredWarning_NamesTheCauseThatApplies(t *testing.T) {
	cases := []struct {
		name    string
		firing  ruleFiring
		want    []string
		notWant []string
	}{
		{
			name: "constraint rejected every row it selected",
			firing: ruleFiring{index: 3, when: "score > 4", attempted: true,
				reads: []whenField{{name: "score", typeName: "f64", dist: DistNormal}}},
			want:    []string{"rule 3 never fired", "constraint", "relax"},
			notWant: []string{"round(field)", "PRE-ROUNDING"},
		},
		{
			name: "continuous integer column is pre-rounding",
			firing: ruleFiring{index: 0, when: "familiarity == 1",
				reads: []whenField{{name: "familiarity", typeName: "u4", dist: DistNormal, preRounded: true}}},
			want:    []string{"PRE-ROUNDING", "round(field)", `"familiarity" (u4, normal)`},
			notWant: []string{"int(field)", "constraint"},
		},
		{
			name: "discrete integer column is exact, so pre-rounding is ruled out",
			firing: ruleFiring{index: 1, when: "nps == 99",
				reads: []whenField{{name: "nps", typeName: "u4", dist: DistDiscrete}}},
			want:    []string{`"nps" (u4, discrete)`, "pre-rounding is NOT the cause"},
			notWant: []string{"PRE-ROUNDING", "round(field)"},
		},
		{
			name: "bernoulli packed_bool is exact too",
			firing: ruleFiring{index: 2, when: "aware == 2",
				reads: []whenField{{name: "aware", typeName: "packed_bool", dist: DistBernoulli}}},
			want:    []string{`"aware" (packed_bool, bernoulli)`, "pre-rounding is NOT the cause"},
			notWant: []string{"round(field)"},
		},
		{
			name: "a categorical gate gets neither remedy",
			firing: ruleFiring{index: 4, when: `region == "noplace"`,
				reads: []whenField{{name: "region", typeName: "categorical_u8", dist: DistWeightedCategorical}}},
			want:    []string{`"region" (categorical_u8, weighted_categorical)`, "reconstruction"},
			notWant: []string{"round(field)", "pre-rounding is NOT the cause", "constraint"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ruleNeverFiredWarning(tc.firing, 20000)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("warning does not contain %q: %s", want, got)
				}
			}
			for _, bad := range tc.notWant {
				if strings.Contains(got, bad) {
					t.Errorf("warning wrongly contains %q: %s", bad, got)
				}
			}
			// Whatever the arm, the line must stay in its warning kind:
			// the classifier keys on the message shape, and an arm that
			// falls into the catch-all is reported as an unrecognised
			// warning rather than as a dead rule.
			if kind, attention := classifyWarning(got); kind != "rule never fired" || !attention {
				t.Errorf("classifyWarning = (%q, %v), want the attention-needing never-fired kind", kind, attention)
			}
		})
	}
	// Guard the guard: the advice is only reachable on the `when` arm.
	if strings.Contains(ruleNeverFiredWarning(ruleFiring{index: 0}, 20000), "round(field)") {
		t.Error("the no-`when` arm carries pre-rounding advice it has no use for")
	}
}

// TestRuleFirings_ConstraintRejectedOnlyIsItsOwnCause is the DETECTION
// half of the case above: the constraint arm is reached from evidence
// the applier records, not from a guess.
//
// The rule fires on every attempt and none of those attempts is
// committed, which is exactly what generate() does when a constraint
// rejects the row. The warning must then blame the constraint — the
// separation the original single message could not make, and the reason
// it sent this case to a normalisation that would have done nothing.
func TestRuleFirings_ConstraintRejectedOnlyIsItsOwnCause(t *testing.T) {
	a := firingFixture(t, RuleSpec{When: "aware == 1", SetNull: []string{"score"}})

	for i := 0; i < 5; i++ {
		row, mask := firingRow(1)
		if err := a.apply(row, mask); err != nil {
			t.Fatalf("apply: %v", err)
		}
		// No commitRow: the constraint pass rejected this row.
	}
	// One accepted row the rule did NOT select, so the run generated
	// something and the zero is a real zero.
	row, mask := firingRow(0)
	if err := a.apply(row, mask); err != nil {
		t.Fatalf("apply: %v", err)
	}
	a.commitRow()

	ws := a.neverFiredWarnings(1)
	if len(ws) != 1 {
		t.Fatalf("neverFiredWarnings = %v, want exactly one", ws)
	}
	if !strings.Contains(ws[0], "constraint") {
		t.Errorf("a rule that fired only on rejected rows does not blame the constraint: %q", ws[0])
	}
	if strings.Contains(ws[0], "round(field)") {
		t.Errorf("a rule the constraint removed is sent to a rounding normalisation: %q", ws[0])
	}
}

// TestWhenFieldsRead_ClassifiesTheFieldsAPredicateReads pins the
// classification the message is built from, straight off the fixture's
// own schema — a message arm is only as right as the predicate behind
// it.
//
// The fixture carries one of each shape that matters: `nps` is a u4 on a
// continuous `uniform`, so a comparison against it is pre-rounding;
// `aware` is a packed_bool on `bernoulli`, so it is exact; `score` is an
// f64, which the writer stores as given; `region` is a categorical a
// numeric comparison never reaches.
func TestWhenFieldsRead_ClassifiesTheFieldsAPredicateReads(t *testing.T) {
	spec := ruleSpecFixture()
	byName := map[string]FieldSpec{}
	for _, f := range spec.Fields {
		byName[f.Name] = f
	}
	cases := []struct {
		src            string
		wantNames      []string
		wantPreRounded []string
	}{
		{"nps == 9", []string{"nps"}, []string{"nps"}},
		{"aware == 1", []string{"aware"}, nil},
		{"score > 0.5", []string{"score"}, nil},
		{`region == "east"`, []string{"region"}, nil},
		{"nps == 9 && aware == 1", []string{"aware", "nps"}, []string{"nps"}},
		// isnull's string spelling must resolve to the field too, or a
		// co-missingness gate reads as a predicate over nothing.
		{`isnull("nps")`, []string{"nps"}, []string{"nps"}},
		// An identifier that is not a declared field contributes
		// nothing — the message names fields, not free variables.
		{"1 == 2", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got := whenFieldsRead(tc.src, byName, nil)
			var names, pre []string
			for _, f := range got {
				names = append(names, f.name)
				if f.preRounded {
					pre = append(pre, f.name)
				}
			}
			if strings.Join(names, ",") != strings.Join(tc.wantNames, ",") {
				t.Errorf("fields read = %v, want %v", names, tc.wantNames)
			}
			if strings.Join(pre, ",") != strings.Join(tc.wantPreRounded, ",") {
				t.Errorf("pre-rounded = %v, want %v", pre, tc.wantPreRounded)
			}
		})
	}

	// A field some rule's set_expr writes loses its exactness claim: the
	// expression's result is an arbitrary float, so the value in the row
	// need no longer sit on the distribution's own support.
	got := whenFieldsRead("aware == 1", byName, map[string]bool{"aware": true})
	if len(got) != 1 || !got[0].preRounded {
		t.Errorf("a set_expr-written packed_bool still claims exactness: %+v", got)
	}
}
