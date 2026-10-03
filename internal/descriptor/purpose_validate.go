package descriptor

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/frankbardon/pulse/descriptor"
)

// Purpose validity limits (binding for built-ins and, through the same
// ValidatePurpose, for extension registrations).
const (
	// PurposePlainMax caps Purpose.Plain, in characters.
	PurposePlainMax = 140
	// PurposeQuestionsMin is the fewest Questions a Purpose may carry.
	PurposeQuestionsMin = 2
)

// PurposeRule names one validity rule ValidatePurpose enforces. The
// rules fall into four families, one per named gate: the limits
// (TestSkillsCoverAllPurposes), alternatives (TestPurposeAlternativesResolve),
// intents (TestPurposeQuestionsResolve) and glossary links
// (TestGlossaryTermsResolve).
type PurposeRule string

// The validity rules.
const (
	// PurposeRulePlain: Plain is non-empty and at most PurposePlainMax
	// characters.
	PurposeRulePlain PurposeRule = "plain"
	// PurposeRuleIntents: at least one intent is declared.
	PurposeRuleIntents PurposeRule = "intents"
	// PurposeRuleQuestions: at least PurposeQuestionsMin non-empty
	// Questions.
	PurposeRuleQuestions PurposeRule = "questions"
	// PurposeRuleNotFor: at least one NotFor entry.
	PurposeRuleNotFor PurposeRule = "not_for"
	// PurposeRuleUseCases: at least one UseCase, every key a known
	// Domain, every value non-empty.
	PurposeRuleUseCases PurposeRule = "use_cases"
	// PurposeRuleLevel: Level is one of the declared levels.
	PurposeRuleLevel PurposeRule = "level"
	// PurposeRuleAlternative: every NotFor entry has a When and a Use
	// that resolves (and is not the operator itself).
	PurposeRuleAlternative PurposeRule = "alternative"
	// PurposeRuleIntentUnknown: every intent ID is in the taxonomy and
	// listed once.
	PurposeRuleIntentUnknown PurposeRule = "intent_unknown"
	// PurposeRuleGlossaryUnknown: every Glossary ID is a glossary term
	// and listed once.
	PurposeRuleGlossaryUnknown PurposeRule = "glossary_unknown"
	// PurposeRuleJargonUnlinked: every jargon term whose Form appears in
	// Plain is listed in Glossary.
	PurposeRuleJargonUnlinked PurposeRule = "jargon_unlinked"
)

// PurposeViolation is one broken validity rule on one Purpose.
type PurposeViolation struct {
	// Name is the operator (or registry key) the Purpose belongs to.
	Name string
	// Rule is the broken rule.
	Rule PurposeRule
	// Detail says what is wrong, in a sentence.
	Detail string
}

func (v PurposeViolation) String() string {
	return fmt.Sprintf("%s: purpose %s: %s", v.Name, v.Rule, v.Detail)
}

// PurposeResolver reports whether a NotFor.Use target names something
// that exists: a bare registered operator, or a "<kind>:<name>" feature.
type PurposeResolver func(use string) bool

// BuiltinPurposeResolver resolves a NotFor.Use against the full
// built-in registry: a bare name must be a registered built-in surface
// (any PurposeSurfaces member), a prefixed name a row of the feature
// table.
func BuiltinPurposeResolver() PurposeResolver {
	names := map[string]bool{}
	for _, s := range PurposeSurfaces() {
		for _, n := range s.Names {
			names[n] = true
		}
	}
	return func(use string) bool {
		if strings.Contains(use, ":") {
			_, ok := LookupFeature(use)
			return ok
		}
		return names[use]
	}
}

