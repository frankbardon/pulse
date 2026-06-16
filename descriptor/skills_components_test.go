package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/skills"
)

// TestSkillsCoverAllOperatorComponents is the non-skippable CI gate for
// per-operator component-key documentation under the post-E4 atomic-skill
// regime. For every registered aggregator, grouper, and filterer:
//
//  1. The matching per-operator atomic skill file exists
//     (skills/op-<category>-<kebab-name>.md). Existence is also
//     enforced by skills/coverage_test.go's TestSkillsCoverAllComponents
//     — this gate is a sibling assertion that focuses on component-key
//     coverage rather than file existence alone.
//  2. The atomic file contains a `## Components` section (also enforced
//     structurally by skills/atomic_test.go TestAtomicSkillHasRequiredSections).
//  3. Every key declared in the operator's `ComponentSchema` appears
//     in the matching atomic file's body. Universal-floor keys live in
//     the file as part of the operator's Components bullet; per-operator
//     keys extend that section inline.
//
// Routing (E4 atomic-skill convention):
//   - AGG_<name>  -> skills/op-agg-<kebab>.md
//   - GROUP_<name> -> skills/op-group-<kebab>.md
//   - FILTER_<name> -> skills/op-filter-<kebab>.md
//
// Drift modes caught:
//   - A new operator landing with a new component key that the
//     per-operator skill does not yet document.
//   - A capability-row rename that loses skill coverage.
//   - A skill edit that removes a previously-documented key.
//   - A registered operator missing its per-operator skill file.
//
// The descriptor package is the natural home for this gate — skills/
// is a leaf the descriptor package imports for manifest assembly, so
// reaching from descriptor into skills.Get is acyclic.
func TestSkillsCoverAllOperatorComponents(t *testing.T) {
	check := func(t *testing.T, opName, stem string, keys []string) {
		t.Helper()
		content, ok := skills.Get(stem)
		if !ok {
			t.Errorf("%s: missing atomic skill skills/%s.md", opName, stem)
			return
		}
		if !strings.Contains(content, "## Components") {
			t.Errorf("%s: skills/%s.md missing required `## Components` section", opName, stem)
		}
		for _, key := range keys {
			if !strings.Contains(content, key) {
				t.Errorf("%s: skills/%s.md does not mention component key %q (declare it under `## Components`)",
					opName, stem, key)
			}
		}
	}

	t.Run("aggregators", func(t *testing.T) {
		for _, op := range aggregatorCapabilities() {
			op := op
			t.Run(op.Name, func(t *testing.T) {
				stem := "op-agg-" + atomicKebab(strings.TrimPrefix(op.Name, "AGG_"))
				keys := make([]string, 0, len(op.ComponentSchema.Keys))
				for _, k := range op.ComponentSchema.Keys {
					keys = append(keys, k.Name)
				}
				check(t, op.Name, stem, keys)
			})
		}
	})

	t.Run("groupers", func(t *testing.T) {
		for _, op := range grouperCapabilities() {
			op := op
			t.Run(op.Name, func(t *testing.T) {
				stem := "op-group-" + atomicKebab(strings.TrimPrefix(op.Name, "GROUP_"))
				keys := make([]string, 0, len(op.ComponentSchema.Keys))
				for _, k := range op.ComponentSchema.Keys {
					keys = append(keys, k.Name)
				}
				check(t, op.Name, stem, keys)
			})
		}
	})

	t.Run("filterers", func(t *testing.T) {
		for _, op := range filtererCapabilities() {
			op := op
			t.Run(op.Name, func(t *testing.T) {
				stem := "op-filter-" + atomicKebab(strings.TrimPrefix(op.Name, "FILTER_"))
				keys := make([]string, 0, len(op.ComponentSchema.Keys))
				for _, k := range op.ComponentSchema.Keys {
					keys = append(keys, k.Name)
				}
				check(t, op.Name, stem, keys)
			})
		}
	})
}

// atomicKebab is the descriptor-side mirror of skills.kebab — used to
// derive the file-stem segment for the per-operator skill assertion.
func atomicKebab(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}
