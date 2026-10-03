package descriptor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// The guidance prose lint (TestGuidanceProseLint) holds every built-in
// Purpose, Interpretation and shared rule set to a statistical-reporting
// rubric derived from the ASA statement on p-values (Wasserstein & Lazar
// 2016), APA JARS-Quant and SAMPL. Two rule shapes:
//
//   - text rules (ASA-*) scan every prose string for a banned reading;
//   - registry rules (PV-SHARED, ES-CONV, CORR-CAUSAL, ASSUME-INDEP,
//     MULTI-COMP, NORM-POWER) require a declaration or caveat on the
//     operators their scope table names.
//
// Every operator-scoped rule applies only once the operator declares
// guidance, so an undeclared operator never fails. The scope tables
// below are data: a later backfill extends coverage by declaring
// guidance, never by editing the lint.

// Rule IDs.
const (
	ruleASAProof     = "ASA-PROOF"
	ruleASAPNull     = "ASA-PNULL"
	ruleASANoDiff    = "ASA-NODIFF"
	ruleASAImportant = "ASA-IMPORTANT"
	rulePVShared     = "PV-SHARED"
	ruleESConv       = "ES-CONV"
	ruleCorrCausal   = "CORR-CAUSAL"
	ruleAssumeIndep  = "ASSUME-INDEP"
	ruleMultiComp    = "MULTI-COMP"
	ruleNormPower    = "NORM-POWER"
)

// sharedLintPrefix names a shared rule set in the lint's operator slot.
const sharedLintPrefix = "shared:"

// lintScope names operators by exact name, prefix or suffix.
type lintScope struct {
	names    []string
	prefixes []string
	suffixes []string
}

func (s lintScope) covers(op string) bool {
	for _, n := range s.names {
		if op == n {
			return true
		}
	}
	for _, p := range s.prefixes {
		if strings.HasPrefix(op, p) {
			return true
		}
	}
	for _, x := range s.suffixes {
		if strings.HasSuffix(op, x) {
			return true
		}
	}
	return false
}

// lintViolation is one hit: (operator, field, rule ID, offending span).
type lintViolation struct {
	Operator string
	Field    string
	Rule     string
	Span     string
}

func (v lintViolation) String() string {
	return fmt.Sprintf("(%s, %s, %s, %q)", v.Operator, v.Field, v.Rule, v.Span)
}

// lintRegistry is the guidance the lint reads; the gate hands it the
// built-ins, the unit cases hand it fixtures.
type lintRegistry struct {
	purposes map[string]descriptor.Purpose
	interps  map[string][]descriptor.Interpretation
	shared   map[string]descriptor.Interpretation
}

func builtinLintRegistry() lintRegistry {
	shared := map[string]descriptor.Interpretation{}
	for _, k := range SharedInterpretationKeys() {
		in, _ := SharedInterpretation(k)
		shared[k] = in
	}
	purposes := map[string]descriptor.Purpose{}
	for k, p := range builtinPurposes {
		purposes[k] = p
	}
	return lintRegistry{purposes: purposes, interps: BuiltinInterpretations(), shared: shared}
}

// ---- text rules ------------------------------------------------------

// textRule bans a phrase pattern. A match whose `gap` submatch (named
// group) carries a negation is a contrast ("significant is not the same
// as important"), not a coupling, and is skipped when negationGap is set.
type textRule struct {
	id          string
	patterns    []*regexp.Regexp
	negationGap bool
}

var lintNegation = regexp.MustCompile(`(?i)\b(?:not|never|no|nor)\b|n't\b`)

var textRules = []textRule{
	{id: ruleASAProof, patterns: []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:prove[sdn]?|proving|proof|confirm(?:s|ed|ing)?|establish(?:es|ed|ing)?)\b[^.;:]{0,60}?\b(?:hypothes[ie]s|null|effects?|differences?|relationships?|association|cause)\b`),
		regexp.MustCompile(`(?i)\b(?:hypothes[ie]s|null|effects?|differences?|relationships?)\b[^.;:]{0,30}?\b(?:is|was|are|were|been)\s+(?:proven|proved|confirmed|established)\b`),
	}},
	{id: ruleASAPNull, patterns: []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:due to|caused by|produced by|the result of)\s+(?:random\s+|pure\s+|mere\s+)?chance\b`),
		regexp.MustCompile(`(?i)\b(?:probability|chance|likelihood|odds)\b[^.;:]{0,40}?\b(?:null|hypothesis|result|finding|difference)\b[^.;:]{0,30}?\b(?:is|was|being)\s+(?:true|false|correct|wrong|real|random|a fluke)\b`),
		regexp.MustCompile(`(?i)\b(?:probability|chance|likelihood)\b[^.;:]{0,40}?\b(?:fluke|accident)\b`),
	}},
	{id: ruleASANoDiff, patterns: []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bno\s+(?:real\s+|actual\s+|true\s+|significant\s+)?(?:difference|effect|relationship)s?\b`),
	}},
	{id: ruleASAImportant, negationGap: true, patterns: []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bsignifican(?:t|ce)\b(?P<gap>[^.;:]{0,60}?)\b(?:important|importance|meaningful|large)\b`),
		regexp.MustCompile(`(?i)\b(?:important|meaningful|large)\b(?P<gap>[^.;:]{0,60}?)\bsignifican(?:t|ce)\b`),
	}},
}

