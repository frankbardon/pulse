package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// Embedder skills (Extensions.Skills): validated at pulse.New, served
// through the facade, manifest and ontology like built-ins, pruned with
// their operator. The validation table lives in internal/descriptor
// (TestLoadExtensionSkills_Refusals); MCP parity in mcp/gosdk
// (TestExtensionSkills_MCP).

const rootExtSkillAtomic = `---
name: op-agg-acme-kept
description: Trimmed mean that drops both tails before averaging.
kind: operator
category: AGG
operator: AGG_ACME_KEPT
type: reference
applies_to: process
---

# AGG_ACME_KEPT

## Params
None.

## Inputs
A numeric field.

## Output
A float64.

## Components
The universal floor only.

## Gotchas
None.

## See
` + "`ext-acme-guide`" + `
`

const rootExtSkillHidden = `---
name: op-agg-acme-brand
description: Brand-share aggregate for the Acme survey.
kind: operator
category: AGG
operator: AGG_ACME_BRAND
type: reference
applies_to: process
---

# AGG_ACME_BRAND

## Params
None.

## Inputs
A categorical field.

## Output
A float64.

## Components
The universal floor only.

## Gotchas
None.

## See
` + "`op-agg-acme-kept`" + `
`

// rootExtSkillGuide fences the brand operator; rootExtSkillBrandGuide
// requires it.
const rootExtSkillGuide = `---
name: ext-acme-guide
description: How the Acme operators fit a survey workflow.
kind: design
type: guide
applies_to: process
---

# Acme guide

Start from the kept mean.
<!-- feature: AGG_ACME_BRAND -->
Brand share comes from AGG_ACME_BRAND.
<!-- /feature -->
`

const rootExtSkillBrandGuide = `---
name: ext-acme-brand-guide
description: Reading brand share.
kind: design
type: guide
applies_to: process
requires: [AGG_ACME_BRAND]
---

# Brand guide

AGG_ACME_BRAND reads one categorical.
`

func acmeSkillExtensions() Extensions {
	return Extensions{
		Aggregators: []AggregatorRegistration{
			{Name: "AGG_ACME_KEPT", Description: "Stub.", Factory: featureSetStubAggFactory},
			{Name: "AGG_ACME_BRAND", Description: "Stub.", Factory: featureSetStubAggFactory},
		},
		Skills: fstest.MapFS{
			"op-agg-acme-kept.md":     {Data: []byte(rootExtSkillAtomic)},
			"op-agg-acme-brand.md":    {Data: []byte(rootExtSkillHidden)},
			"ext-acme-guide.md":       {Data: []byte(rootExtSkillGuide)},
			"ext-acme-brand-guide.md": {Data: []byte(rootExtSkillBrandGuide)},
		},
	}
}

var acmeSkillNames = []string{"ext-acme-brand-guide", "ext-acme-guide", "op-agg-acme-brand", "op-agg-acme-kept"}

func skillNameSet(mds []SkillMetadata) map[string]bool {
	out := map[string]bool{}
	for _, md := range mds {
		out[md.Name] = true
	}
	return out
}

func manifestSkillNames(t *testing.T, p *Pulse) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, s := range p.Manifest(context.Background()).Skills {
		out[s.Name] = true
	}
	return out
}

// TestExtensions_SkillsServedLikeBuiltins: a profile-free instance
// lists, serves and graphs every embedder skill.
func TestExtensions_SkillsServedLikeBuiltins(t *testing.T) {
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: acmeSkillExtensions()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	listed, manifest := skillNameSet(p.Skills()), manifestSkillNames(t, p)
	g := p.Ontology()
	for _, name := range acmeSkillNames {
		if !listed[name] || !manifest[name] {
			t.Errorf("%s: listed=%v manifest=%v, want both", name, listed[name], manifest[name])
		}
		if body, ok := p.Skill(name); !ok || strings.Contains(body, "<!--") {
			t.Errorf("%s: Skill = %v (markers present: %v)", name, ok, strings.Contains(body, "<!--"))
		}
		if !slices.ContainsFunc(g.Nodes, func(n descriptor.OntologyNode) bool { return n.ID == "skill:"+name }) {
			t.Errorf("%s: no ontology node", name)
		}
	}
	if body, _ := p.Skill("ext-acme-guide"); !strings.Contains(body, "AGG_ACME_BRAND") {
		t.Error("ext-acme-guide: fenced span missing on a profile-free instance")
	}
	if !slices.Contains(g.Edges, descriptor.OntologyEdge{From: "operator:AGG_ACME_KEPT", To: "skill:op-agg-acme-kept", Kind: descriptor.OntologyEdgeDocumentedBy}) {
		t.Error("no documented_by edge from the extension operator to its skill")
	}
	// Listed metadata is sorted like the built-in list.
	all := p.Skills()
	if !slices.IsSortedFunc(all, func(a, b SkillMetadata) int { return strings.Compare(a.Name, b.Name) }) {
		t.Error("Skills() not sorted by name")
	}
}

