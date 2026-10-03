package descriptor

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/types"
)

// validPurposeFixture is a Purpose that passes every rule; each fixture
// arm below breaks exactly one.
func validPurposeFixture() descriptor.Purpose {
	return descriptor.Purpose{
		Plain:     "Reports the p-value of a fixture check.",
		Intents:   []string{IntentDescribe},
		Questions: []string{"Is it one?", "Is it two?"},
		UseCases:  map[descriptor.Domain]string{descriptor.DomainOps: "A use."},
		NotFor:    []descriptor.Alternative{{When: "something else fits", Use: "AGG_MEDIAN"}},
		Level:     descriptor.LevelBasic,
		Glossary:  []string{"p-value"},
	}
}

// purposeRuleCase breaks one rule on the fixture.
type purposeRuleCase struct {
	name   string
	rule   PurposeRule
	mutate func(*descriptor.Purpose)
}

var purposeRuleCases = []purposeRuleCase{
	// Limits — TestSkillsCoverAllPurposes.
	{"empty plain", PurposeRulePlain, func(p *descriptor.Purpose) { p.Plain = " " }},
	{"plain too long", PurposeRulePlain, func(p *descriptor.Purpose) { p.Plain = strings.Repeat("x", PurposePlainMax+1) }},
	{"no intents", PurposeRuleIntents, func(p *descriptor.Purpose) { p.Intents = nil }},
	{"one question", PurposeRuleQuestions, func(p *descriptor.Purpose) { p.Questions = p.Questions[:1] }},
	{"blank question", PurposeRuleQuestions, func(p *descriptor.Purpose) { p.Questions = []string{"Is it?", " "} }},
	{"no not-for", PurposeRuleNotFor, func(p *descriptor.Purpose) { p.NotFor = nil }},
	{"no use cases", PurposeRuleUseCases, func(p *descriptor.Purpose) { p.UseCases = nil }},
	{"unknown domain", PurposeRuleUseCases, func(p *descriptor.Purpose) {
		p.UseCases = map[descriptor.Domain]string{"finance": "A use."}
	}},
	{"blank use case", PurposeRuleUseCases, func(p *descriptor.Purpose) {
		p.UseCases = map[descriptor.Domain]string{descriptor.DomainOps: ""}
	}},
	{"level unset", PurposeRuleLevel, func(p *descriptor.Purpose) { p.Level = "" }},
	{"level unknown", PurposeRuleLevel, func(p *descriptor.Purpose) { p.Level = "expert" }},
	// Alternatives — TestPurposeAlternativesResolve.
	{"bare use unregistered", PurposeRuleAlternative, func(p *descriptor.Purpose) { p.NotFor[0].Use = "AGG_NOPE" }},
	{"prefixed use unknown", PurposeRuleAlternative, func(p *descriptor.Purpose) { p.NotFor[0].Use = "capability:nope" }},
	{"use names self", PurposeRuleAlternative, func(p *descriptor.Purpose) { p.NotFor[0].Use = "FIXTURE_OP" }},
	{"empty use", PurposeRuleAlternative, func(p *descriptor.Purpose) { p.NotFor[0].Use = "" }},
	{"empty when", PurposeRuleAlternative, func(p *descriptor.Purpose) { p.NotFor[0].When = "" }},
	// Intents — TestPurposeQuestionsResolve.
	{"unknown intent", PurposeRuleIntentUnknown, func(p *descriptor.Purpose) { p.Intents = []string{"astrology"} }},
	{"repeated intent", PurposeRuleIntentUnknown, func(p *descriptor.Purpose) {
		p.Intents = []string{IntentDescribe, IntentDescribe}
	}},
	// Glossary links — TestGlossaryTermsResolve.
	{"unknown glossary id", PurposeRuleGlossaryUnknown, func(p *descriptor.Purpose) {
		p.Glossary = append(p.Glossary, "no-such-term")
	}},
	{"repeated glossary id", PurposeRuleGlossaryUnknown, func(p *descriptor.Purpose) {
		p.Glossary = []string{"p-value", "p-value"}
	}},
	{"jargon unlinked", PurposeRuleJargonUnlinked, func(p *descriptor.Purpose) { p.Glossary = nil }},
	{"jargon unlinked case-insensitive", PurposeRuleJargonUnlinked, func(p *descriptor.Purpose) {
		p.Plain = "Reports the P Value and the Effect Size."
	}},
}

