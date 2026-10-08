package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// ---- the gate --------------------------------------------------------

// TestGuidanceProseLint: built-in guidance carries no misleading
// statistical phrasing and every required caveat (binding).
func TestGuidanceProseLint(t *testing.T) {
	remaining, problems := applyAllowlist(lintGuidance(builtinLintRegistry()), guidanceLintAllowlist)
	for _, p := range problems {
		t.Error(p)
	}
	for _, v := range remaining {
		t.Errorf("guidance lint violation %s", v)
	}
}

// ---- unit cases ------------------------------------------------------

func TestGuidanceProseLint_TextRules(t *testing.T) {
	cases := []struct {
		rule string
		bad  []string
		good []string
	}{
		{ruleASAProof,
			[]string{"A small p proves the hypothesis.", "This confirms an effect exists.", "The difference was established."},
			[]string{"A small p is evidence against the null.", "Confirm the field type before running."}},
		{ruleASAPNull,
			[]string{"The p-value is the chance the result is due to chance.", "The probability that the null is true.", "The odds the finding was a fluke."},
			[]string{"The chance of seeing a result at least this extreme if the null held.", "Some tests will be significant by chance alone."}},
		{ruleASANoDiff,
			[]string{"Read that as no effect.", "A large p means no difference between groups.", "There is no relationship."},
			[]string{"Read that as a negligible effect.", "0 means no straight-line link.", "The data are too few to detect a difference."}},
		{ruleASAImportant,
			[]string{"A significant result is important.", "Large and significant effects.", "Significance shows the effect is meaningful."},
			[]string{"Significant is not the same as important.", "Read the effect size for how large it is."}},
		{ruleMultiCompBan,
			[]string{"Adjust for multiple comparisons yourself.", "Pulse does not adjust them, so correct by hand.", "Correct the p-values manually (for example Holm)."},
			[]string{"Set multiplicity on the overlay to add adjusted p-values.", "The power-1 term is the original column, which you add yourself."}},
		{ruleSlotToken,
			[]string{"Compare the multiplicity of categories in a field.", "The weight_aware flag is set on every row.", "Plot the vectors on a chart."},
			[]string{"Set multiplicity on the request (holm) to adjust the p-values.", "Read p_adjusted, the corrected p-value.",
				"multiplicity controls duplicate-key handling: 'assert_unique' (default).", "Body weight and height, in kilograms."}},
	}
	for _, c := range cases {
		for _, s := range c.bad {
			if !hasRule(textHits(s), c.rule) {
				t.Errorf("%s: missed %q", c.rule, s)
			}
		}
		for _, s := range c.good {
			if hasRule(textHits(s), c.rule) {
				t.Errorf("%s: flagged clean %q", c.rule, s)
			}
		}
	}
}

// TestLintGuidanceText_RunsTextRules: the exported entry point other
// packages' tests use runs the same text rules, slot-token rule
// included.
func TestLintGuidanceText_RunsTextRules(t *testing.T) {
	got := LintGuidanceText("A small p proves the hypothesis. Compare the multiplicity of categories.")
	rules := map[string]bool{}
	for _, h := range got {
		rules[h.Rule] = true
	}
	if !rules[ruleASAProof] || !rules[ruleSlotToken] || len(got) != 2 {
		t.Errorf("LintGuidanceText = %v, want one %s and one %s hit", got, ruleASAProof, ruleSlotToken)
	}
	if got := LintGuidanceText("Set multiplicity on the overlay to adjust the p-values."); len(got) != 0 {
		t.Errorf("clean text flagged: %v", got)
	}
}

func hasRule(hits []struct{ rule, span string }, rule string) bool {
	for _, h := range hits {
		if h.rule == rule {
			return true
		}
	}
	return false
}

func violationRules(vs []lintViolation, op string) map[string]bool {
	out := map[string]bool{}
	for _, v := range vs {
		if v.Operator == op {
			out[v.Rule] = true
		}
	}
	return out
}

