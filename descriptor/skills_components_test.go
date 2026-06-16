package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/skills"
)

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
