package synth

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// The pre-rounding classifier decides one thing: whether a comparison in
// a rule's `when` reads a value the file will not hold. It is allowed to
// be incomplete and is not allowed to be wrong, and until these tests it
// was over-broad in two directions that both cost the diagnostic its
// point.
//
// (1) A `constant` field was inexact because the map keyed on the
// distribution NAME cannot see the literal — so {"value": 3} on a u8, a
// field that can only ever hold 3, was told about rounding.
//
// (2) Every `set_expr` target was inexact, including the target of the
// normalisation this package DOCUMENTS as the remedy. The message says
// "normalise first (an earlier rule setting round(field))"; having done
// exactly that, an author was told to do it again.

// TestDistributionIsExactValued_ConstantDependsOnTheLiteral is the FU-37
// gate. The claim under test is that the coercion decides, not the
// distribution name — and it is the SAME coercion the sampler applies
// (constantRowValue), so a diagnostic and a row cannot disagree about
// what the field holds.
func TestDistributionIsExactValued_ConstantDependsOnTheLiteral(t *testing.T) {
	cases := []struct {
		name     string
		typeName string
		value    any
		want     bool
	}{
		{"integral number on u8", "u8", 3.0, true},
		{"integral int on u8", "u8", 3, true},
		{"fractional number on u8", "u8", 3.4, false},
		{"bool coerces to 1", "packed_bool", true, true},
		{"bool coerces to 0", "packed_bool", false, true},
		{"zero on u4", "u4", 0.0, true},
		// A literal the coercion REFUSES is not admitted: buildSampler
		// will refuse the field on its own terms, and a diagnostic must
		// claim nothing about a field that will not build.
		{"string on a numeric is refused, not admitted", "u8", "3", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := FieldSpec{Name: "f", Type: tc.typeName, Distribution: DistConstant,
				Params: map[string]any{"value": tc.value}}
			if got := distributionIsExactValued(f); got != tc.want {
				t.Errorf("distributionIsExactValued = %v, want %v", got, tc.want)
			}
			// And the classifier that consumes it must agree: an exact
			// constant on a quantizing type is not pre-rounded.
			if got := fieldIsPreRounded(f, rewriteIndex{}, 0); got == tc.want {
				t.Errorf("fieldIsPreRounded = %v for an exactness of %v — the two must be opposites", got, tc.want)
			}
		})
	}

	// A constant with no `value` param at all claims nothing: the field
	// does not build either.
	f := FieldSpec{Name: "f", Type: "u8", Distribution: DistConstant}
	if distributionIsExactValued(f) {
		t.Error("a constant with no value param must not claim exactness")
	}
}