func TestGuidanceProseLint_RegistryRules(t *testing.T) {
	cohen := conventionCitation(ConventionCohenEta2)
	bands := conventionBands(ConventionCohenEta2)
	cases := []struct {
		name string
		rule string
		op   string
		bad  lintRegistry
		good lintRegistry
	}{
		{"pv-shared", rulePVShared, "TEST_X",
			lintRegistry{interps: map[string][]descriptor.Interpretation{"TEST_X": {{Field: "p_value", Means: "bespoke"}}}},
			lintRegistry{interps: map[string][]descriptor.Interpretation{"TEST_X": {{Field: "p_value", Shared: SharedPValue}}}}},
		{"pv-shared quirk slot", rulePVShared, "OVERLAY_CHISQ_VS_REF",
			lintRegistry{interps: map[string][]descriptor.Interpretation{"OVERLAY_CHISQ_VS_REF": {{Field: "scalar", Means: "p"}}}},
			lintRegistry{interps: map[string][]descriptor.Interpretation{"OVERLAY_CHISQ_VS_REF": {{Field: "scalar", Shared: SharedPValue}}}}},
		{"es-conv", ruleESConv, "TEST_X",
			lintRegistry{interps: map[string][]descriptor.Interpretation{"TEST_X": {{Field: "details.effect_size.eta_squared", Means: "m", Bands: bands, Convention: "Somebody (2001)"}}}},
			lintRegistry{interps: map[string][]descriptor.Interpretation{"TEST_X": {{Field: "details.effect_size.eta_squared", Means: "m", Bands: bands, Convention: cohen}}}}},
		{"es-conv rationale", ruleESConv, "TEST_X",
			lintRegistry{interps: map[string][]descriptor.Interpretation{"TEST_X": {{Field: "details.effect_size.rank_biserial", Means: "m"}}}},
			lintRegistry{interps: map[string][]descriptor.Interpretation{"TEST_X": {{Field: "details.effect_size.rank_biserial", Means: "m",
				Caveats: []string{"There are no sourced bands for this statistic; compare it with the same measure elsewhere."}}}}}},
		{"corr-causal", ruleCorrCausal, "TEST_SPEARMAN_R",
			lintRegistry{interps: map[string][]descriptor.Interpretation{"TEST_SPEARMAN_R": {{Field: "statistic", Means: "m"}}}},
			lintRegistry{interps: map[string][]descriptor.Interpretation{"TEST_SPEARMAN_R": {{Field: "statistic", Means: "m", Caveats: []string{"Correlation is not causation."}}}}}},
		{"corr-causal regression", ruleCorrCausal, "REG_OLS",
			lintRegistry{interps: map[string][]descriptor.Interpretation{"REG_OLS": {{Field: "coefficients.*", Means: "m"}}}},
			lintRegistry{interps: map[string][]descriptor.Interpretation{"REG_OLS": {{Field: "coefficients.*", Means: "m", Caveats: []string{"Association, not causation."}}}}}},
		{"assume-indep regression", ruleAssumeIndep, "REG_GLM",
			lintRegistry{purposes: map[string]descriptor.Purpose{"REG_GLM": {Assumptions: []string{"The family matches the outcome."}}}},
			lintRegistry{purposes: map[string]descriptor.Purpose{"REG_GLM": {Assumptions: []string{"Rows are independent."}}}}},
		{"assume-indep", ruleAssumeIndep, "TEST_T",
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_T": {Assumptions: []string{"Roughly normal."}}}},
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_T": {Assumptions: []string{"Rows are independent."}}}}},
		{"assume-indep paired", ruleAssumeIndep, "TEST_PAIRED_T",
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_PAIRED_T": {Assumptions: []string{"Roughly normal."}}}},
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_PAIRED_T": {Assumptions: []string{"Each row pairs two measurements of the same subject."}}}}},
		{"multi-comp tukey", ruleMultiComp, "TEST_TUKEY_HSD",
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_TUKEY_HSD": {Assumptions: []string{"Rows are independent."}}}},
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_TUKEY_HSD": {Assumptions: []string{"Rows are independent; the p-values already adjust for multiple comparisons."}}}}},
		{"multi-comp pairwise", ruleMultiComp, "OVERLAY_PAIRWISE_WELCH_T",
			lintRegistry{interps: map[string][]descriptor.Interpretation{"OVERLAY_PAIRWISE_WELCH_T": {{Field: "cells.value", Means: "m"}}}},
			lintRegistry{interps: map[string][]descriptor.Interpretation{"OVERLAY_PAIRWISE_WELCH_T": {{Field: "cells.value", Means: "m", Caveats: []string{"Many pairs are tested at once; expect some false hits."}}}}}},
		{"multi-comp cell", ruleMultiComp, "OVERLAY_Z_CELL",
			lintRegistry{interps: map[string][]descriptor.Interpretation{"OVERLAY_Z_CELL": {{Field: "cells.value", Means: "m"}}}},
			lintRegistry{interps: map[string][]descriptor.Interpretation{"OVERLAY_Z_CELL": {{Field: "cells.value", Means: "m", Caveats: []string{"Every cell is a separate test: adjust for multiple comparisons."}}}}}},
		{"norm-power", ruleNormPower, "TEST_SHAPIRO_WILK",
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_SHAPIRO_WILK": {Assumptions: []string{"Rows are independent."}}}},
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_SHAPIRO_WILK": {Assumptions: []string{
				"Rows are independent.",
				"With few rows the test has low power; with very large samples even trivial departures are flagged.",
			}}}}},
		{"norm-power ks lilliefors", ruleNormPower, "TEST_KS",
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_KS": {Assumptions: []string{
				"Rows are independent.",
				"With few rows the test has low power; with very large samples even trivial departures are flagged.",
			}}}},
			lintRegistry{purposes: map[string]descriptor.Purpose{"TEST_KS": {Assumptions: []string{
				"Rows are independent.",
				"With few rows the test has low power; with very large samples even trivial departures are flagged.",
				"Not a normality test with parameters estimated from the data: that needs the Lilliefors correction.",
			}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !violationRules(lintGuidance(c.bad), c.op)[c.rule] {
				t.Errorf("%s: bad fixture not flagged", c.rule)
			}
			if got := lintGuidance(c.good); violationRules(got, c.op)[c.rule] {
				t.Errorf("%s: good fixture flagged: %v", c.rule, got)
			}
		})
	}
}