// fixtureRegistry injects one Purpose under a fixture key, the same
// registry shape the built-in gates walk.
func fixtureRegistry(p descriptor.Purpose) map[string]descriptor.Purpose {
	return map[string]descriptor.Purpose{"FIXTURE_OP": p}
}

// assertPurposeRuleFamily runs the real registry through ValidatePurpose
// (binding, for rules in family) and proves each fixture arm of family
// bites: the valid fixture passes, the broken one reports its rule.
func assertPurposeRuleFamily(t *testing.T, family ...PurposeRule) {
	t.Helper()
	in := func(r PurposeRule) bool { return slices.Contains(family, r) }
	resolve := BuiltinPurposeResolver()
	for _, v := range purposeRegistryViolations(builtinPurposes, resolve) {
		if in(v.Rule) {
			t.Error(v)
		}
	}
	if v := purposeRegistryViolations(fixtureRegistry(validPurposeFixture()), resolve); len(v) != 0 {
		t.Fatalf("valid fixture reported violations: %v", v)
	}
	ran := 0
	for _, tc := range purposeRuleCases {
		if !in(tc.rule) {
			continue
		}
		ran++
		t.Run(tc.name, func(t *testing.T) {
			p := validPurposeFixture()
			tc.mutate(&p)
			got := purposeRegistryViolations(fixtureRegistry(p), resolve)
			for _, v := range got {
				if v.Rule == tc.rule && v.Name == "FIXTURE_OP" {
					return
				}
			}
			t.Errorf("want a %q violation, got %v", tc.rule, got)
		})
	}
	if ran == 0 {
		t.Fatalf("no fixture arm for rule family %v", family)
	}
}

// TestSkillsCoverAllPurposes is the two-tier Purpose gate. Validity
// (binding): every declared built-in Purpose meets the limits — Plain
// at most 140 characters, at least one intent, two Questions, one
// NotFor, one UseCase, and a Level — and every registry key names a
// registered built-in. Coverage (binding): every registered built-in
// declares a Purpose or holds a purposeExemptions entry; the full
// missing list is logged, grouped by category.
func TestSkillsCoverAllPurposes(t *testing.T) {
	assertPurposeRuleFamily(t,
		PurposeRulePlain, PurposeRuleIntents, PurposeRuleQuestions,
		PurposeRuleNotFor, PurposeRuleUseCases, PurposeRuleLevel)

	if orphans := orphanPurposes(builtinPurposes, PurposeSurfaces()); len(orphans) != 0 {
		t.Errorf("Purpose declared for unregistered names: %v", orphans)
	}

	missing, declared, total := 0, 0, 0
	var report strings.Builder
	for _, s := range PurposeSurfaces() {
		var lack []string
		for _, n := range s.Names {
			total++
			if _, ok := builtinPurposes[n]; ok {
				declared++
				continue
			}
			lack = append(lack, n)
		}
		missing += len(lack)
		if len(lack) > 0 {
			report.WriteString("\n  " + s.Category + " (" + strconv.Itoa(len(lack)) + "): " + strings.Join(lack, ", "))
		}
	}
	t.Logf("Purpose coverage: %d of %d built-ins declare one; %d lack a Purpose:%s", declared, total, missing, report.String())
	assertExempted(t, "purpose", purposeCoverageGaps(builtinPurposes), purposeExemptions, roadmapUnitStatus())
}

// purposeCoverageGaps returns every registered built-in reg declares no
// Purpose for.
func purposeCoverageGaps(reg map[string]descriptor.Purpose) []string {
	var out []string
	for _, s := range PurposeSurfaces() {
		for _, n := range s.Names {
			if _, ok := reg[n]; !ok {
				out = append(out, n)
			}
		}
	}
	return out
}

