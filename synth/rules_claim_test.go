package synth_test

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// E2-S2 test pack: the priority-0 pre-claim a structural rule makes on
// a field it DETERMINES.
//
// Every test here turns on one question — did resolveConflicts hand the
// field to the rule, or did it leave it with the model, pair or
// correlation that was going to produce a value the rule discards? The
// answer is observable three ways and each test names which it is
// using, because two of the three are indirect:
//
//   - the conflict WARNING, which names both claimants;
//   - the fidelity report's `models` section, which is derived from the
//     same arbitration and is the surface this story exists for;
//   - the RNG STREAM, because dropping a model also drops that drawer's
//     unconditional per-row NormFloat64 and every later row shifts.
//
// The third is the only end-to-end signal available for an
// unconditional `set_null`, where the target is masked on every row and
// so shows nothing of its own on the wire.

// claimWarning is the exact shape resolveConflicts emits when a
// structural rule takes a target off a lower-priority claimant.
func claimWarning(field string, ruleIdx int, slot, dropped string) string {
	return fmt.Sprintf(
		"conditional relationship conflict: field %q is already claimed by structural rule %d (%s); dropping %s",
		field, ruleIdx, slot, dropped)
}

func hasWarning(warnings []string, want string) bool {
	for _, w := range warnings {
		if w == want {
			return true
		}
	}
	return false
}

func warningContaining(warnings []string, substr string) (string, bool) {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return w, true
		}
	}
	return "", false
}

// TestRuleClaim_UnconditionalValueRuleRetiresTheModel asserts BOTH
// halves of the claim in one test, because each is trivially satisfiable
// by abandoning the other: the model that LOSES must be gone (warned,
// and its field carrying the rule's value), and the model that does NOT
// lose must still run (its field carrying the model-inferred value, not
// its bare marginal).
//
// A one-sided test would pass against a change that dropped every model
// whenever any rule existed.
func TestRuleClaim_UnconditionalValueRuleRetiresTheModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule synth.RuleSpec
		slot string
		want func(aware float64) float64
	}{
		{
			name: "set literal",
			rule: synth.RuleSpec{Set: map[string]any{"score": 3.0}},
			slot: "set",
			want: func(float64) float64 { return 3 },
		},
		{
			name: "set_expr",
			rule: synth.RuleSpec{SetExpr: map[string]string{"score": "aware * 2"}},
			slot: "set_expr",
			want: func(a float64) float64 { return a * 2 },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := ruleModelSpec(300, []synth.RuleSpec{tc.rule})
			data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}

			want := claimWarning("score", 0, tc.slot, "linear model")
			if !hasWarning(res.Warnings, want) {
				t.Fatalf("missing the claim warning\n want %q\n  got %v", want, res.Warnings)
			}
			// ...and ONLY the claimed field's model lost. A warning
			// naming `spend` would mean the claim was over-broad.
			if w, ok := warningContaining(res.Warnings, "\"spend\""); ok {
				t.Fatalf("the claim reached an unclaimed field: %q", w)
			}

			aware := readF64Field(t, data, "aware")
			region := readCategoricalField(t, data, "region")
			score, _ := readFieldRows(t, data, "score")
			spend, _ := readFieldRows(t, data, "spend")
			inferredSpend := map[string]float64{"east": 100, "west": 140}

			for i := range score {
				if got, w := score[i], tc.want(aware[i]); math.Abs(got-w) > ruleValueTolerance {
					t.Fatalf("row %d: score = %v, want the rule's %v", i, got, w)
				}
				if w := inferredSpend[region[i]]; math.Abs(spend[i]-w) > ruleValueTolerance {
					t.Fatalf("row %d (region %s): spend = %v, want the model-inferred %v — "+
						"a rule claiming `score` must not retire `spend`'s model",
						i, region[i], spend[i], w)
				}
			}
		})
	}
}

