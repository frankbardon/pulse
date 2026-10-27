package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/skills"
)

func TestSkillsCoverAllOperatorComponents(t *testing.T) {
	t.Parallel()

	check := func(t *testing.T, opName, stem string, keys []string) {
		t.Helper()
		// The RENDERED skill: the generated `## Contract` names every
		// component key from the registry, so a skill needs no hand
		// key list — but it must carry the contract marker (or name
		// the keys itself).
		content, ok := skills.Get(stem)
		if !ok {
			t.Errorf("%s: missing atomic skill skills/%s.md", opName, stem)
			return
		}
		// Each key must be a backticked token on the rendered
		// `- Components:` line — a bare substring match let a short key
		// such as `n` pass on any prose that happens to contain it.
		named := renderedComponentTokens(content)
		for _, key := range keys {
			if !named[key] {
				t.Errorf("%s: skills/%s.md does not name component key %q on its rendered `- Components:` line (carry `<!-- generated: contract -->`)",
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

	// Attributes: only an operator that declares a ComponentSchema
	// (it emits a Response.Components.Attributes entry) owes the
	// section.
	t.Run("attributes", func(t *testing.T) {
		for _, op := range attributeCapabilities() {
			if len(op.ComponentSchema.Keys) == 0 {
				continue
			}
			op := op
			t.Run(op.Name, func(t *testing.T) {
				stem := "op-attr-" + atomicKebab(strings.TrimPrefix(op.Name, "ATTR_"))
				check(t, op.Name, stem, componentKeyNames(op.ComponentSchema))
			})
		}
	})

	// Matrices: the manifest's declaration (weighted keys spliced in),
	// so the skill names the weighted floor keys too.
	t.Run("matrices", func(t *testing.T) {
		for _, op := range BuildManifest().Matrices {
			op := op
			t.Run(op.Name, func(t *testing.T) {
				stem := "op-mat-" + atomicKebab(strings.TrimPrefix(op.Name, "MAT_"))
				check(t, op.Name, stem, componentKeyNames(op.ComponentSchema))
			})
		}
	})
}

// renderedComponentTokens is every backticked token on the rendered
// `- Components:` line(s) of a served skill body (the generated
// `## Contract` line renderContractKeys writes).
func renderedComponentTokens(body string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		rest, ok := strings.CutPrefix(line, "- Components: ")
		if !ok {
			continue
		}
		for {
			i := strings.IndexByte(rest, '`')
			if i < 0 {
				break
			}
			j := strings.IndexByte(rest[i+1:], '`')
			if j < 0 {
				break
			}
			out[rest[i+1:i+1+j]] = true
			rest = rest[i+2+j:]
		}
	}
	return out
}

// atomicKebab is the descriptor-side mirror of skills.kebab — used to
// derive the file-stem segment for the per-operator skill assertion.
func atomicKebab(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}