// assertExempted fails on every gap the ledger does not exempt and on
// every ledger problem (unjustified, ownerless, unknown or done owner,
// duplicate, stale).
func assertExempted(t *testing.T, table string, gaps []string, ledger []guidanceExemption, status unitStatusReader) {
	t.Helper()
	remaining, problems := applyExemptions(table, gaps, ledger, status)
	for _, p := range problems {
		t.Error(p)
	}
	for _, g := range remaining {
		t.Errorf("%s coverage gap %s is neither covered nor exempted (guidance_exemptions_test.go)", table, g)
	}
}

// TestPurposeRegistry_OrphanKeysDetected: a Purpose keyed by a name no
// built-in surface registers is reported.
func TestPurposeRegistry_OrphanKeysDetected(t *testing.T) {
	reg := map[string]descriptor.Purpose{"AGG_AVERAGE": {}, "AGG_GHOST": {}}
	if got := orphanPurposes(reg, PurposeSurfaces()); !slices.Equal(got, []string{"AGG_GHOST"}) {
		t.Errorf("orphanPurposes = %v, want [AGG_GHOST]", got)
	}
}

// TestPurposeAlternativesResolve (binding): every NotFor entry has a
// When and a Use that resolves — a bare name to a registered built-in,
// a "<kind>:<name>" spelling to a feature-table row — and never names
// the operator itself.
func TestPurposeAlternativesResolve(t *testing.T) {
	assertPurposeRuleFamily(t, PurposeRuleAlternative)

	resolve := BuiltinPurposeResolver()
	for _, ok := range []string{"AGG_MEDIAN", "TEST_WELCH", "REG_OLS", "OVERLAY_SHARE_OF_TOTAL", "capability:crosstab", "io_format:csv"} {
		if !resolve(ok) {
			t.Errorf("resolver rejected %q", ok)
		}
	}
	for _, bad := range []string{"AGG_NOPE", "capability:nope", "crosstab", "operator:AGG_MEDIAN", ""} {
		if resolve(bad) {
			t.Errorf("resolver accepted %q", bad)
		}
	}
	if v := ValidatePurpose("X", validPurposeFixture(), nil); len(v) != 1 || v[0].Rule != PurposeRuleAlternative {
		t.Errorf("nil resolver: want exactly one alternative violation, got %v", v)
	}
}

// TestPurposeQuestionsResolve. Validity (binding): every intent a
// Purpose declares is in the taxonomy, once. Coverage (binding): every
// intent is declared by at least intentMinDeclarers built-ins AND tagged
// by at least one example's _meta.intents, unless intentDeclarerExemptions
// / intentExampleExemptions lists the gap.
func TestPurposeQuestionsResolve(t *testing.T) {
	assertPurposeRuleFamily(t, PurposeRuleIntentUnknown)

	thin, untagged := intentCoverageGaps(builtinPurposes, examples.Intents())
	t.Logf("Intent coverage: %d intents declared by fewer than %d operators: %s", len(thin), intentMinDeclarers, strings.Join(thin, ", "))
	t.Logf("Intent coverage: %d intents with no tagged example: %s", len(untagged), strings.Join(untagged, ", "))
	status := roadmapUnitStatus()
	assertExempted(t, "intent-declarers", thin, intentDeclarerExemptions, status)
	assertExempted(t, "intent-example", untagged, intentExampleExemptions, status)
}

// intentMinDeclarers is how many built-in Purposes must declare an intent.
const intentMinDeclarers = 3

// intentCoverageGaps returns the intent IDs fewer than intentMinDeclarers
// purposes declare (thin) and those no example tags (untagged).
func intentCoverageGaps(purposes map[string]descriptor.Purpose, exampleIntents map[string][]string) (thin, untagged []string) {
	declaredBy := map[string]int{}
	for _, p := range purposes {
		for _, id := range p.Intents {
			declaredBy[id]++
		}
	}
	tagged := map[string]bool{}
	for _, ids := range exampleIntents {
		for _, id := range ids {
			tagged[id] = true
		}
	}
	for _, id := range IntentIDs() {
		if declaredBy[id] < intentMinDeclarers {
			thin = append(thin, id)
		}
		if !tagged[id] {
			untagged = append(untagged, id)
		}
	}
	return thin, untagged
}