// TestRuleClaim_ExcludedModelDisappearsFromTheFidelityReport is the
// criterion the story exists for, and it is asserted ON THE REPORT
// rather than inferred from a warning.
//
// The failure this prevents is silent by construction: a rule overwrites
// `spend`, generation correctly produces the rule's value, and the
// report then refits `spend`'s captured coefficients against rows that
// never carried them and publishes a captured-versus-recovered delta for
// a value nothing kept. Every number renders. Nothing on the report says
// the model did not run.
//
// Both halves again: the claimed model must be ABSENT and the unclaimed
// one PRESENT, so a change that emptied the section entirely fails.
func TestRuleClaim_ExcludedModelDisappearsFromTheFidelityReport(t *testing.T) {
	spec := modelFidelitySpec(1200,
		synth.FieldModelSpec{
			Field: "spend", Intercept: 100, ResidualStd: 10, Min: 0, Max: 400,
			Predictors: []synth.ModelPredictorSpec{
				{Kind: "categorical_level", Field: "region", Level: "west", Coefficient: 30},
			},
		},
		synth.FieldModelSpec{
			Field: "tenure", Intercept: 30, ResidualStd: 3, Min: 0, Max: 100,
			Predictors: []synth.ModelPredictorSpec{
				{Kind: "categorical_level", Field: "plan", Level: "pro", Coefficient: 8},
			},
		},
	)
	spec.Rules = []synth.RuleSpec{{Set: map[string]any{"spend": 42.0}}}

	schema, records := augmentForModelFidelity(t, spec, 1200, 91)
	report := &synth.FidelityReport{}
	synth.BuildModelFidelity(report, schema, records, spec)

	if m := findModelFidelity(report, "spend"); m != nil {
		t.Fatalf("the fidelity report scores `spend`, whose value an unconditional rule "+
			"overwrites on every row: %+v", m)
	}
	if m := findModelFidelity(report, "tenure"); m == nil {
		t.Fatalf("the fidelity report lost `tenure`, which no rule claims; got %d entry/entries",
			len(report.Models))
	}
}

// TestRuleClaim_ExcludedFieldLeavesTheResidualCorrelation covers the
// third relationship a claim retires. A residual correlation is a
// statement about two MODELS, so a field whose model the rule took has
// no residual left to correlate; buildResidualCorrelator already owns
// that vocabulary for a model dropped in translation, and the claim
// reuses it rather than inventing a second phrasing.
//
// The surviving endpoint must keep its model — with one participant gone
// the matrix is a scalar and simply does not build, which is the
// documented nil case, not a failure.
func TestRuleClaim_ExcludedFieldLeavesTheResidualCorrelation(t *testing.T) {
	spec := modelFidelitySpec(400,
		synth.FieldModelSpec{
			Field: "spend", Intercept: 100, ResidualStd: 10, Min: 0, Max: 400,
			Predictors: []synth.ModelPredictorSpec{
				{Kind: "categorical_level", Field: "region", Level: "west", Coefficient: 30},
			},
		},
		synth.FieldModelSpec{
			Field: "tenure", Intercept: 30, ResidualStd: 3, Min: 0, Max: 100,
			Predictors: []synth.ModelPredictorSpec{
				{Kind: "categorical_level", Field: "plan", Level: "pro", Coefficient: 8},
			},
		},
	)
	spec.ResidualCorrelations = []synth.CorrelationSpec{{A: "spend", B: "tenure", Correlation: 0.6}}
	spec.Rules = []synth.RuleSpec{{SetExpr: map[string]string{"spend": "42"}}}

	_, res, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if !hasWarning(res.Warnings, claimWarning("spend", 0, "set_expr", "linear model")) {
		t.Fatalf("the rule did not claim `spend`: %v", res.Warnings)
	}
	w, ok := warningContaining(res.Warnings, "residual correlation(s) naming ")
	if !ok {
		t.Fatalf("the residual correlation naming a claimed field was dropped silently: %v", res.Warnings)
	}
	if !strings.Contains(w, "spend") {
		t.Fatalf("the residual-correlation drop does not name the claimed field: %q", w)
	}
	if strings.Contains(w, "tenure") {
		t.Fatalf("the residual-correlation drop names the SURVIVING endpoint too: %q", w)
	}
}

