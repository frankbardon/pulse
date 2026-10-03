package descriptor

import (
	stderrors "errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/skills"
)

// extSkillAtomic is a valid embedder atomic skill for AGG_ACME_TRIM.
const extSkillAtomic = `---
name: op-agg-acme-trim
description: Trimmed mean that drops both tails before averaging.
kind: operator
category: AGG
operator: AGG_ACME_TRIM
type: reference
applies_to: process
---

# AGG_ACME_TRIM

## Params
` + "`trim`" + ` — the fraction cut from each tail.

## Inputs
A numeric field. Builds on AGG_SUM.

## Output
A float64.

## Components
The universal floor only.

## Gotchas
A trim of 0.5 or more leaves nothing.

## See
` + "`ext-acme-guide`, `aggregation-design`" + `
`

// extSkillTopical is a valid embedder topical skill.
const extSkillTopical = `---
name: ext-acme-guide
description: How the Acme operators fit a survey workflow.
kind: design
type: guide
applies_to: process
---

# Acme guide

Skewed scores want a robust centre<!-- feature: AGG_ACME_TRIM --> — the trimmed mean (` + "`AGG_ACME_TRIM`" + `)<!-- /feature -->.
`

func extSkillSnapshot() *ExtensionsSnapshot {
	return &ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{{Name: "AGG_ACME_TRIM"}, {Name: "AGG_ACME_OTHER"}},
	}
}

// extSkillDeps: AGG_ACME_TRIM depends on AGG_SUM (a hard AND edge), so
// its skill may name AGG_SUM bare.
var extSkillDeps = map[string][]string{"AGG_ACME_TRIM": {"AGG_SUM"}, "AGG_ACME_OTHER": nil}

func extSkillFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestLoadExtensionSkills_Valid(t *testing.T) {
	got, err := LoadExtensionSkills(extSkillFS(map[string]string{
		"op-agg-acme-trim.md": extSkillAtomic,
		"ext-acme-guide.md":   extSkillTopical,
	}), extSkillSnapshot(), extSkillDeps)
	if err != nil {
		t.Fatalf("LoadExtensionSkills: %v", err)
	}
	if len(got) != 2 || got[0].Metadata.Name != "ext-acme-guide" || got[1].Metadata.Name != "op-agg-acme-trim" {
		t.Fatalf("skills = %+v, want ext-acme-guide, op-agg-acme-trim", got)
	}
	if got[1].Metadata.Operator != "AGG_ACME_TRIM" || got[1].Raw != extSkillAtomic {
		t.Errorf("atomic skill parsed wrong: %+v", got[1].Metadata)
	}
	if none, err := LoadExtensionSkills(nil, extSkillSnapshot(), extSkillDeps); none != nil || err != nil {
		t.Errorf("nil fs.FS = (%v, %v), want (nil, nil)", none, err)
	}
}

