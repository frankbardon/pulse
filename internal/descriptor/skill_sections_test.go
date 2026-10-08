package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// sectionFixture is an atomic skill carrying both generated-section
// markers next to a feature fence and a `## See` section, so one render
// exercises every pass a served body goes through.
const sectionFixture = `---
name: op-test-welch
description: Welch two-sample t-test.
kind: operator
category: TEST
operator: TEST_WELCH
type: reference
---

Two-sample t-test without the equal-variance assumption.

<!-- generated: use-when -->

## Params

<!-- feature: capability:multiplicity -->
Adjusted p-values ride beside the raw one.
<!-- /feature -->

## Inputs

Two groups.

## Output

A test result.

<!-- generated: reading-the-output -->

## Gotchas

None.

## See

- Skills: ` + "`op-test-t`, `op-test-paired-t`" + `
`

// welchAlternatives are TEST_WELCH's not-for targets.
var welchAlternatives = []string{"TEST_PAIRED_T", "TEST_MANN_WHITNEY_U", "TEST_ANOVA_WELCH", "TEST_T", "TEST_KS"}

// section returns the `## <heading>` section of body (heading included,
// up to the next `## ` heading), trailing blank lines trimmed.
func section(t *testing.T, body, heading string) string {
	t.Helper()
	start := strings.Index(body, "\n## "+heading+"\n")
	if start < 0 {
		t.Fatalf("no ## %s section in:\n%s", heading, body)
	}
	rest := body[start+1:]
	if end := strings.Index(rest[3:], "\n## "); end >= 0 {
		rest = rest[:end+3]
	}
	return strings.TrimRight(rest, "\n")
}

func assertWelchSections(t *testing.T, out string) {
	t.Helper()
	if strings.Contains(out, "<!-- generated") {
		t.Errorf("a generated marker survived the render:\n%s", out)
	}
	for _, h := range []string{"## Use when", "## Reading the output", "## Params", "## Output", "## See"} {
		if !skills.HasHeading(out, h) {
			t.Errorf("HasHeading(%q) = false", h)
		}
	}
	p, _ := PurposeOf("TEST_WELCH")
	use := section(t, out, "Use when")
	if !strings.Contains(use, p.Plain) || !strings.Contains(use, p.Questions[0]) {
		t.Errorf("Use when lacks the plain line or first question:\n%s", use)
	}
	if len(use) > UseWhenSectionCap {
		t.Errorf("Use when is %d bytes, over the %d cap", len(use), UseWhenSectionCap)
	}
	read := section(t, out, "Reading the output")
	if !strings.Contains(read, "`statistic`: Welch's t") || !strings.Contains(read, "`p_value`: "+pValueBrief) {
		t.Errorf("Reading the output lacks statistic / shared p-value lines:\n%s", read)
	}
	if len(read) > ReadingSectionCap {
		t.Errorf("Reading the output is %d bytes, over the %d cap", len(read), ReadingSectionCap)
	}
	// Placement: each section replaces its marker.
	if !(strings.Index(out, "## Use when") < strings.Index(out, "## Params") &&
		strings.Index(out, "## Output") < strings.Index(out, "## Reading the output") &&
		strings.Index(out, "## Reading the output") < strings.Index(out, "## Gotchas")) {
		t.Errorf("sections out of place:\n%s", out)
	}
	if !strings.HasPrefix(strings.TrimSpace(skills.StripFrontmatter(out)), "Two-sample t-test") {
		t.Errorf("StripFrontmatter lost the lead sentence:\n%s", skills.StripFrontmatter(out))
	}
}

// TestSkillSections_FullRender: the profile-free path (skills.Get →
// RenderFull, the renderer registered at init) renders both sections
// from the full registries, keeps every fence and every See item.
func TestSkillSections_FullRender(t *testing.T) {
	out := skills.RenderFull(sectionFixture)
	assertWelchSections(t, out)
	// The cap admits the first alternative; the rest overflow.
	if !strings.Contains(out, "Use something else:") || !strings.Contains(out, "`TEST_PAIRED_T` when ") {
		t.Errorf("full render lacks the first not-for alternative:\n%s", section(t, out, "Use when"))
	}
	if !strings.Contains(out, "Adjusted p-values ride beside the raw one.") || strings.Contains(out, "<!-- feature") {
		t.Error("full render must keep the fence body and strip its markers")
	}
	if !strings.Contains(out, "- Skills: `op-test-t`, `op-test-paired-t`") {
		t.Error("full render changed the See section")
	}
}