// TestExtensions_SkillsKeepBuiltinsByteIdentical: registering embedder
// skills changes no built-in skill, listing or body.
func TestExtensions_SkillsKeepBuiltinsByteIdentical(t *testing.T) {
	plain, err := New(Options{FS: memFsWith(t, nil)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: acmeSkillExtensions()})
	if err != nil {
		t.Fatal(err)
	}
	ext := map[string]bool{}
	for _, n := range acmeSkillNames {
		ext[n] = true
	}
	var got []SkillMetadata
	for _, md := range p.Skills() {
		if !ext[md.Name] {
			got = append(got, md)
		}
	}
	want := plain.Skills()
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatal("built-in skill listing differs once embedder skills are registered")
	}
	for _, md := range want {
		x, _ := plain.Skill(md.Name)
		y, _ := p.Skill(md.Name)
		if x != y {
			t.Errorf("%s: body differs once embedder skills are registered", md.Name)
		}
	}
}

// TestExtensions_SkillsPrunedWithOperator: a feature profile hiding
// AGG_ACME_BRAND hides its atomic skill and the topical skill requiring
// it — absent from every list, and an exact get reads like a name that
// never existed — and cuts the fenced span of the topical skill that
// stays.
func TestExtensions_SkillsPrunedWithOperator(t *testing.T) {
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: acmeSkillExtensions(),
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "AGG_ACME_KEPT"}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	listed, manifest := skillNameSet(p.Skills()), manifestSkillNames(t, p)
	raw, _ := json.Marshal(p.Ontology())
	for _, name := range []string{"op-agg-acme-brand", "ext-acme-brand-guide"} {
		body, ok := p.Skill(name)
		never, neverOK := p.Skill("ext-never-registered")
		if ok || body != never || neverOK {
			t.Errorf("%s: Skill = (%q, %v), want the nonexistent answer (%q, %v)", name, body, ok, never, neverOK)
		}
		if listed[name] || manifest[name] || strings.Contains(string(raw), "skill:"+name) {
			t.Errorf("%s: listed=%v manifest=%v in ontology=%v, want none", name, listed[name], manifest[name], strings.Contains(string(raw), "skill:"+name))
		}
	}
	for _, name := range []string{"op-agg-acme-kept", "ext-acme-guide"} {
		if !listed[name] || !manifest[name] {
			t.Errorf("%s: pruned, want kept", name)
		}
	}
	guide, _ := p.Skill("ext-acme-guide")
	if strings.Contains(guide, "AGG_ACME_BRAND") || !strings.Contains(guide, "Start from the kept mean.") {
		t.Errorf("ext-acme-guide = %q; want the brand fence cut, the rest kept", guide)
	}
	// The kept atomic skill's See names a kept skill only: unchanged.
	if kept, _ := p.Skill("op-agg-acme-kept"); !strings.Contains(kept, "`ext-acme-guide`") {
		t.Errorf("op-agg-acme-kept See lost a visible stem: %q", kept)
	}
}

// TestExtensions_SkillsRefusedAtNew: pulse.New surfaces the validation
// code (the full table: internal/descriptor TestLoadExtensionSkills_Refusals).
func TestExtensions_SkillsRefusedAtNew(t *testing.T) {
	cases := []struct {
		name string
		fs   fstest.MapFS
		code errors.Code
	}{
		{"built-in stem", fstest.MapFS{"op-agg-count.md": {Data: []byte(rootExtSkillAtomic)}}, errors.PULSE_EXTENSION_SKILL_COLLISION},
		{"unregistered operator", fstest.MapFS{"op-agg-acme-nope.md": {Data: []byte(strings.ReplaceAll(strings.ReplaceAll(rootExtSkillAtomic, "acme-kept", "acme-nope"), "AGG_ACME_KEPT", "AGG_ACME_NOPE"))}}, errors.PULSE_EXTENSION_SKILL_INVALID},
		{"unfenced built-in", fstest.MapFS{"ext-acme-guide.md": {Data: []byte(rootExtSkillGuide + "Pair it with TEST_WELCH.\n")}}, errors.PULSE_EXTENSION_SKILL_INVALID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext := acmeSkillExtensions()
			ext.Skills = tc.fs
			_, err := New(Options{FS: memFsWith(t, nil), Extensions: ext})
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("New err = %v, want %s", err, tc.code)
			}
		})
	}
}

// TestExtensions_SkillsMergedValuesCollide: two merged Extensions values
// shipping the same stem collide; distinct stems merge.
func TestExtensions_SkillsMergedValuesCollide(t *testing.T) {
	base := acmeSkillExtensions()
	a := Extensions{Aggregators: base.Aggregators, Skills: fstest.MapFS{"ext-acme-guide.md": {Data: []byte(rootExtSkillGuide)}}}
	b := Extensions{Skills: fstest.MapFS{"ext-acme-guide.md": {Data: []byte(rootExtSkillGuide)}}}
	_, err := validateExtensionUniverse(mergeExtensions([]Extensions{a, b}))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_EXTENSION_SKILL_COLLISION || ce.Details["reason"] != descx.SkillReasonDuplicate {
		t.Fatalf("err = %v, want PULSE_EXTENSION_SKILL_COLLISION duplicate", err)
	}
	b.Skills = fstest.MapFS{"op-agg-acme-kept.md": {Data: []byte(rootExtSkillAtomic)}}
	u, err := validateExtensionUniverse(mergeExtensions([]Extensions{a, b}))
	if err != nil || len(u.skills) != 2 {
		t.Fatalf("distinct stems: skills = %d, err = %v; want 2, nil", len(u.skills), err)
	}
}