// TestRuleClaim_ConditionalRuleDoesNotClaim pins the first of the four
// exclusions. A `when`-carrying rule writes only the rows its predicate
// selects, so claiming its target would delete the model from every row
// the rule never touches — and those rows would still carry a plausible
// value (the bare marginal) with nothing reporting the loss.
func TestRuleClaim_ConditionalRuleDoesNotClaim(t *testing.T) {
	spec := ruleModelSpec(400, []synth.RuleSpec{{
		When:    "aware == 0",
		SetExpr: map[string]string{"score": "99"},
	}})
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 19})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if w, ok := warningContaining(res.Warnings, "dropping linear model"); ok {
		t.Fatalf("a conditional rule claimed its target: %q", w)
	}

	aware := readF64Field(t, data, "aware")
	region := readCategoricalField(t, data, "region")
	score, _ := readFieldRows(t, data, "score")
	inferred := map[string]float64{"east": 10, "west": 15}
	var gated, open int
	for i := range score {
		if aware[i] == 0 {
			gated++
			if math.Abs(score[i]-99) > ruleValueTolerance {
				t.Fatalf("row %d: gated score = %v, want 99", i, score[i])
			}
			continue
		}
		open++
		if w := inferred[region[i]]; math.Abs(score[i]-w) > ruleValueTolerance {
			t.Fatalf("row %d (region %s): score = %v, want the model-inferred %v — "+
				"a conditional rule must leave the model running on the rows it does not gate",
				i, region[i], score[i], w)
		}
	}
	if gated == 0 || open == 0 {
		t.Fatalf("fixture degenerate: %d gated / %d open rows", gated, open)
	}
}

// TestRuleClaim_SetNullNeverClaims is the criterion that protects
// `if gate then null else inferred`, and it is asserted end to end on
// generated bytes.
//
// A set_null REMOVES a value rather than supplying one, so the field's
// own model is exactly what produces the value the non-gated rows keep.
// Getting this backwards is silent — the file still looks plausible, it
// has merely lost the inference everywhere the rule did not gate.
//
// The two conditionalities need different evidence, which is why both
// are here:
//
//   - conditional: the non-gated rows carry the model-inferred value
//     directly, so the assertion is on `spend` itself;
//   - unconditional: EVERY row is masked, so `spend` shows nothing. The
//     evidence is the RNG stream — a retained drawer consumes one
//     unconditional NormFloat64 per row, so `aware` and `region` stay
//     bit-equal to the rules-free run. Drop the model and every later
//     row shifts.
func TestRuleClaim_SetNullNeverClaims(t *testing.T) {
	plain, _, err := synth.SynthBytes(ruleModelSpec(300, nil), synth.Options{Seed: 64})
	if err != nil {
		t.Fatalf("rules-free SynthBytes: %v", err)
	}

	for _, tc := range []struct {
		name        string
		rule        synth.RuleSpec
		checkValues bool
	}{
		{
			name:        "conditional",
			rule:        synth.RuleSpec{When: "aware == 0", SetNull: []string{"spend"}},
			checkValues: true,
		},
		{
			name: "unconditional",
			rule: synth.RuleSpec{SetNull: []string{"spend"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := ruleModelSpec(300, []synth.RuleSpec{tc.rule})
			data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 64})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}
			if w, ok := warningContaining(res.Warnings, "dropping linear model"); ok {
				t.Fatalf("a set_null rule claimed its target: %q", w)
			}

			// The stream signature. A claim would drop `spend`'s drawer,
			// and with it that drawer's per-row normal.
			for _, name := range []string{"aware"} {
				a, b := readF64Field(t, data, name), readF64Field(t, plain, name)
				for i := range a {
					if a[i] != b[i] {
						t.Fatalf("%s row %d: %v with the set_null rule vs %v without — "+
							"the rule retired a model and the RNG stream shifted", name, i, a[i], b[i])
					}
				}
			}
			ra, rb := readCategoricalField(t, data, "region"), readCategoricalField(t, plain, "region")
			for i := range ra {
				if ra[i] != rb[i] {
					t.Fatalf("region row %d: %q vs %q — the rule retired a model", i, ra[i], rb[i])
				}
			}

			if !tc.checkValues {
				return
			}
			aware := readF64Field(t, data, "aware")
			region := readCategoricalField(t, data, "region")
			spend, spendNull := readFieldRows(t, data, "spend")
			inferred := map[string]float64{"east": 100, "west": 140}
			var open int
			for i := range spend {
				if aware[i] == 0 {
					if !spendNull[i] {
						t.Fatalf("row %d: gated row must be null", i)
					}
					continue
				}
				open++
				if w := inferred[region[i]]; math.Abs(spend[i]-w) > ruleValueTolerance {
					t.Fatalf("row %d (region %s): spend = %v, want the model-inferred %v — "+
						"`if gate then null else inferred` is broken", i, region[i], spend[i], w)
				}
			}
			if open == 0 {
				t.Fatalf("fixture degenerate: every row was gated")
			}
		})
	}
}