// TestGuidanceProseLint_ScopedToDeclaredGuidance: an operator with no
// guidance is never checked, and an out-of-scope operator is never held
// to a scoped rule.
func TestGuidanceProseLint_ScopedToDeclaredGuidance(t *testing.T) {
	if vs := lintGuidance(lintRegistry{}); len(vs) != 0 {
		t.Errorf("empty registry produced violations: %v", vs)
	}
	reg := lintRegistry{purposes: map[string]descriptor.Purpose{"AGG_SUM": {Plain: "Total of a field."}}}
	if vs := lintGuidance(reg); len(vs) != 0 {
		t.Errorf("out-of-scope operator produced violations: %v", vs)
	}
}

// TestGuidanceProseLint_ReportsLocation: a hit names operator, field,
// rule and span.
func TestGuidanceProseLint_ReportsLocation(t *testing.T) {
	reg := lintRegistry{interps: map[string][]descriptor.Interpretation{
		"TEST_X": {{Field: "statistic", Means: "m", Caveats: []string{"ok", "Read that as no effect."}}},
	}}
	want := lintViolation{"TEST_X", "statistic.caveats[1]", ruleASANoDiff, "no effect"}
	for _, v := range lintGuidance(reg) {
		if v == want {
			return
		}
	}
	t.Errorf("want violation %s in %v", want, lintGuidance(reg))
}

func TestGuidanceProseLint_Allowlist(t *testing.T) {
	vs := []lintViolation{{"OP", "f", ruleASANoDiff, "no effect"}}
	rem, probs := applyAllowlist(vs, []lintAllow{{"OP", "f", ruleASANoDiff, "justified"}})
	if len(rem) != 0 || len(probs) != 0 {
		t.Errorf("justified matching entry: remaining %v problems %v", rem, probs)
	}
	_, probs = applyAllowlist(vs, []lintAllow{{"OP", "f", ruleASANoDiff, " "}})
	if len(probs) != 1 || !strings.Contains(probs[0], "no justification") {
		t.Errorf("unjustified entry not rejected: %v", probs)
	}
	rem, probs = applyAllowlist(vs, []lintAllow{{"OP", "g", ruleASANoDiff, "justified"}})
	if len(rem) != 1 || len(probs) != 1 || !strings.Contains(probs[0], "stale") {
		t.Errorf("stale entry not rejected: remaining %v problems %v", rem, probs)
	}
}