// TestLoadExtensionSkills_Refusals: every validation failure maps to its
// code and details["reason"].
func TestLoadExtensionSkills_Refusals(t *testing.T) {
	atomic := func(old, new string) string { return strings.Replace(extSkillAtomic, old, new, 1) }
	topical := func(old, new string) string { return strings.Replace(extSkillTopical, old, new, 1) }
	cases := []struct {
		name   string
		files  map[string]string
		code   errors.Code
		reason string
		skill  string
	}{
		{"non-md file", map[string]string{"notes.txt": "x"}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonLayout, "notes.txt"},
		{"subdirectory", map[string]string{"sub/ext-a.md": extSkillTopical}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonLayout, "sub"},
		{"built-in stem", map[string]string{"op-agg-count.md": extSkillAtomic}, errors.PULSE_EXTENSION_SKILL_COLLISION, SkillReasonBuiltin, "op-agg-count"},
		{"built-in topical stem", map[string]string{"aggregation-design.md": extSkillTopical}, errors.PULSE_EXTENSION_SKILL_COLLISION, SkillReasonBuiltin, "aggregation-design"},
		{"virtual stem", map[string]string{"glossary.md": extSkillTopical}, errors.PULSE_EXTENSION_SKILL_COLLISION, SkillReasonBuiltin, "glossary"},
		{"bad stem", map[string]string{"acme-guide.md": extSkillTopical}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonStem, "acme-guide"},
		{"no frontmatter", map[string]string{"ext-acme-guide.md": "# Acme\n"}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonFrontmatter, "ext-acme-guide"},
		{"name differs from stem", map[string]string{"ext-acme-guide.md": topical("name: ext-acme-guide", "name: ext-other")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonName, "ext-acme-guide"},
		{"no description", map[string]string{"ext-acme-guide.md": topical("description: How the Acme operators fit a survey workflow.", "description:")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonDescription, "ext-acme-guide"},
		{"atomic not kind operator", map[string]string{"op-agg-acme-trim.md": atomic("kind: operator", "kind: design")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonKind, "op-agg-acme-trim"},
		{"topical not kind design", map[string]string{"ext-acme-guide.md": topical("kind: design", "kind: operator")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonKind, "ext-acme-guide"},
		{"topical with operator", map[string]string{"ext-acme-guide.md": topical("kind: design", "kind: design\noperator: AGG_ACME_TRIM")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonKind, "ext-acme-guide"},
		{"unregistered operator", map[string]string{"op-agg-acme-nope.md": strings.ReplaceAll(strings.ReplaceAll(extSkillAtomic, "acme-trim", "acme-nope"), "AGG_ACME_TRIM", "AGG_ACME_NOPE")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonOperator, "op-agg-acme-nope"},
		{"built-in operator", map[string]string{"op-agg-acme-trim.md": atomic("operator: AGG_ACME_TRIM", "operator: AGG_SUM")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonOperator, "op-agg-acme-trim"},
		{"stem not the operator's", map[string]string{"op-agg-acme-trim.md": atomic("operator: AGG_ACME_TRIM", "operator: AGG_ACME_OTHER")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonStem, "op-agg-acme-trim"},
		{"category mismatch", map[string]string{"op-agg-acme-trim.md": atomic("category: AGG", "category: ATTR")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonCategory, "op-agg-acme-trim"},
		{"atomic requires", map[string]string{"op-agg-acme-trim.md": atomic("type: reference", "type: reference\nrequires: [capability:crosstab]")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonRequires, "op-agg-acme-trim"},
		{"requires unknown", map[string]string{"ext-acme-guide.md": topical("type: guide", "type: guide\nrequires: [capability:nope]")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonRequires, "ext-acme-guide"},
		{"missing section", map[string]string{"op-agg-acme-trim.md": atomic("## Components", "## Parts")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonSections, "op-agg-acme-trim"},
		{"atomic over budget", map[string]string{"op-agg-acme-trim.md": atomic("## Gotchas\n", "## Gotchas\n"+strings.Repeat("x", skills.OpBudget)+"\n")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonBudget, "op-agg-acme-trim"},
		{"topical over budget", map[string]string{"ext-acme-guide.md": extSkillTopical + strings.Repeat("y", skills.DesignBudget)}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonBudget, "ext-acme-guide"},
		{"unclosed fence", map[string]string{"ext-acme-guide.md": extSkillTopical + "<!-- feature: AGG_SUM -->\nopen\n"}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonFence, "ext-acme-guide"},
		{"fence names no feature", map[string]string{"ext-acme-guide.md": extSkillTopical + "<!-- feature: AGG_ACME_NOPE -->\nx\n<!-- /feature -->\n"}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonFence, "ext-acme-guide"},
		{"unfenced built-in operator", map[string]string{"op-agg-acme-trim.md": atomic("A float64.", "A float64, like TEST_WELCH.")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonFenceCoverage, "op-agg-acme-trim"},
		{"unfenced extension operator", map[string]string{"ext-acme-guide.md": extSkillTopical + "Compare AGG_ACME_OTHER.\n"}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonFenceCoverage, "ext-acme-guide"},
		{"unfenced tool", map[string]string{"ext-acme-guide.md": extSkillTopical + "Run it with pulse_facet.\n"}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonFenceCoverage, "ext-acme-guide"},
		{"feature in description", map[string]string{"ext-acme-guide.md": topical("fit a survey workflow.", "fit beside capability:crosstab.")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonFenceCoverage, "ext-acme-guide"},
		{"see names no skill", map[string]string{"op-agg-acme-trim.md": atomic("`aggregation-design`", "`aggregation-designs`")}, errors.PULSE_EXTENSION_SKILL_INVALID, SkillReasonSee, "op-agg-acme-trim"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := tc.files
			if _, ok := files["ext-acme-guide.md"]; !ok && tc.reason == SkillReasonSee {
				files["ext-acme-guide.md"] = extSkillTopical
			}
			_, err := LoadExtensionSkills(extSkillFS(files), extSkillSnapshot(), extSkillDeps)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("err = %v, want a coded error", err)
			}
			if ce.Code != tc.code || ce.Details["reason"] != tc.reason || ce.Details["skill"] != tc.skill {
				t.Fatalf("got %s reason=%v skill=%v (%s), want %s reason=%s skill=%s",
					ce.Code, ce.Details["reason"], ce.Details["skill"], ce.Message, tc.code, tc.reason, tc.skill)
			}
		})
	}
}

// TestLoadExtensionSkills_Guards: an atomic skill may name its own
// operator, that operator's DependsOn (AGG_SUM) and a fenced feature
// bare; a topical skill its requires.
func TestLoadExtensionSkills_Guards(t *testing.T) {
	topical := strings.Replace(extSkillTopical, "type: guide", "type: guide\nrequires: [AGG_ACME_TRIM, capability:crosstab]", 1) +
		"AGG_ACME_TRIM inside a capability:crosstab cell.\n"
	if _, err := LoadExtensionSkills(extSkillFS(map[string]string{
		"op-agg-acme-trim.md": extSkillAtomic, // names AGG_ACME_TRIM and AGG_SUM bare
		"ext-acme-guide.md":   topical,
	}), extSkillSnapshot(), extSkillDeps); err != nil {
		t.Fatalf("guarded names refused: %v", err)
	}
	// Without the DependsOn edge AGG_SUM is an unfenced mention.
	_, err := LoadExtensionSkills(extSkillFS(map[string]string{
		"op-agg-acme-trim.md": extSkillAtomic, "ext-acme-guide.md": extSkillTopical,
	}), extSkillSnapshot(), map[string][]string{"AGG_ACME_TRIM": nil})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Details["reason"] != SkillReasonFenceCoverage {
		t.Fatalf("err = %v, want fence_coverage without the DependsOn guard", err)
	}
}

// TestInstanceOntology_ExtensionSkills: an embedder skill is a skill
// node with the edges a built-in of its shape gets, and goes with its
// operator.
func TestInstanceOntology_ExtensionSkills(t *testing.T) {
	sk, err := LoadExtensionSkills(extSkillFS(map[string]string{
		"op-agg-acme-trim.md": extSkillAtomic, "ext-acme-guide.md": extSkillTopical,
	}), extSkillSnapshot(), extSkillDeps)
	if err != nil {
		t.Fatal(err)
	}
	snap := extSkillSnapshot()
	snap.Skills = sk
	g := extendOntology(BaseOntology(), snap)
	skill := OntologyID(descriptor.OntologyNodeSkill, "op-agg-acme-trim")
	guide := OntologyID(descriptor.OntologyNodeSkill, "ext-acme-guide")
	op := OntologyID(descriptor.OntologyNodeOperator, "AGG_ACME_TRIM")
	want := []descriptor.OntologyEdge{
		{From: op, To: skill, Kind: descriptor.OntologyEdgeDocumentedBy},
		{From: skill, To: guide, Kind: descriptor.OntologyEdgeRoutesTo},
		{From: skill, To: OntologyID(descriptor.OntologyNodeSkill, "aggregation-design"), Kind: descriptor.OntologyEdgeRoutesTo},
		{From: guide, To: op, Kind: descriptor.OntologyEdgeRoutesTo},
	}
	for _, e := range want {
		if !hasEdge(g, e) {
			t.Errorf("missing edge %v", e)
		}
	}
	if p := g.Problems(); len(p) != 0 {
		t.Errorf("problems = %v", p)
	}

	// The operator hidden (dropped from the snapshot): no atomic node; the
	// topical skill stays, without its fence edge.
	hidden := &ExtensionsSnapshot{Aggregators: []descriptor.OperatorMeta{{Name: "AGG_ACME_OTHER"}}, Skills: sk}
	g = extendOntology(BaseOntology(), hidden)
	if g.Has(skill) || !g.Has(guide) {
		t.Errorf("hidden operator: has atomic = %v (want false), has topical = %v (want true)", g.Has(skill), g.Has(guide))
	}
	if p := g.Problems(); len(p) != 0 {
		t.Errorf("problems = %v", p)
	}
}

// TestDiscovery_ExtensionSkills: a hide-nothing instance with embedder
// skills lists and serves them (rendered, markers stripped) and passes
// built-ins through byte-identically; the manifest lists them.
func TestDiscovery_ExtensionSkills(t *testing.T) {
	sk, err := LoadExtensionSkills(extSkillFS(map[string]string{
		"op-agg-acme-trim.md": extSkillAtomic, "ext-acme-guide.md": extSkillTopical,
	}), extSkillSnapshot(), extSkillDeps)
	if err != nil {
		t.Fatal(err)
	}
	snap := extSkillSnapshot()
	snap.Skills = sk
	inst := NewInstanceSnapshot(snap, FeatureSet{Enabled: featureNames()})
	d := inst.Discovery()

	names := map[string]bool{}
	for _, md := range d.Skills() {
		names[md.Name] = true
	}
	if !names["op-agg-acme-trim"] || !names["ext-acme-guide"] || !names["op-agg-count"] {
		t.Fatalf("Skills() lacks an embedder or built-in skill")
	}
	body, ok := d.Skill("ext-acme-guide")
	if !ok || strings.Contains(body, "<!--") || !strings.Contains(body, "`AGG_ACME_TRIM`") {
		t.Errorf("ext-acme-guide body = %q, %v; want the full render, markers stripped", body, ok)
	}
	for _, name := range []string{"op-synth-normal", "statistical-testing", skills.VirtualIntents} {
		got, _ := d.Skill(name)
		want, _ := skills.Get(name)
		if got != want {
			t.Errorf("%s: built-in body differs from skills.Get on a hide-nothing instance", name)
		}
	}
	listed := map[string]bool{}
	for _, s := range visibleSkills(d) {
		listed[s.Name] = true
	}
	if !listed["op-agg-acme-trim"] || !listed["ext-acme-guide"] {
		t.Error("manifest skills lack the embedder skills")
	}
}

func featureNames() []string {
	var out []string
	for _, f := range Features() {
		out = append(out, f.Name)
	}
	return out
}