// TestSkillSections_ProfiledRender: an instance hiding a not-for target
// renders the section without it (and still prunes fences and See
// items around the generated text); hiding every target drops the
// not-for list.
func TestSkillSections_ProfiledRender(t *testing.T) {
	d := hidingSnapshot("TEST_PAIRED_T", "capability:multiplicity").Discovery()
	out := d.renderBody("op-test-welch", sectionFixture)
	assertWelchSections(t, out)
	if strings.Contains(out, "TEST_PAIRED_T") {
		t.Errorf("hidden not-for target TEST_PAIRED_T rendered:\n%s", section(t, out, "Use when"))
	}
	// The next visible alternative takes the hidden one's place.
	if !strings.Contains(out, "`TEST_MANN_WHITNEY_U` when ") {
		t.Error("visible not-for target TEST_MANN_WHITNEY_U not rendered")
	}
	if strings.Contains(out, "Adjusted p-values") {
		t.Error("hidden fence survived")
	}
	if !strings.Contains(out, "- Skills: `op-test-t`\n") {
		t.Errorf("See item for the pruned skill not cut:\n%s", section(t, out, "See"))
	}

	all := hidingSnapshot(welchAlternatives...).Discovery()
	out = all.renderBody("op-test-welch", sectionFixture)
	use := section(t, out, "Use when")
	if strings.Contains(use, "Use something else") {
		t.Errorf("every alternative hidden, yet the not-for list rendered:\n%s", use)
	}
	for _, alt := range welchAlternatives {
		if strings.Contains(out, alt) {
			t.Errorf("hidden alternative %s rendered", alt)
		}
	}
	if !strings.Contains(use, "Questions it answers:") {
		t.Errorf("Use when lost its questions:\n%s", use)
	}
}

// TestSkillSections_ScrubsHiddenNames: prose naming a hidden operator
// goes through the instance scrub — the sentence (or the whole item) is
// dropped, the plain line kept.
func TestSkillSections_ScrubsHiddenNames(t *testing.T) {
	scrub := NewProseScrub(hidingSnapshot("TEST_KS"))
	p := descriptor.Purpose{
		Plain:     "Compares two groups. Pairs with TEST_KS for shape.",
		Questions: []string{"Does TEST_KS agree?", "Do the groups differ?"},
		NotFor: []descriptor.Alternative{
			{When: "you would rather run TEST_KS", Use: "TEST_T"},
			{When: "the groups are paired", Use: "TEST_PAIRED_T"},
		},
	}
	got := RenderUseWhenSection(p, nil, scrub)
	if strings.Contains(got, "TEST_KS") {
		t.Errorf("hidden name rendered:\n%s", got)
	}
	for _, want := range []string{"Compares two groups.", "Do the groups differ?", "`TEST_PAIRED_T` when the groups are paired."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "`TEST_T`") {
		t.Errorf("an alternative whose When was scrubbed empty must go:\n%s", got)
	}
	ins := []descriptor.Interpretation{{Field: "statistic", Means: "A gap. Unlike TEST_KS it reads means.", Caveats: []string{"Read TEST_KS too."}}}
	read := RenderReadingSection(ins, scrub)
	if strings.Contains(read, "TEST_KS") || !strings.Contains(read, "`statistic`: A gap.") || strings.Contains(read, "Caveat") {
		t.Errorf("reading not scrubbed:\n%s", read)
	}
}

// TestSkillSections_Overflow: a Purpose / Interpretation set far over
// the caps renders within them, keeping the plain line (and the first
// field), never failing.
func TestSkillSections_Overflow(t *testing.T) {
	long := strings.Repeat("very long words ", 12)
	p := descriptor.Purpose{
		Plain:     "Short plain line that must survive.",
		Questions: []string{long + "one?", "Short second question?", long + "three?"},
	}
	for i := 0; i < 10; i++ {
		p.NotFor = append(p.NotFor, descriptor.Alternative{When: long, Use: "TEST_T"})
	}
	p.NotFor = append(p.NotFor, descriptor.Alternative{When: "a short case", Use: "TEST_KS"})
	got := RenderUseWhenSection(p, nil, ProseScrub{})
	if len(got) > UseWhenSectionCap {
		t.Errorf("Use when is %d bytes, over %d:\n%s", len(got), UseWhenSectionCap, got)
	}
	for _, want := range []string{p.Plain, "Short second question?", "`TEST_KS` when a short case."} {
		if !strings.Contains(got, want) {
			t.Errorf("overflow render lacks %q (a later item that fits is still added):\n%s", want, got)
		}
	}
	if strings.Contains(got, "three?") {
		t.Errorf("only %d questions may render:\n%s", useWhenQuestions, got)
	}

	huge := descriptor.Purpose{Plain: strings.Repeat("x", UseWhenSectionCap+50)}
	if got := RenderUseWhenSection(huge, nil, ProseScrub{}); !strings.Contains(got, huge.Plain) {
		t.Error("the plain line is kept even when it alone overflows")
	}

	var ins []descriptor.Interpretation
	for i := 0; i < 10; i++ {
		ins = append(ins, descriptor.Interpretation{
			Field: "details.f" + string(rune('a'+i)), Means: long,
			Sign: map[string]string{"+": long, "-": long}, Caveats: []string{long, long},
		})
	}
	read := RenderReadingSection(ins, ProseScrub{})
	if len(read) > ReadingSectionCap {
		t.Errorf("Reading is %d bytes, over %d:\n%s", len(read), ReadingSectionCap, read)
	}
	if !strings.Contains(read, "`details.fa`: ") {
		t.Errorf("the first field must survive:\n%s", read)
	}
}