// textHits returns every text-rule hit in text.
func textHits(text string) []struct{ rule, span string } {
	var out []struct{ rule, span string }
	for _, r := range textRules {
		for _, re := range r.patterns {
			gapIdx := re.SubexpIndex("gap")
			for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
				if r.negationGap && gapIdx > 0 && m[2*gapIdx] >= 0 &&
					lintNegation.MatchString(text[m[2*gapIdx]:m[2*gapIdx+1]]) {
					continue
				}
				out = append(out, struct{ rule, span string }{r.id, text[m[0]:m[1]]})
			}
		}
	}
	return out
}

// lintText is one prose string and where it sits.
type lintText struct {
	field string
	text  string
}

func purposeTexts(p descriptor.Purpose) []lintText {
	out := []lintText{{"plain", p.Plain}}
	for i, q := range p.Questions {
		out = append(out, lintText{fmt.Sprintf("questions[%d]", i), q})
	}
	domains := make([]string, 0, len(p.UseCases))
	for d := range p.UseCases {
		domains = append(domains, string(d))
	}
	sort.Strings(domains)
	for _, d := range domains {
		out = append(out, lintText{"use_cases." + d, p.UseCases[descriptor.Domain(d)]})
	}
	for i, a := range p.NotFor {
		out = append(out, lintText{fmt.Sprintf("not_for[%d].when", i), a.When})
	}
	for i, a := range p.Assumptions {
		out = append(out, lintText{fmt.Sprintf("assumptions[%d]", i), a})
	}
	return out
}

// interpTexts lists an Interpretation's prose; prefix is the field
// path ("" for a shared rule set, whose Field is empty).
func interpTexts(prefix string, in descriptor.Interpretation) []lintText {
	at := func(s string) string {
		if prefix == "" {
			return s
		}
		return prefix + "." + s
	}
	out := []lintText{{at("means"), in.Means}}
	for i, b := range in.Bands {
		out = append(out, lintText{at(fmt.Sprintf("bands[%d].label", i)), b.Label})
	}
	signs := make([]string, 0, len(in.Sign))
	for k := range in.Sign {
		signs = append(signs, k)
	}
	sort.Strings(signs)
	for _, k := range signs {
		out = append(out, lintText{at("sign." + k), in.Sign[k]})
	}
	for i, c := range in.Caveats {
		out = append(out, lintText{at(fmt.Sprintf("caveats[%d]", i)), c})
	}
	return out
}

// ---- registry rules --------------------------------------------------

// pValueFieldsByOperator lists p-value fields whose path does not end in
// p_value / p_values (the overlay slot quirks).
var pValueFieldsByOperator = map[string][]string{
	"OVERLAY_T_VS_REF":     {"summary.statistic"},
	"OVERLAY_Z_VS_REF":     {"summary.statistic"},
	"OVERLAY_CHISQ_VS_REF": {"scalar"},
}

func isPValueField(op, field string) bool {
	leaf := field
	if i := strings.LastIndex(field, "."); i >= 0 {
		leaf = field[i+1:]
	}
	if leaf == "*" {
		parts := strings.Split(field, ".")
		if len(parts) >= 2 {
			leaf = parts[len(parts)-2]
		}
	}
	if leaf == "p_value" || leaf == "p_values" || strings.HasPrefix(field, "p_values.") {
		return true
	}
	for _, f := range pValueFieldsByOperator[op] {
		if f == field {
			return true
		}
	}
	return false
}

// effectSizeFieldPrefix marks an effect-size Interpretation.
const effectSizeFieldPrefix = "details.effect_size."

