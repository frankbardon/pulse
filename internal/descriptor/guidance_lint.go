package descriptor

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
)

// guidance_lint.go is the guidance prose lint's rule table and engine,
// kept in non-test code so every test that renders guidance (the
// generated skill sections, docgen) can run the same rules over its
// output: LintGuidanceText is the text-rule entry point.
//
// The guidance prose lint (TestGuidanceProseLint) holds every built-in
// Purpose, Interpretation and shared rule set to a statistical-reporting
// rubric derived from the ASA statement on p-values (Wasserstein & Lazar
// 2016), APA JARS-Quant and SAMPL. Two rule shapes:
//
//   - text rules (ASA-*, MULTI-COMP-MANUAL, SLOT-TOKEN) scan every prose
//     string for a banned reading — MULTI-COMP-MANUAL bans telling the
//     reader to correct p-values by hand now that a multiplicity block
//     does it; SLOT-TOKEN flags a sentence naming a slot token
//     (slotTokens) that is not about that slot, which the prose scrub
//     would over-drop on an instance hiding the slot's capability;
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
	ruleMultiCompBan = "MULTI-COMP-MANUAL"
	ruleNormPower    = "NORM-POWER"
	ruleSlotToken    = "SLOT-TOKEN"
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
	{id: ruleMultiCompBan, patterns: []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:adjust|correct)\w*\b[^.;:]{0,80}?\b(?:yourself|by hand|manually)\b`),
		regexp.MustCompile(`(?i)\bPulse\s+(?:does\s+not|doesn't|never)\s+(?:adjust|correct)s?\b`),
	}},
}

// GuidanceLintHit is one text-rule hit: the rule ID and the offending
// span.
type GuidanceLintHit struct {
	Rule string
	Span string
}

// LintGuidanceText runs the guidance lint's text rules (ASA-*,
// MULTI-COMP-MANUAL, SLOT-TOKEN) over text — a prose string or rendered
// guidance — and returns every hit in rule order. The registry rules
// need the declared guidance, not text, and run only in
// TestGuidanceProseLint.
func LintGuidanceText(text string) []GuidanceLintHit {
	var out []GuidanceLintHit
	for _, h := range textHits(text) {
		out = append(out, GuidanceLintHit{Rule: h.rule, Span: h.span})
	}
	return out
}

// slotTokenHits returns, for each sentence of text naming a slot token
// (whole token; a homonym-marked sentence does not count) of some
// capability, the sentence when it is not about that slot: with the
// tokens cut out it no longer matches the capability's topic.
func slotTokenHits(text string) []struct{ rule, span string } {
	var out []struct{ rule, span string }
	for _, sentence := range splitSentences(text) {
		for _, c := range SlotTokenCapabilities() {
			set := slotTokens[c]
			if len(set.tokens) == 0 {
				continue
			}
			var b strings.Builder
			named, last := false, 0
			anyToken(sentence, func(tok string, start, end int) bool {
				if slices.Contains(set.tokens, tok) && !homonymSentence(sentence, start, end) {
					named = true
					b.WriteString(sentence[last:start])
					b.WriteByte(' ')
					last = end
				}
				return false
			})
			if !named {
				continue
			}
			b.WriteString(sentence[last:])
			if !set.topic.MatchString(b.String()) {
				out = append(out, struct{ rule, span string }{ruleSlotToken, sentence})
			}
		}
	}
	return out
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
	return append(out, slotTokenHits(text)...)
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
	for i, a := range p.FollowUps {
		out = append(out, lintText{fmt.Sprintf("follow_ups[%d].when", i), a.When})
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
	// MATRIX-payload kinds whose cell value is the p-value.
	"OVERLAY_FISHER_EXACT_CELL":             {"cells.value"},
	"OVERLAY_PAIRWISE_PROBIT_T":             {"cells.value"},
	"OVERLAY_PAIRWISE_PROP_Z":               {"cells.value"},
	"OVERLAY_PAIRWISE_TWO_MEANS_Z":          {"cells.value"},
	"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z": {"cells.value"},
	"OVERLAY_PAIRWISE_WELCH_T":              {"cells.value"},
	"OVERLAY_PROP_Z_CELL":                   {"cells.value"},
	"OVERLAY_PROP_Z_PANEL":                  {"cells.value"},
	"OVERLAY_T_CELL":                        {"cells.value"},
	"OVERLAY_Z_CELL":                        {"cells.value"},
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

// correlationMatrixScope: matrix operators whose primary.values cells
// are correlations (CORR-CAUSAL).
var correlationMatrixScope = lintScope{names: []string{"MAT_CORRELATION", "MAT_PARTIAL_CORRELATION"}}

// regressionScope: the REG_* types, whose coefficients.* reading must
// carry the causation caveat (CORR-CAUSAL) and whose Purpose must name
// independence (ASSUME-INDEP): a coefficient is an association holding
// the other predictors fixed, never on its own a causal effect.
var regressionScope = lintScope{prefixes: []string{"REG_"}}

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
			if (in.Field == "statistic" && correlationScope.covers(op)) ||
				(in.Field == "primary.values" && correlationMatrixScope.covers(op)) ||
				(in.Field == "coefficients.*" && regressionScope.covers(op)) {
				ok := false
				for _, c := range in.Caveats {
					ok = ok || causationCaveat.MatchString(c)
				}
				if !ok {
					out = append(out, lintViolation{op, in.Field + ".caveats", ruleCorrCausal, ""})
				}
			}
		}
		if hasPurpose && (testScope.covers(op) || regressionScope.covers(op)) {
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
