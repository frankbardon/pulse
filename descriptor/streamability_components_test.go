package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

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