// ValidatePurpose checks p, declared for name, against every validity
// rule and returns the violations (nil when valid). resolve judges
// NotFor.Use targets; a nil resolver rejects every target. The built-in
// gates and the extension registration path share this one function.
func ValidatePurpose(name string, p descriptor.Purpose, resolve PurposeResolver) []PurposeViolation {
	var out []PurposeViolation
	bad := func(rule PurposeRule, format string, args ...any) {
		out = append(out, PurposeViolation{Name: name, Rule: rule, Detail: fmt.Sprintf(format, args...)})
	}

	// Limits.
	switch n := utf8.RuneCountInString(p.Plain); {
	case strings.TrimSpace(p.Plain) == "":
		bad(PurposeRulePlain, "Plain is empty")
	case n > PurposePlainMax:
		bad(PurposeRulePlain, "Plain is %d characters, limit %d", n, PurposePlainMax)
	}
	if len(p.Intents) == 0 {
		bad(PurposeRuleIntents, "declares no intents")
	}
	nonEmptyQ := 0
	for i, q := range p.Questions {
		if strings.TrimSpace(q) == "" {
			bad(PurposeRuleQuestions, "Questions[%d] is empty", i)
			continue
		}
		nonEmptyQ++
	}
	if nonEmptyQ < PurposeQuestionsMin {
		bad(PurposeRuleQuestions, "has %d questions, want at least %d", nonEmptyQ, PurposeQuestionsMin)
	}
	if len(p.NotFor) == 0 {
		bad(PurposeRuleNotFor, "declares no NotFor alternative")
	}
	if len(p.UseCases) == 0 {
		bad(PurposeRuleUseCases, "declares no UseCases")
	}
	for _, d := range sortedDomains(p.UseCases) {
		if !isDomain(d) {
			bad(PurposeRuleUseCases, "UseCases key %q is not a domain", d)
		}
		if strings.TrimSpace(p.UseCases[d]) == "" {
			bad(PurposeRuleUseCases, "UseCases[%q] is empty", d)
		}
	}
	if !isLevel(p.Level) {
		bad(PurposeRuleLevel, "Level %q is not one of basic, intermediate, advanced", p.Level)
	}

	// Alternatives.
	for i, a := range p.NotFor {
		if strings.TrimSpace(a.When) == "" {
			bad(PurposeRuleAlternative, "NotFor[%d] has an empty When", i)
		}
		switch {
		case strings.TrimSpace(a.Use) == "":
			bad(PurposeRuleAlternative, "NotFor[%d] has an empty Use", i)
		case a.Use == name:
			bad(PurposeRuleAlternative, "NotFor[%d] names the operator itself", i)
		case resolve == nil || !resolve(a.Use):
			bad(PurposeRuleAlternative, "NotFor[%d].Use %q does not resolve to a registered operator or feature", i, a.Use)
		}
	}

	// Intents.
	seenIntent := map[string]bool{}
	for _, id := range p.Intents {
		if !IsIntent(id) {
			bad(PurposeRuleIntentUnknown, "intent %q is not in the taxonomy", id)
		}
		if seenIntent[id] {
			bad(PurposeRuleIntentUnknown, "intent %q listed twice", id)
		}
		seenIntent[id] = true
	}

	// Glossary links.
	linked := map[string]bool{}
	for _, id := range p.Glossary {
		if !IsGlossaryTerm(id) {
			bad(PurposeRuleGlossaryUnknown, "Glossary %q is not a glossary term", id)
		}
		if linked[id] {
			bad(PurposeRuleGlossaryUnknown, "Glossary %q listed twice", id)
		}
		linked[id] = true
	}
	for _, id := range jargonTermsIn(p.Plain, JargonForms()) {
		if !linked[id] {
			bad(PurposeRuleJargonUnlinked, "Plain uses jargon term %q without listing it in Glossary", id)
		}
	}
	return out
}