// noBandsRationale is the caveat shape an effect size without registry
// bands must carry instead (ES-CONV).
var noBandsRationale = regexp.MustCompile(`(?i)\bno\s+(?:sourced|published|agreed|standard|conventional|established|widely\s+used)\s+(?:bands|benchmarks|thresholds|cut-?offs|conventions?)\b`)

// correlationScope: operators whose "statistic" is a correlation
// (CORR-CAUSAL).
var correlationScope = lintScope{names: []string{"TEST_PEARSON_R", "TEST_SPEARMAN_R", "TEST_KENDALL_TAU"}}

var causationCaveat = regexp.MustCompile(`(?i)\bcaus(?:e|es|ed|ation|al|ally)\b`)

// testScope / pairedTestScope drive ASSUME-INDEP: every TEST_* Purpose
// names independence; a paired test may name its pairing instead.
var (
	testScope       = lintScope{prefixes: []string{"TEST_"}}
	pairedTestScope = lintScope{names: []string{"TEST_PAIRED_T", "TEST_WILCOXON_SR", "TEST_ANOVA_RM"}}
	independenceRe  = regexp.MustCompile(`(?i)\bindependen(?:t|ce|tly)\b`)
	pairingRe       = regexp.MustCompile(`(?i)\b(?:pair(?:s|ed|ing)?|matched|same\s+(?:subjects|rows|people|units|respondents))\b`)
)

// contentRule requires every pattern to appear somewhere in an in-scope
// operator's own prose (Purpose and Interpretations; the shared rule
// sets do not count — they apply to every p-value alike).
type contentRule struct {
	id       string
	scope    lintScope
	requires map[string]*regexp.Regexp // label -> pattern
}

var contentRules = []contentRule{
	{
		id: ruleMultiComp,
		scope: lintScope{
			names:    []string{"TEST_TUKEY_HSD", "OVERLAY_PROP_Z_PANEL"},
			prefixes: []string{"OVERLAY_PAIRWISE_"},
			suffixes: []string{"_CELL"},
		},
		requires: map[string]*regexp.Regexp{
			"multiple-comparisons caveat": regexp.MustCompile(`(?i)\bmultiple\s+comparisons?\b|\bmany\s+(?:comparisons|tests|cells|pairs)\b|\bfamily-?wise\b|\bmore\s+than\s+one\s+(?:test|comparison)\b`),
		},
	},
	{
		id:    ruleNormPower,
		scope: lintScope{names: []string{"TEST_SHAPIRO_WILK", "TEST_KS", "OVERLAY_KS_VS_POP"}},
		requires: map[string]*regexp.Regexp{
			"small-n low power":          regexp.MustCompile(`(?i)\blow\s+power\b|\b(?:small|few)\b[^.;]{0,60}?\b(?:power|miss\w*|detect\w*|undetected)\b`),
			"large-n trivial departures": regexp.MustCompile(`(?i)\b(?:large|huge|big|many)\b[^.;]{0,80}?\b(?:trivial|tiny|negligible|minor)\b|\b(?:trivial|tiny|negligible|minor)\b[^.;]{0,80}?\b(?:large|huge|big|many)\b`),
		},
	},
	{
		id:    ruleNormPower,
		scope: lintScope{names: []string{"TEST_KS", "OVERLAY_KS_VS_POP"}},
		requires: map[string]*regexp.Regexp{
			"estimated-parameters (Lilliefors) caveat": regexp.MustCompile(`(?i)\blilliefors\b|\bestimated\s+(?:from\s+the\s+data|parameters)\b|\bparameters\s+(?:are\s+)?estimated\b`),
		},
	},
}

// registeredCitations is the set of Convention texts the registry hands
// out (ES-CONV).
func registeredCitations() map[string]bool {
	out := map[string]bool{}
	for _, id := range conventionIDs() {
		out[conventionCitation(id)] = true
	}
	return out
}

