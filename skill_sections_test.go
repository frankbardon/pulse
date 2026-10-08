package pulse

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// Embedder skills opt in to generated sections (U21 E1-S1): a marker
// plus metadata renders, no marker leaves the body unchanged, a marker
// without metadata is removed silently — and pulse.New gains no failure
// mode either way.

const extSectionSkill = `---
name: op-test-acme-gap
description: Gap between two brand scores.
kind: operator
category: TEST
operator: TEST_ACME_GAP
type: reference
applies_to: process
---

Gap between two brand scores.

<!-- generated: use-when -->

## Params
None.

## Inputs
Two scores.

## Output
A test result.

<!-- generated: reading-the-output -->

## Gotchas
None.

## See
None.
`

// extSectionNoMeta carries both markers for an operator that declares
// no Purpose or Interpretation.
const extSectionNoMeta = `---
name: op-agg-acme-plain
description: Plain stub aggregate.
kind: operator
category: AGG
operator: AGG_ACME_PLAIN
type: reference
applies_to: process
---

Plain stub aggregate.

<!-- generated: use-when -->

## Params
None.

## Inputs
A numeric field.

## Output
A float64.

<!-- generated: reading-the-output -->

## Components
The universal floor only.

## Gotchas
None.

## See
None.
`

// extSectionNoMarker is a marker-free embedder skill.
var extSectionNoMarker = strings.Replace(rootExtSkillAtomic, "`ext-acme-guide`", "`op-test-acme-gap`", 1)

func extSectionExtensions() Extensions {
	p := validExtPurpose()
	p.Intents = []string{descx.IntentCompareGroups}
	p.NotFor = []descriptor.Alternative{
		{When: "the two groups are independent samples", Use: "TEST_WELCH"},
		{When: "you compare one group with a fixed target", Use: "TEST_T"},
	}
	return Extensions{
		Tests: []TestRegistration{{
			Name: "TEST_ACME_GAP", Description: "Stub.", Tier: TestTierPost,
			PostFactory: guidanceStubPostTestFactory, Purpose: p,
			Interpretation: []descriptor.Interpretation{{Field: "details.acme_gap", Means: "How far apart the two brand scores are."}},
		}},
		Aggregators: []AggregatorRegistration{
			{Name: "AGG_ACME_PLAIN", Description: "Stub.", Factory: featureSetStubAggFactory},
			{Name: "AGG_ACME_KEPT", Description: "Stub.", Factory: featureSetStubAggFactory},
		},
		Skills: fstest.MapFS{
			"op-test-acme-gap.md":  {Data: []byte(extSectionSkill)},
			"op-agg-acme-plain.md": {Data: []byte(extSectionNoMeta)},
			"op-agg-acme-kept.md":  {Data: []byte(extSectionNoMarker)},
		},
	}
}

func TestExtensionSkills_GeneratedSections(t *testing.T) {
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: extSectionExtensions()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Marker + metadata renders both sections.
	got, ok := p.Skill("op-test-acme-gap")
	if !ok {
		t.Fatal("op-test-acme-gap not served")
	}
	for _, want := range []string{
		"## Use when", validExtPurpose().Plain, "`TEST_WELCH` when the two groups are independent samples.",
		"## Reading the output", "`details.acme_gap`: How far apart the two brand scores are.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("op-test-acme-gap lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<!-- generated") {
		t.Errorf("marker survived:\n%s", got)
	}

	// Marker without metadata: removed silently.
	got, _ = p.Skill("op-agg-acme-plain")
	if strings.Contains(got, "<!-- generated") || strings.Contains(got, "## Use when") || strings.Contains(got, "## Reading the output") || strings.Contains(got, "\n\n\n") {
		t.Errorf("metadata-less markers not removed cleanly:\n%s", got)
	}
	if want := strings.ReplaceAll(strings.ReplaceAll(extSectionNoMeta, "<!-- generated: use-when -->\n\n", ""), "<!-- generated: reading-the-output -->\n\n", ""); got != want {
		t.Errorf("op-agg-acme-plain = %q, want %q", got, want)
	}

	// No marker: unchanged.
	if got, _ = p.Skill("op-agg-acme-kept"); got != extSectionNoMarker {
		t.Errorf("marker-free embedder skill changed:\n%s", got)
	}
}

// TestExtensionSkills_GeneratedSectionsProfiled: on a profiled instance
// a hidden not-for target is absent from the rendered section.
func TestExtensionSkills_GeneratedSectionsProfiled(t *testing.T) {
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: extSectionExtensions(),
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "TEST_ACME_GAP", "TEST_T", "AGG_ACME_PLAIN", "AGG_ACME_KEPT"}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, ok := p.Skill("op-test-acme-gap")
	if !ok {
		t.Fatal("op-test-acme-gap not served")
	}
	if strings.Contains(got, "TEST_WELCH") {
		t.Errorf("hidden not-for target rendered:\n%s", got)
	}
	if !strings.Contains(got, "`TEST_T` when you compare one group with a fixed target.") {
		t.Errorf("visible not-for target missing:\n%s", got)
	}
}

// TestExtensionSkills_MarkersOutsideBudget: the hard embedder budget
// measures the hand-written body — marker lines do not count, so a body
// at exactly the budget without them still loads.
func TestExtensionSkills_MarkersOutsideBudget(t *testing.T) {
	stripped := strings.ReplaceAll(strings.ReplaceAll(extSectionSkill, "<!-- generated: use-when -->\n\n", ""), "<!-- generated: reading-the-output -->\n\n", "")
	body := len(stripped) - strings.Index(stripped, "\nGap between")
	pad := strings.Repeat("x", 1200-body)
	ext := extSectionExtensions()
	ext.Skills = fstest.MapFS{"op-test-acme-gap.md": {Data: []byte(strings.Replace(extSectionSkill, "## Gotchas\nNone.", "## Gotchas\nNone."+pad, 1))}}
	if _, err := New(Options{FS: memFsWith(t, nil), Extensions: ext}); err != nil {
		t.Fatalf("New: %v", err)
	}
}
