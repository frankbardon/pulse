package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestStreamability_ComponentsMergeabilityKnown is the fail-closed gate
// for the per-operator components mergeability axis (E4-S1). Every
// registered aggregator, grouper, and filterer MUST declare a
// non-empty ComponentSchema.Mergeability drawn from the canonical
// triple {Mergeable, Partial, None} (defined in types/streamability.go
// and aliased on the descriptor side).
//
// Drift modes the gate catches:
//   - A new operator landing in types.All*Types() without a row in
//     the capabilities slice (capabilities_*.go).
//   - An existing capability row losing its Mergeability tag during a
//     refactor (zero-value empty string).
//   - A typo'd merge string sneaking in via direct ComponentSchema
//     construction (the constants forward from types/ so any literal
//     other than "mergeable" / "partial" / "none" fails the lookup).
//
// The gate is intentionally co-located with descriptor/ rather than
// types/ — types/ is the leaf package and cannot import descriptor/
// (would cycle), so the only place that can introspect both
// types.All*Types() and the descriptor capability slices at once is
// descriptor/.
func TestStreamability_ComponentsMergeabilityKnown(t *testing.T) {
	validMerge := map[ComponentsMergeability]bool{
		Mergeable: true,
		Partial:   true,
		None:      true,
	}

	t.Run("aggregators", func(t *testing.T) {
		caps := indexByName(aggregatorCapabilities())
		for _, agg := range types.AllAggregationTypes() {
			op, ok := caps[string(agg)]
			if !ok {
				t.Fatalf("aggregator %s missing from aggregatorCapabilities() — declare a capability row before landing the registry entry", agg)
			}
			if op.ComponentSchema.Mergeability == "" {
				t.Errorf("%s: ComponentSchema.Mergeability is empty — declare one of {Mergeable, Partial, None}", agg)
				continue
			}
			if !validMerge[op.ComponentSchema.Mergeability] {
				t.Errorf("%s: ComponentSchema.Mergeability = %q, want one of {%q, %q, %q}",
					agg, op.ComponentSchema.Mergeability, Mergeable, Partial, None)
			}
		}
	})

	t.Run("groupers", func(t *testing.T) {
		caps := indexByName(grouperCapabilities())
		for _, g := range types.AllGroupTypes() {
			op, ok := caps[string(g)]
			if !ok {
				t.Fatalf("grouper %s missing from grouperCapabilities() — declare a capability row before landing the registry entry", g)
			}
			if op.ComponentSchema.Mergeability == "" {
				t.Errorf("%s: ComponentSchema.Mergeability is empty — declare one of {Mergeable, Partial, None}", g)
				continue
			}
			if !validMerge[op.ComponentSchema.Mergeability] {
				t.Errorf("%s: ComponentSchema.Mergeability = %q, want one of {%q, %q, %q}",
					g, op.ComponentSchema.Mergeability, Mergeable, Partial, None)
			}
		}
	})

	t.Run("filterers", func(t *testing.T) {
		caps := indexByName(filtererCapabilities())
		for _, f := range types.AllFiltererTypes() {
			op, ok := caps[string(f)]
			if !ok {
				t.Fatalf("filterer %s missing from filtererCapabilities() — declare a capability row before landing the registry entry", f)
			}
			if op.ComponentSchema.Mergeability == "" {
				t.Errorf("%s: ComponentSchema.Mergeability is empty — declare one of {Mergeable, Partial, None}", f)
				continue
			}
			if !validMerge[op.ComponentSchema.Mergeability] {
				t.Errorf("%s: ComponentSchema.Mergeability = %q, want one of {%q, %q, %q}",
					f, op.ComponentSchema.Mergeability, Mergeable, Partial, None)
			}
		}
	})
}

// TestComponentsMergeability_DescriptorAliasMatchesTypes pins the
// alias contract: descriptor.Mergeable / Partial / None forward
// byte-for-byte to types.ComponentsMergeable / ComponentsPartial /
// ComponentsNone. The values are wire-stable JSON strings — if anyone
// renames the descriptor-side constants (or the underlying types-side
// constants) without updating the other, the alias fiction breaks and
// every manifest-consuming downstream sees a mismatched enum.
func TestComponentsMergeability_DescriptorAliasMatchesTypes(t *testing.T) {
	cases := []struct {
		desc string
		got  ComponentsMergeability
		want types.ComponentsMergeability
		wire string
	}{
		{"Mergeable", Mergeable, types.ComponentsMergeable, "mergeable"},
		{"Partial", Partial, types.ComponentsPartial, "partial"},
		{"None", None, types.ComponentsNone, "none"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("descriptor.%s = %q, types alias mismatch (want %q)", c.desc, c.got, c.want)
			}
			if string(c.got) != c.wire {
				t.Errorf("descriptor.%s wire value = %q, want %q (manifest JSON contract)", c.desc, string(c.got), c.wire)
			}
		})
	}
}

// indexByName returns a name→Operator lookup for capability slice
// iteration in the gate above. Order-independent; the gate iterates
// types.All*Types() rather than the slice so any types-side entry
// missing a capability row fails closed.
func indexByName(ops []Operator) map[string]Operator {
	out := make(map[string]Operator, len(ops))
	for _, op := range ops {
		out[op.Name] = op
	}
	return out
}