// testPurposeGlossaryLinks is the Purpose half of TestGlossaryTermsResolve
// (which calls it): Glossary IDs resolve, once, and every jargon term
// whose Form appears in Plain is listed in Glossary.
func testPurposeGlossaryLinks(t *testing.T) {
	assertPurposeRuleFamily(t, PurposeRuleGlossaryUnknown, PurposeRuleJargonUnlinked)
}

// TestJargonTermsIn_LongestMatchWordBoundary: forms match
// case-insensitively on word boundaries, and an overlapping shorter
// form inside a longer match is not counted.
func TestJargonTermsIn_LongestMatchWordBoundary(t *testing.T) {
	forms := JargonForms()
	cases := []struct {
		text string
		want []string
	}{
		{"Reports Partial Eta Squared.", []string{"partial-eta-squared"}},
		{"eta squared and partial eta squared", []string{"eta-squared", "partial-eta-squared"}},
		{"The p-value, alpha.", []string{"alpha", "p-value"}},
		{"alphabet soup", nil},
		{"rephrase the graph", nil},                  // "phi"/"rake" never inside a word
		{"chi-squared test", []string{"chi-square"}}, // chi-square form does not split chi-squared
		{"the 'skew' matters", []string{"skew"}},
		{"cohen's d and cohens_d", []string{"cohens-d"}},
		{"", nil},
	}
	for _, tc := range cases {
		got := jargonTermsIn(tc.text, forms)
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("jargonTermsIn(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// TestPurposeExemplars: the three exemplar purposes are declared, valid
// under every rule, and their intents ride the manifest entries.
func TestPurposeExemplars(t *testing.T) {
	want := map[string]string{
		"AGG_AVERAGE":    IntentDescribe,
		"TEST_ANOVA_F":   IntentCompareGroups,
		"TEST_PEARSON_R": IntentRelationship,
	}
	resolve := BuiltinPurposeResolver()
	for name, intent := range want {
		p, ok := PurposeOf(name)
		if !ok {
			t.Errorf("%s declares no Purpose", name)
			continue
		}
		if v := ValidatePurpose(name, p, resolve); len(v) != 0 {
			t.Errorf("%s: %v", name, v)
		}
		if !slices.Contains(p.Intents, intent) {
			t.Errorf("%s intents = %v, want %q among them", name, p.Intents, intent)
		}
	}
	m := BuildManifest()
	for _, op := range m.Components.Aggregators {
		if op.Name == "AGG_AVERAGE" && !slices.Equal(op.Intents, []string{IntentDescribe}) {
			t.Errorf("manifest AGG_AVERAGE intents = %v", op.Intents)
		}
	}
	for _, tm := range append(append([]descriptor.TestMeta{}, m.Tests...), m.PostTests...) {
		if w, ok := want[tm.Family]; ok && !slices.Equal(tm.Intents, []string{w}) {
			t.Errorf("manifest %s intents = %v, want [%s]", tm.Name, tm.Intents, w)
		}
	}
}

// TestPurposeSurfaces_CoverEveryCategory: the ten categories are
// present, non-empty and sorted; tests are keyed by family.
func TestPurposeSurfaces_CoverEveryCategory(t *testing.T) {
	want := []string{"aggregator", "attribute", "filterer", "grouper", "window", "feature", "test", "regression", "overlay", "synth_distribution"}
	var got []string
	for _, s := range PurposeSurfaces() {
		got = append(got, s.Category)
		if len(s.Names) == 0 {
			t.Errorf("category %q has no names", s.Category)
		}
		if !sort.StringsAreSorted(s.Names) {
			t.Errorf("category %q names not sorted", s.Category)
		}
		if s.Category == "test" {
			for _, n := range s.Names {
				if strings.Contains(n, "/") {
					t.Errorf("test surface carries a variant name %q, want families only", n)
				}
			}
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("categories = %v, want %v", got, want)
	}
}

// TestExamples_IntentsFromTaxonomy (binding): every example's optional
// _meta.intents value is an intent-taxonomy ID; the fixture arm proves
// an unknown value is reported.
func TestExamples_IntentsFromTaxonomy(t *testing.T) {
	for _, p := range exampleIntentProblems(examples.Intents()) {
		t.Error(p)
	}
	bad := map[string][]string{
		"ok":  {IntentDescribe},
		"bad": {IntentDescribe, "astrology"},
	}
	got := exampleIntentProblems(bad)
	if len(got) != 1 || !strings.Contains(got[0], `"bad"`) || !strings.Contains(got[0], "astrology") {
		t.Errorf("exampleIntentProblems = %v, want one problem naming example \"bad\" and \"astrology\"", got)
	}
}

// exampleIntentProblems reports every _meta.intents value (keyed by
// example name) that is not an intent ID, in name order.
func exampleIntentProblems(byExample map[string][]string) []string {
	names := make([]string, 0, len(byExample))
	for n := range byExample {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		for _, id := range byExample[n] {
			if !IsIntent(id) {
				out = append(out, "example \""+n+"\": _meta.intents value \""+id+"\" is not an intent ID")
			}
		}
	}
	return out
}

// TestBuiltinPurposes_AssembledFromCategoryMaps: builtinPurposes is
// exactly the union of the per-category maps, every stat-test entry is
// keyed by a TEST_* family, and a name declared by two category maps
// panics instead of silently shadowing one declaration.
func TestBuiltinPurposes_AssembledFromCategoryMaps(t *testing.T) {
	cats := []map[string]descriptor.Purpose{aggregatorPurposes, attributePurposes, statTestPurposes, overlayPurposes, regressionPurposes}
	total := 0
	for _, m := range cats {
		total += len(m)
		for name := range m {
			if _, ok := builtinPurposes[name]; !ok {
				t.Errorf("category entry %s missing from builtinPurposes", name)
			}
		}
	}
	if total != len(builtinPurposes) {
		t.Errorf("builtinPurposes has %d entries, category maps hold %d", len(builtinPurposes), total)
	}
	for name := range statTestPurposes {
		if !strings.HasPrefix(name, "TEST_") {
			t.Errorf("statTestPurposes holds non-test key %s", name)
		}
	}

	for name := range overlayPurposes {
		if !strings.HasPrefix(name, "OVERLAY_") {
			t.Errorf("overlayPurposes holds non-overlay key %s", name)
		}
	}
	for name := range regressionPurposes {
		if !strings.HasPrefix(name, "REG_") {
			t.Errorf("regressionPurposes holds non-regression key %s", name)
		}
	}

	merged := mergePurposes(map[string]descriptor.Purpose{"A": {Plain: "a"}}, map[string]descriptor.Purpose{"B": {Plain: "b"}})
	if len(merged) != 2 || merged["A"].Plain != "a" || merged["B"].Plain != "b" {
		t.Errorf("mergePurposes = %v", merged)
	}
	defer func() {
		if recover() == nil {
			t.Error("mergePurposes accepted a name declared by two category maps")
		}
	}()
	mergePurposes(map[string]descriptor.Purpose{"A": {}}, map[string]descriptor.Purpose{"A": {}})
}

// TestOverlayPurposes_CoverEveryKind: every registered overlay kind
// declares a Purpose (U08 FR-23), so the coverage report lists no
// overlay. The kind list comes from the registry, never a hardcoded count.
func TestOverlayPurposes_CoverEveryKind(t *testing.T) {
	kinds := types.AllOverlayKinds()
	if len(kinds) == 0 {
		t.Fatal("no overlay kinds registered")
	}
	for _, k := range kinds {
		if _, ok := overlayPurposes[string(k)]; !ok {
			t.Errorf("overlay kind %s declares no Purpose", k)
		}
	}
	if len(overlayPurposes) != len(kinds) {
		t.Errorf("overlayPurposes has %d entries, %d overlay kinds registered", len(overlayPurposes), len(kinds))
	}
	if got := len(intentsOf(string(kinds[0]))); got == 0 {
		t.Errorf("overlay %s projects no intents", kinds[0])
	}
}