// TestRuleClaim_NullTogetherDoesNotClaim: a block copies one null
// DECISION across its members and supplies no value, so it claims
// nothing — the same answer as set_null, for the same reason.
func TestRuleClaim_NullTogetherDoesNotClaim(t *testing.T) {
	spec := ruleModelSpec(300, []synth.RuleSpec{{
		NullTogether: []string{"spend", "score"},
	}})
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 77})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if w, ok := warningContaining(res.Warnings, "dropping linear model"); ok {
		t.Fatalf("a null_together rule claimed a member: %q", w)
	}
	plain, _, err := synth.SynthBytes(ruleModelSpec(300, nil), synth.Options{Seed: 77})
	if err != nil {
		t.Fatalf("rules-free SynthBytes: %v", err)
	}
	a, b := readF64Field(t, data, "aware"), readF64Field(t, plain, "aware")
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("aware row %d: %v vs %v — null_together retired a model and shifted the stream",
				i, a[i], b[i])
		}
	}
}

// TestRuleClaim_SelfReferentialSetExprDoesNotClaim is the fourth
// exclusion, and the one this story's brief did not anticipate.
//
// `{"set_expr": {"score": "score + 1"}}` does not DETERMINE `score` — it
// TRANSFORMS whatever generation produced for it. Claiming it would
// strip the model and leave the arithmetic applied to a bare marginal
// draw. That matters because the canonical instance of the form is the
// normalisation idiom this package documents as the REMEDY for the
// pre-rounding gotcha (`{"set_expr": {"nps": "int(nps)"}}`), so a naive
// claim would make the documented fix for one silent fault cause a
// larger one.
//
// The assertion is exact rather than statistical: with ResidualStd 0 the
// model's own value is 10 / 15 by region, so `score + 1` is 11 / 16 iff
// the model ran. A claimed field would carry a normal(10, 2) draw plus
// one and would essentially never land on either.
func TestRuleClaim_SelfReferentialSetExprDoesNotClaim(t *testing.T) {
	spec := ruleModelSpec(300, []synth.RuleSpec{{
		SetExpr: map[string]string{"score": "score + 1"},
	}})
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 29})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if w, ok := warningContaining(res.Warnings, "dropping linear model"); ok {
		t.Fatalf("a self-referential set_expr claimed its own target: %q", w)
	}
	region := readCategoricalField(t, data, "region")
	score, _ := readFieldRows(t, data, "score")
	inferred := map[string]float64{"east": 11, "west": 16}
	for i := range score {
		if w := inferred[region[i]]; math.Abs(score[i]-w) > ruleValueTolerance {
			t.Fatalf("row %d (region %s): score = %v, want the model-inferred value plus one (%v) — "+
				"the rule reads its own target, so the model must still produce it",
				i, region[i], score[i], w)
		}
	}
}