func sortedDomains(m map[descriptor.Domain]string) []descriptor.Domain {
	out := make([]descriptor.Domain, 0, len(m))
	for d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func isDomain(d descriptor.Domain) bool {
	switch d {
	case descriptor.DomainSurvey, descriptor.DomainOps, descriptor.DomainScience, descriptor.DomainHarness:
		return true
	}
	return false
}

func isLevel(l descriptor.Level) bool {
	switch l {
	case descriptor.LevelBasic, descriptor.LevelIntermediate, descriptor.LevelAdvanced:
		return true
	}
	return false
}

// jargonTermsIn returns the sorted, de-duplicated term IDs whose forms
// (forms maps a lowercase surface form to its term ID) occur in text,
// case-insensitively and on word boundaries. Longer forms win: a span
// matched by "partial eta squared" is not also counted as "eta squared".
func jargonTermsIn(text string, forms map[string]string) []string {
	lower := strings.ToLower(text)
	ordered := make([]string, 0, len(forms))
	for f := range forms {
		ordered = append(ordered, f)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) != len(ordered[j]) {
			return len(ordered[i]) > len(ordered[j])
		}
		return ordered[i] < ordered[j]
	})
	taken := make([]bool, len(lower))
	found := map[string]bool{}
	for _, f := range ordered {
		if f == "" {
			continue
		}
		for start := 0; start <= len(lower)-len(f); {
			i := strings.Index(lower[start:], f)
			if i < 0 {
				break
			}
			i += start
			end := i + len(f)
			start = i + 1
			if !wordBoundaryBefore(lower, i) || !wordBoundaryAfter(lower, end) || spanTaken(taken, i, end) {
				continue
			}
			for k := i; k < end; k++ {
				taken[k] = true
			}
			found[forms[f]] = true
		}
	}
	out := make([]string, 0, len(found))
	for id := range found {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func wordBoundaryBefore(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return !isWordRune(r)
}

func wordBoundaryAfter(s string, end int) bool {
	if end >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[end:])
	return !isWordRune(r)
}

func spanTaken(taken []bool, i, end int) bool {
	for k := i; k < end; k++ {
		if taken[k] {
			return true
		}
	}
	return false
}

// PurposeSurface is one category of built-in manifest entries a Purpose
// may be declared for, with its registry keys.
type PurposeSurface struct {
	// Category is the coverage-report grouping (e.g. "aggregator").
	Category string
	// Names lists the category's registry keys, sorted.
	Names []string
}

// PurposeSurfaces returns every built-in name a Purpose may be keyed by,
// grouped into the ten categories in a stable order. Tests are keyed by
// family (one Purpose covers both tiers), so post-test variants
// contribute their family.
func PurposeSurfaces() []PurposeSurface {
	opNames := func(ops []descriptor.Operator) []string {
		out := make([]string, 0, len(ops))
		for _, o := range ops {
			out = append(out, o.Name)
		}
		return out
	}
	families := map[string]bool{}
	for _, tm := range append(testCapabilities(), postTestCapabilities()...) {
		key := tm.Family
		if key == "" {
			key = tm.Name
		}
		families[key] = true
	}
	var tests []string
	for f := range families {
		tests = append(tests, f)
	}
	var regs, overlays, dists []string
	for _, r := range regressionCapabilities() {
		regs = append(regs, r.Name)
	}
	for _, o := range OverlayCapabilities() {
		overlays = append(overlays, string(o.Kind))
	}
	for _, d := range distributionCapabilities() {
		dists = append(dists, d.Name)
	}
	out := []PurposeSurface{
		{"aggregator", opNames(aggregatorCapabilities())},
		{"attribute", opNames(attributeCapabilities())},
		{"filterer", opNames(filtererCapabilities())},
		{"grouper", opNames(grouperCapabilities())},
		{"window", opNames(windowCapabilities())},
		{"feature", opNames(featureCapabilities())},
		{"test", tests},
		{"regression", regs},
		{"overlay", overlays},
		{"synth_distribution", dists},
	}
	for i := range out {
		sort.Strings(out[i].Names)
	}
	return out
}

// BuiltinPurposes returns a copy of the built-in Purpose registry.
func BuiltinPurposes() map[string]descriptor.Purpose {
	out := make(map[string]descriptor.Purpose, len(builtinPurposes))
	for k, v := range builtinPurposes {
		out[k] = v
	}
	return out
}

// purposeRegistryViolations validates every entry of reg in sorted key
// order — the gates run it over builtinPurposes, the fixtures over an
// injected registry.
func purposeRegistryViolations(reg map[string]descriptor.Purpose, resolve PurposeResolver) []PurposeViolation {
	keys := make([]string, 0, len(reg))
	for k := range reg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []PurposeViolation
	for _, k := range keys {
		out = append(out, ValidatePurpose(k, reg[k], resolve)...)
	}
	return out
}

// orphanPurposes returns the sorted keys of reg that name no built-in
// surface.
func orphanPurposes(reg map[string]descriptor.Purpose, surfaces []PurposeSurface) []string {
	known := map[string]bool{}
	for _, s := range surfaces {
		for _, n := range s.Names {
			known[n] = true
		}
	}
	var out []string
	for k := range reg {
		if !known[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