// lintGuidance runs every rule over reg.
func lintGuidance(reg lintRegistry) []lintViolation {
	var out []lintViolation
	addText := func(op string, ts []lintText) {
		for _, t := range ts {
			for _, h := range textHits(t.text) {
				out = append(out, lintViolation{op, t.field, h.rule, h.span})
			}
		}
	}

	ops := map[string]bool{}
	for op := range reg.purposes {
		ops[op] = true
	}
	for op := range reg.interps {
		ops[op] = true
	}
	names := make([]string, 0, len(ops))
	for op := range ops {
		names = append(names, op)
	}
	sort.Strings(names)

	citations := registeredCitations()
	for _, op := range names {
		p, hasPurpose := reg.purposes[op]
		ins := reg.interps[op]
		var own []lintText
		if hasPurpose {
			pt := purposeTexts(p)
			addText(op, prefixed("purpose", pt))
			own = append(own, pt...)
		}
		for _, in := range ins {
			it := interpTexts(in.Field, in)
			addText(op, it)
			own = append(own, it...)

			if isPValueField(op, in.Field) && (in.Shared != SharedPValue || strings.TrimSpace(in.Means) != "") {
				out = append(out, lintViolation{op, in.Field, rulePVShared, in.Means})
			}
			if strings.HasPrefix(in.Field, effectSizeFieldPrefix) && in.Shared == "" {
				banded := len(in.Bands) > 0 && citations[in.Convention]
				rationale := false
				for _, c := range in.Caveats {
					rationale = rationale || noBandsRationale.MatchString(c)
				}
				if !banded && !rationale {
					out = append(out, lintViolation{op, in.Field, ruleESConv, in.Convention})
				}
			}
			if in.Field == "statistic" && correlationScope.covers(op) {
				ok := false
				for _, c := range in.Caveats {
					ok = ok || causationCaveat.MatchString(c)
				}
				if !ok {
					out = append(out, lintViolation{op, "statistic.caveats", ruleCorrCausal, ""})
				}
			}
		}
		if hasPurpose && testScope.covers(op) {
			joined := strings.Join(p.Assumptions, " ")
			if !independenceRe.MatchString(joined) &&
				(!pairedTestScope.covers(op) || !pairingRe.MatchString(joined)) {
				out = append(out, lintViolation{op, "purpose.assumptions", ruleAssumeIndep, ""})
			}
		}
		var all strings.Builder
		for _, t := range own {
			all.WriteString(t.text)
			all.WriteString("\n")
		}
		for _, cr := range contentRules {
			if !cr.scope.covers(op) {
				continue
			}
			labels := make([]string, 0, len(cr.requires))
			for l := range cr.requires {
				labels = append(labels, l)
			}
			sort.Strings(labels)
			for _, l := range labels {
				if !cr.requires[l].MatchString(all.String()) {
					out = append(out, lintViolation{op, "*", cr.id, "missing " + l})
				}
			}
		}
	}

	keys := make([]string, 0, len(reg.shared))
	for k := range reg.shared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		addText(sharedLintPrefix+k, interpTexts("", reg.shared[k]))
	}
	return out
}

func prefixed(prefix string, ts []lintText) []lintText {
	out := make([]lintText, len(ts))
	for i, t := range ts {
		out[i] = lintText{prefix + "." + t.field, t.text}
	}
	return out
}

// ---- allowlist -------------------------------------------------------

// lintAllow exempts one (operator, field, rule) triple. Why is mandatory
// and an entry matching no current violation is stale.
type lintAllow struct {
	Operator, Field, Rule, Why string
}

var guidanceLintAllowlist = []lintAllow{
	{
		Operator: sharedLintPrefix + SharedPValue, Field: "means", Rule: ruleASANoDiff,
		Why: "States the null hypothesis the p-value is computed under (\"usually no difference or link\"), not a reading of a result.",
	},
	{
		Operator: sharedLintPrefix + SharedPValue, Field: "caveats[1]", Rule: ruleASANoDiff,
		Why: "The ASA absence-of-evidence warning itself: \"not significant does not mean there is no difference\".",
	},
}

// applyAllowlist drops allowlisted violations and reports unjustified or
// stale entries.
func applyAllowlist(vs []lintViolation, allow []lintAllow) (remaining []lintViolation, problems []string) {
	used := make([]bool, len(allow))
	for _, a := range allow {
		if strings.TrimSpace(a.Why) == "" {
			problems = append(problems, fmt.Sprintf("allowlist entry (%s, %s, %s) has no justification", a.Operator, a.Field, a.Rule))
		}
	}
	for _, v := range vs {
		hit := false
		for i, a := range allow {
			if a.Operator == v.Operator && a.Field == v.Field && a.Rule == v.Rule {
				used[i] = true
				hit = true
			}
		}
		if !hit {
			remaining = append(remaining, v)
		}
	}
	for i, a := range allow {
		if !used[i] {
			problems = append(problems, fmt.Sprintf("allowlist entry (%s, %s, %s) is stale: it matches no current violation", a.Operator, a.Field, a.Rule))
		}
	}
	return remaining, problems
}

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