// TestRuleClaim_ClaimedFieldDropsAConditionalPair covers the remaining
// relationship kind. The pair loses with the ORDINARY conflict wording —
// there is nothing special about losing to a rule, and inventing a
// second phrasing would split one finding across two shapes a reader
// (and warning_summary.go's taxonomy) has to learn separately.
func TestRuleClaim_ClaimedFieldDropsAConditionalPair(t *testing.T) {
	spec := ruleModelSpec(200, []synth.RuleSpec{{
		Set: map[string]any{"spend": 5.0},
	}})
	spec.Models = nil
	spec.CategoricalNumericPairs = []synth.CategoricalNumericPairSpec{{
		A: "region", B: "spend",
		Categories: []synth.CategoricalNumericCategorySpec{
			{Category: "east", Mean: 50, Std: 5},
			{Category: "west", Mean: 90, Std: 5},
		},
	}}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 37})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	want := claimWarning("spend", 0, "set", "categorical-numeric pair (region -> spend)")
	if !hasWarning(res.Warnings, want) {
		t.Fatalf("missing the pair-drop warning\n want %q\n  got %v", want, res.Warnings)
	}
	spend, _ := readFieldRows(t, data, "spend")
	for i := range spend {
		if math.Abs(spend[i]-5) > ruleValueTolerance {
			t.Fatalf("row %d: spend = %v, want the rule's 5", i, spend[i])
		}
	}
}

// TestRuleClaim_ClaimingRuleShiftsTheStreamNonClaimingDoesNot states,
// in one place, the ONE contract E2-S2 narrows.
//
// The rule PASS still consumes no RNG — that is unchanged and
// TestRules_ConsumeNoRNG still measures it. What is no longer true is
// the stronger sentence that used to follow from it: "a spec with rules
// draws the identical per-row sequence to the same spec without them".
// A claiming rule retires a model, and a retired model stops consuming
// its own unconditional per-row normal, so the stream legitimately
// moves. It moves for a REASON a warning names, which is the whole
// difference between this and a determinism bug.
//
// Both halves in one test: a non-claiming rule must still leave the
// stream bit-identical, or the "no RNG" property has quietly gone.
func TestRuleClaim_ClaimingRuleShiftsTheStreamNonClaimingDoesNot(t *testing.T) {
	const seed = 55
	plain, _, err := synth.SynthBytes(ruleModelSpec(200, nil), synth.Options{Seed: seed})
	if err != nil {
		t.Fatalf("rules-free: %v", err)
	}
	claiming, _, err := synth.SynthBytes(
		ruleModelSpec(200, []synth.RuleSpec{{Set: map[string]any{"score": 1.0}}}),
		synth.Options{Seed: seed})
	if err != nil {
		t.Fatalf("claiming: %v", err)
	}
	nonClaiming, _, err := synth.SynthBytes(
		ruleModelSpec(200, []synth.RuleSpec{{When: "aware == 0", Set: map[string]any{"score": 1.0}}}),
		synth.Options{Seed: seed})
	if err != nil {
		t.Fatalf("non-claiming: %v", err)
	}

	base := readCategoricalField(t, plain, "region")
	same := func(other []byte) bool {
		got := readCategoricalField(t, other, "region")
		for i := range base {
			if base[i] != got[i] {
				return false
			}
		}
		return true
	}
	if same(claiming) {
		t.Fatal("a claiming rule left the stream untouched: the model it claimed is still " +
			"consuming its per-row normal, so it was never retired")
	}
	if !same(nonClaiming) {
		t.Fatal("a NON-claiming rule shifted the stream: the rule pass has started consuming RNG, " +
			"or the claim has become over-broad")
	}
}