// TestExprIsIntegral_Matrix is the FU-38 recogniser. Every TRUE row is a
// statement about the expression that holds for every row it will ever
// see; every FALSE row is either genuinely non-integral or outside what
// the source alone demonstrates.
func TestExprIsIntegral_Matrix(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		// The documented normalisation idiom, and its siblings.
		{"round(nps)", true},
		{"int(nps)", true},
		{"floor(nps)", true},
		{"ceil(nps)", true},
		// Boolean results: the coercion matrix writes them as 1/0, which
		// is what makes --suggest-rules' emitted band rules exact.
		{"round(nps) >= 9", true},
		{"nps >= 7 && nps < 9", true},
		{"!(nps >= 9)", true},
		{"true", true},
		{"7", true},
		{"-7", true},
		{`region == "east"`, true},
		{"round(nps) >= 9 ? 1 : 0", true},
		// Genuinely non-integral.
		{"nps", false},
		{"nps * 1.5", false},
		{"abs(score)", false},
		{"score / 2", false},
		// Integral in fact, not demonstrable from the source alone
		// without a type lattice this diagnostic does not need.
		{"round(nps) + 1", false},
		// Unparseable answers false, the direction that keeps the old
		// advice. Unreachable past validateRules.
		{"round(", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			if got := exprIsIntegral(tc.src); got != tc.want {
				t.Errorf("exprIsIntegral(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

// TestRuleRewriteIndex_SplitsIntegralFromInexactWrites pins the index the
// classifier reads, including the two shapes that decide a tie: a field
// written twice keeps the LOWEST integral index, and a field written both
// ways lands in both maps (where fieldIsPreRounded gives inexact the
// verdict).
func TestRuleRewriteIndex_SplitsIntegralFromInexactWrites(t *testing.T) {
	rw := ruleRewriteIndex([]RuleSpec{
		{SetExpr: map[string]string{"score": "nps * 1.5"}},
		{SetExpr: map[string]string{"nps": "round(nps)"}},
		{SetExpr: map[string]string{"nps": "int(nps)", "aware": "nps >= 9"}},
		{SetExpr: map[string]string{"aware": "score"}},
	})
	if rw.inexact["nps"] {
		t.Error("nps is written only by integral expressions")
	}
	if !rw.inexact["score"] {
		t.Error("score is written by a non-integral expression")
	}
	if !rw.inexact["aware"] {
		t.Error("aware is written by a non-integral expression at rule 3")
	}
	if got, ok := rw.normalisedAt["nps"]; !ok || got != 1 {
		t.Errorf("normalisedAt[nps] = %d (ok=%v), want the LOWEST writing index 1", got, ok)
	}
	if got, ok := rw.normalisedAt["aware"]; !ok || got != 2 {
		t.Errorf("normalisedAt[aware] = %d (ok=%v), want 2", got, ok)
	}
}

// TestFieldIsPreRounded_EarlierIntegralNormalisationClearsTheHazard is
// the FU-38 payoff, stated as the three positions that matter. `nps` is
// a u4 on a continuous `uniform` — genuinely pre-rounding — and the
// documented remedy sits at rule 0.
func TestFieldIsPreRounded_EarlierIntegralNormalisationClearsTheHazard(t *testing.T) {
	spec := ruleSpecFixture(
		RuleSpec{When: `!isnull("nps")`, SetExpr: map[string]string{"nps": "round(nps)"}},
		RuleSpec{When: "nps >= 9", SetNull: []string{"score"}},
	)
	rw := ruleRewriteIndex(spec.Rules)
	nps := spec.Fields[1]
	if nps.Name != "nps" {
		t.Fatalf("fixture moved: field 1 is %q", nps.Name)
	}

	// At the normalising rule's OWN index the field still holds its
	// sampler's float — the rule has not run yet.
	if !fieldIsPreRounded(nps, rw, 0) {
		t.Error("the normalising rule itself reads the un-normalised value")
	}
	// At any LATER index the field holds round(nps). This is the case
	// that used to be reported, sending an author to apply a fix they
	// had already applied.
	if fieldIsPreRounded(nps, rw, 1) {
		t.Error("a rule reading a field an EARLIER integral set_expr normalised is still called pre-rounding")
	}

	// An INEXACT write anywhere keeps the hazard, whatever the position.
	rw2 := ruleRewriteIndex([]RuleSpec{
		{SetExpr: map[string]string{"nps": "round(nps)"}},
		{When: "nps >= 9", SetNull: []string{"score"}},
		{SetExpr: map[string]string{"nps": "score * 2"}},
	})
	if !fieldIsPreRounded(nps, rw2, 1) {
		t.Error("a non-integral rewrite must keep the field marked wherever it sits")
	}
}

// TestNeverFiredWarning_DoesNotSendANormalisedGateBackToRound is the
// end-to-end half: the classifier only matters through the message it
// builds, and the message is what an author reads.
func TestNeverFiredWarning_DoesNotSendANormalisedGateBackToRound(t *testing.T) {
	spec := ruleSpecFixture(
		RuleSpec{When: `!isnull("nps")`, SetExpr: map[string]string{"nps": "round(nps)"}},
		// Unsatisfiable: nps is uniform over [0, 11) so round(nps) never
		// reaches 99. The rule never fires and the warning has to say
		// something about why.
		RuleSpec{When: "nps >= 99", SetNull: []string{"score"}},
	)
	_, wfs, err := buildSchema(spec)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	applier, _, err := compileRules(spec.Rules, wfs)
	if err != nil {
		t.Fatalf("compileRules: %v", err)
	}
	ws := applier.neverFiredWarnings(100)
	if len(ws) == 0 {
		t.Fatal("the unsatisfiable rule must be reported as never fired")
	}
	for _, w := range ws {
		if !strings.Contains(w, "rule 1") {
			continue
		}
		if strings.Contains(w, "PRE-ROUNDING") || strings.Contains(w, "round(field)") {
			t.Errorf("a gate an earlier rule already normalised is sent back to round(): %q", w)
		}
		if !strings.Contains(w, "pre-rounding is NOT the cause") {
			t.Errorf("the message must state the exactness it found: %q", w)
		}
	}
}

// TestCompileRules_RefusesIndependentlyOfValidateRules is FU-17's close.
//
// `set_expr` and `when` are compiled TWICE per Spec — once by
// validateRules (to keep the refusal EAGER, at spec parse) and once by
// compileRules (to build the pass). Handing the applier validation's
// programs was rejected: SpecFromProfile bypasses validateSpec entirely,
// so compileRules must compile on its own regardless; ApplyRulesFile
// validates a COPY and then replaces Spec.Rules, so a validation-built
// cache would be a cache of a different rules slice and its staleness
// would be silent; and the measured cost is ~11.4us per expression, so a
// 14-rule candidate file pays ~160us once per run against ~1.6s to
// generate 20,000 rows. The reasoning lives at validateRules.
//
// This test pins the property that makes the duplication worth its
// price, and the property a shared cache would quietly remove: the
// applier's own refusal is INDEPENDENT and reaches the same coded error,
// with the same rule index and the same slot, on a Spec that never went
// through validateSpec. Sharing the programs would make this path stop
// refusing, which is exactly the "must not weaken the eager-validation
// contract" condition — asserted rather than promised.
func TestCompileRules_RefusesIndependentlyOfValidateRules(t *testing.T) {
	// Reaches compileRules WITHOUT validateSpec, the SpecFromProfile
	// shape. `nps * ` does not parse, so both compile sites must refuse.
	spec := ruleSpecFixture(
		RuleSpec{SetNull: []string{"score"}},
		RuleSpec{When: "nps * ", SetNull: []string{"nps"}},
	)

	validateErr := validateRules(spec)
	if validateErr == nil {
		t.Fatal("validateRules accepted an uncompilable when")
	}

	_, wfs, err := buildSchema(spec)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	_, _, compileErr := compileRules(spec.Rules, wfs)
	if compileErr == nil {
		t.Fatal("compileRules accepted an uncompilable when: the applier no longer compiles for itself, " +
			"so a Spec that bypassed validateSpec (SpecFromProfile) reaches the row loop with a broken rule")
	}

	for _, tc := range []struct {
		who string
		err error
	}{{"validateRules", validateErr}, {"compileRules", compileErr}} {
		coded, ok := tc.err.(*errors.CodedError)
		if !ok {
			t.Fatalf("%s returned %T, want a *errors.CodedError", tc.who, tc.err)
		}
		if coded.Code != errors.PULSE_SYNTH_RULE_EXPR_INVALID {
			t.Errorf("%s: code %v, want PULSE_SYNTH_RULE_EXPR_INVALID", tc.who, coded.Code)
		}
		if got := coded.Details[errors.DetailSynthRule]; got != 1 {
			t.Errorf("%s: rule index %v, want 1", tc.who, got)
		}
		if got := coded.Details[errors.DetailSynthRuleSlot]; got != "when" {
			t.Errorf("%s: slot %v, want \"when\"", tc.who, got)
		}
	}
}
