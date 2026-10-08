package pulse

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// renderedSectionHeadings are the headings the read-time generated
// sections render under (internal/descriptor/skill_sections.go).
var renderedSectionHeadings = []string{"## Use when", "## Reading the output"}

// generatedSectionsOf returns the generated sections of a served skill
// body: each block from a generated-section heading to the next `## `
// heading. The hand-written pack never spells those headings itself
// (the marker is the only way in), so every block found is generated.
func generatedSectionsOf(body string) []string {
	var out []string
	var cur *strings.Builder
	flush := func() {
		if cur != nil {
			out = append(out, strings.TrimRight(cur.String(), "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			if slices.Contains(renderedSectionHeadings, strings.TrimSpace(line)) {
				cur = &strings.Builder{}
			}
		}
		if cur != nil {
			cur.WriteString(line)
			cur.WriteByte('\n')
		}
	}
	flush()
	return out
}

// hiddenSlotTokens is every wire token of a slot-owning capability the
// instance hides, read from the slot-token table itself so dropping the
// table from hiddenProseNames fails the sweep.
func hiddenSlotTokens(p *Pulse) map[string]bool {
	inst := p.svc.InstanceSnapshot()
	out := map[string]bool{}
	for _, c := range descx.SlotTokenCapabilities() {
		if inst.Enabled(c) {
			continue
		}
		toks, _ := descx.SlotTokensOf(c)
		for _, tok := range toks {
			out[tok] = true
		}
	}
	return out
}

// allFeaturesBut is every built-in feature except hide and each
// feature left with an unsatisfiable dependency group once it goes —
// the largest valid profile that hides hide.
func allFeaturesBut(hide string) []string {
	gone := map[string]bool{hide: true}
	for changed := true; changed; {
		changed = false
		for _, f := range descx.Features() {
			if gone[f.Name] {
				continue
			}
			for _, group := range f.DependsOn {
				if !slices.ContainsFunc(group, func(n string) bool { return !gone[n] }) {
					gone[f.Name], changed = true, true
					break
				}
			}
		}
	}
	var out []string
	for _, n := range descx.FeatureNames() {
		if !gone[n] {
			out = append(out, n)
		}
	}
	return out
}

// sweepSlotOp is an embedder post-test whose Interpretation names every
// slot-owning capability's wire tokens in a short on-topic sentence, so
// its rendered `## Reading the output` carries each token within the
// section cap (the built-in sections drop theirs to the cap): it proves
// the instance scrub — slot tokens included — runs on the generated
// sections, exactly as for a built-in.
const sweepSlotOp = "TEST_SWEEP_SLOTS"

func sweepSlotExtensions() Extensions {
	skill := strings.NewReplacer("op-test-acme-gap", "op-test-sweep-slots", "TEST_ACME_GAP", sweepSlotOp).Replace(extSectionSkill)
	return Extensions{
		Tests: []TestRegistration{{
			Name: sweepSlotOp, Description: "Stub.", Tier: TestTierPost,
			PostFactory: guidanceStubPostTestFactory, Purpose: validExtPurpose(),
			Interpretation: []descriptor.Interpretation{
				{Field: "details.sweep_p", Means: "A p-value; to adjust for multiple comparisons set multiplicity and read p_adjusted."},
				{Field: "details.sweep_w", Means: "Rows with an invalid weight are counted in n_weight_invalid."},
				{Field: "details.sweep_m", Means: "Bounded by the correlation matrix pair counts min_pair_n and max_pair_n."},
				{Field: "details.sweep_c", Means: "Margin figures come from margin_aggregations on the crosstab."},
			},
		}},
		Skills: fstest.MapFS{"op-test-sweep-slots.md": {Data: []byte(skill)}},
	}
}

// TestProfileRenderedSkillSweep (U21 E1-S4, PRD FR-41 skills half): for
// every published example feature profile, every private fixture and,
// per slot-owning capability with wire tokens, the largest profile
// hiding it (plus the sweepSlotOp probe), every visible `op-*` skill as
// p.Skill serves it names no hidden operator, hidden-feature MCP tool or
// hidden capability's slot token — the read-time generated `## Use
// when` / `## Reading the output` sections included — and the guidance
// lint's text rules (ASA-*, MULTI-COMP-MANUAL, SLOT-TOKEN) find nothing
// in those rendered sections, profiled or not. Non-vacuous: some
// visible skill's FULL-instance generated section names a token a
// shipped profile hides, and on every all-but profile the probe's full
// render names that capability's tokens.
func TestProfileRenderedSkillSweep(t *testing.T) {
	full, err := New(Options{FS: afero.NewMemMapFs(), Extensions: sweepSlotExtensions()})
	if err != nil {
		t.Fatal(err)
	}
	pulses := shippedProfilePulses(t)
	pulses["unprofiled"] = full
	for _, c := range descx.SlotTokenCapabilities() {
		if toks, _ := descx.SlotTokensOf(c); len(toks) == 0 {
			continue
		}
		p, err := New(Options{FS: afero.NewMemMapFs(), Extensions: sweepSlotExtensions(),
			FeatureProfile: &FeatureProfile{Features: append(allFeaturesBut(c), sweepSlotOp)}})
		if err != nil {
			t.Fatalf("New without %s: %v", c, err)
		}
		pulses["all-but/"+c] = p
	}

	shippedScrubbed := 0
	for _, key := range slices.Sorted(maps.Keys(pulses)) {
		p := pulses[key]
		t.Run(key, func(t *testing.T) {
			hidden := errorsHiddenTokens(p)
			slots := hiddenSlotTokens(p)
			sectionsSeen, slotScrubbed := 0, 0
			for _, md := range p.Skills() {
				if !strings.HasPrefix(md.Name, "op-") {
					continue
				}
				body, ok := p.Skill(md.Name)
				if !ok {
					t.Errorf("profile %s: listed skill %s does not read", key, md.Name)
					continue
				}
				if tok := namesHiddenToken(body, hidden); tok != "" {
					t.Errorf("profile %s: skill %s names hidden %s", key, md.Name, tok)
				}
				for _, s := range slotTokenLeaks(body, slots) {
					t.Errorf("profile %s: skill %s names a hidden slot token: %q", key, md.Name, s)
				}
				sections := generatedSectionsOf(body)
				sectionsSeen += len(sections)
				// Linted line by line: each rendered line carries one
				// registry prose string, as the scrub reads it.
				for _, sec := range sections {
					for _, line := range strings.Split(sec, "\n") {
						for _, h := range descx.LintGuidanceText(line) {
							t.Errorf("profile %s: skill %s generated section: lint %s: %q", key, md.Name, h.Rule, h.Span)
						}
					}
				}
				if p == full {
					continue
				}
				fullBody, _ := full.Skill(md.Name)
				for _, sec := range generatedSectionsOf(fullBody) {
					if namesHiddenToken(sec, hidden) != "" {
						shippedScrubbed++
					}
					if len(slotTokenLeaks(sec, slots)) > 0 {
						slotScrubbed++
					}
				}
			}
			if sectionsSeen == 0 && key != "fixture/empty" {
				t.Fatalf("profile %s serves no generated section: vacuous", key)
			}
			if strings.HasPrefix(key, "all-but/") && slotScrubbed == 0 {
				t.Errorf("profile %s: no full-instance generated section names a hidden slot token: vacuous", key)
			}
		})
	}
	if shippedScrubbed == 0 {
		t.Error("no visible skill's full-instance generated section names a hidden operator or tool: the sweep is vacuous")
	}
}