// TestSkillSections_Bands: bands render with the convention named and
// the absolute-value note.
func TestSkillSections_Bands(t *testing.T) {
	lo, hi := 0.2, 0.5
	in := descriptor.Interpretation{
		Field: "details.effect_size.d", Means: "A gap.", Convention: "Cohen (1988)", Abs: true,
		Bands: []descriptor.Band{{Max: &lo, Label: "negligible"}, {Min: &lo, Max: &hi, Label: "small"}, {Min: &hi, Label: "large"}},
		Sign:  map[string]string{"+": "first higher", "-": "second higher"},
	}
	got := RenderReadingSection([]descriptor.Interpretation{in}, ProseScrub{})
	for _, want := range []string{
		"Bands (Cohen (1988), absolute value): negligible below 0.2; small 0.2 to 0.5; large 0.5 and above.",
		"Sign: positive means first higher; negative means second higher.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// TestSkillSections_UnknownOperatorRemovesMarkers: a body whose
// operator declares no metadata (or names none) loses its markers
// silently, with no doubled blank line left behind.
func TestSkillSections_UnknownOperatorRemovesMarkers(t *testing.T) {
	body := strings.Replace(sectionFixture, "operator: TEST_WELCH", "operator: AGG_NOPE", 1)
	out := skills.RenderFull(body)
	if strings.Contains(out, "<!-- generated") || skills.HasHeading(out, "## Use when") || strings.Contains(out, "\n\n\n") {
		t.Errorf("markers not removed cleanly:\n%s", out)
	}
	if d := hidingSnapshot("TEST_KS").Discovery(); d.renderBody("op-x", body) != skills.RenderGenerated(mustFences(t, body), nil) {
		t.Error("profiled render of a metadata-less body must equal the marker-stripped body")
	}
}

func mustFences(t *testing.T, body string) string {
	t.Helper()
	out, err := skills.RenderFences(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSkillSections_BuiltinsWithinCapsAndLint: every built-in
// operator's sections, rendered from the full set, fit their caps and
// trip no prose-lint text rule (the glue text is new prose; the
// sources are already linted).
func TestSkillSections_BuiltinsWithinCapsAndLint(t *testing.T) {
	names := map[string]bool{}
	for n := range builtinPurposes {
		names[n] = true
	}
	for n := range builtinInterpretations {
		names[n] = true
	}
	for n := range names {
		for _, sec := range skills.GeneratedSections() {
			got := RenderGuidanceSection(n, sec, nil, nil, ProseScrub{})
			limit := UseWhenSectionCap
			if sec == skills.SectionReadingTheOutput {
				limit = ReadingSectionCap
			}
			if len(got) > limit {
				p, _ := PurposeOf(n)
				if sec == skills.SectionReadingTheOutput || len(p.Plain)+len("## Use when\n\n") < limit {
					t.Errorf("%s %s: %d bytes, over %d", n, sec, len(got), limit)
				}
			}
			for _, h := range textHits(got) {
				t.Errorf("%s %s: lint %s on %q", n, sec, h.rule, h.span)
			}
		}
		if _, ok := builtinPurposes[n]; ok && RenderGuidanceSection(n, skills.SectionUseWhen, nil, nil, ProseScrub{}) == "" {
			t.Errorf("%s: declares a Purpose but renders no Use when", n)
		}
	}
	if hits := textHits(pValueBrief); len(hits) > 0 {
		t.Errorf("pValueBrief trips the lint: %v", hits)
	}
}

// TestSkillSections_HiddenCapabilityTarget: a not-for target spelled as
// a capability (which the prose scrub does not tokenise) is dropped by
// the pruned-graph check alone.
func TestSkillSections_HiddenCapabilityTarget(t *testing.T) {
	body := "---\nname: op-agg-mode-count\noperator: AGG_MODE_COUNT\n---\n\n<!-- generated: use-when -->\n"
	shown := hidingSnapshot("GROUP_CATEGORY").Discovery().renderGenerated(body)
	if !strings.Contains(shown, "`capability:facet` when ") {
		t.Fatalf("visible capability target not rendered:\n%s", shown)
	}
	hidden := hidingSnapshot("GROUP_CATEGORY", "capability:facet").Discovery().renderGenerated(body)
	if strings.Contains(hidden, "capability:facet") || !strings.Contains(hidden, "## Use when") {
		t.Errorf("hidden capability target rendered:\n%s", hidden)
	}
}
