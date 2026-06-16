package examples

import (
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestEveryOperatorHasAnExampleTag is the non-skippable CI gate that
// enforces the skills-defer-to-examples contract: every atomic operator
// skill carries a `## See: pulse_examples_search tags=[...]` pointer, and
// that pointer must resolve to at least one example. The gate enumerates
// every in-scope operator from the canonical types.All*Types() enumerators
// and asserts each is represented somewhere in the embedded example
// library.
//
// In-scope categories (assert coverage):
//
//   - AGG_*     — types.AllAggregationTypes()
//   - ATTR_*    — types.AllAttributeTypes()
//   - FILTER_*  — types.AllFiltererTypes()
//   - GROUP_*   — types.AllGroupTypes()
//   - WIN_*     — types.AllWindowTypes()
//   - FEAT_*    — types.AllFeatureTypes()
//   - TEST_*    — types.AllTestTypes() (covers both tier-1 row tests and
//                 tier-2 natives like TEST_TREND / TEST_TUKEY_HSD; the
//                 tier-2 family-name partition is a descriptor-side facet,
//                 not a separate types.All*Types() enumerator)
//   - REG_*     — types.AllRegressionTypes()
//   - OVERLAY_* — types.AllOverlayKinds()
//
// Out-of-scope (intentional — tracked by other gates):
//
//   - SYNTH distributions — exercised via pulse_synth_* and the synth/
//     examples directory; they are not Request-slot operators and are
//     tracked by TestSkillsCoverAllSynthDistributions +
//     TestManifestDistributionsComplete.
//   - Field types — schema-level, not operator-level; tracked by
//     TestSkillsCoverAllFieldTypes.
//   - MCP tools — covered by TestSkillsCoverAllMCPTools and the
//     internal/mcp/mcptools registry, not by _meta.operators.
//
// Coverage rule per category:
//
//   - For AGG / ATTR / FILTER / GROUP / WIN / FEAT / TEST / REG the
//     operator string must appear in `_meta.operators` of at least one
//     example. `_meta.operators` is the canonical declared surface
//     (kept in sync with the request body via TestExamples_OperatorsMatchBody).
//   - For OVERLAY the overlay kind must appear in the `kind` field of an
//     `overlays[]` entry in at least one example body. Overlay kinds are
//     not part of `_meta.operators` by design — the regex in
//     TestExamples_OperatorsMatchBody only catches operator types that
//     ride a Request slot's `type` field. The audit at
//     `.planning/skill-pack-overhaul/research/examples-gap.md` codified
//     this two-track convention; E5-S2 authored the gap-closing examples
//     against it.
//
// Style: per-operator t.Run subtests so a failure pinpoints exactly which
// operator lacks coverage. The subtest name is the operator string itself
// (e.g. `t.Run("AGG_SUM", ...)`), which makes `go test -run` filtering
// natural and `--- FAIL` output legible.
func TestEveryOperatorHasAnExampleTag(t *testing.T) {
	declared, kinds := collectExampleCoverage(t)

	// Tagged operators: ride _meta.operators. Categories enumerated in
	// declaration order — the subtest name is the operator string itself,
	// so per-category grouping is unnecessary for failure-pinpointing.
	taggedCategories := [][]string{
		stringify(types.AllAggregationTypes()),
		stringify(types.AllAttributeTypes()),
		stringify(types.AllFiltererTypes()),
		stringify(types.AllGroupTypes()),
		stringify(types.AllWindowTypes()),
		stringify(types.AllFeatureTypes()),
		stringify(types.AllTestTypes()),
		stringify(types.AllRegressionTypes()),
	}
	var tagged []string
	for _, cat := range taggedCategories {
		tagged = append(tagged, cat...)
	}
	sort.Strings(tagged)
	for _, op := range tagged {
		op := op
		t.Run(op, func(t *testing.T) {
			if _, ok := declared[op]; !ok {
				t.Errorf("operator %q is not declared in `_meta.operators` of any embedded example; "+
					"add a tagging example under examples/ (see .planning/skill-pack-overhaul/research/examples-gap.md "+
					"for the canonical author-an-example pattern)", op)
			}
		})
	}

	// Overlay kinds: ride overlays[].kind in request bodies, not
	// _meta.operators. See package-level comment above for rationale.
	overlayKinds := stringify(types.AllOverlayKinds())
	sort.Strings(overlayKinds)
	for _, kind := range overlayKinds {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			if _, ok := kinds[kind]; !ok {
				t.Errorf("overlay kind %q does not appear in any `overlays[].kind` field of an embedded example; "+
					"add an example under examples/overlays/ instantiating the overlay (see "+
					".planning/skill-pack-overhaul/research/examples-gap.md for the canonical author-an-example pattern)", kind)
			}
		})
	}
}

// collectExampleCoverage walks the embedded examples once and returns the
// union of declared `_meta.operators` plus the union of `overlays[].kind`
// strings encountered anywhere in the request body. The walk uses the same
// embed.FS the library indexes from, so no disk I/O is required.
func collectExampleCoverage(t *testing.T) (declared map[string]struct{}, kinds map[string]struct{}) {
	t.Helper()
	declared = map[string]struct{}{}
	kinds = map[string]struct{}{}

	err := fs.WalkDir(content, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		data, readErr := content.ReadFile(p)
		if readErr != nil {
			t.Fatalf("read %s: %v", p, readErr)
		}

		// Pass 1: harvest _meta.operators declarations.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("%s: parse top-level: %v", p, err)
		}
		if rawMeta, ok := raw["_meta"]; ok {
			var m meta
			if err := json.Unmarshal(rawMeta, &m); err != nil {
				t.Fatalf("%s: parse _meta: %v", p, err)
			}
			for _, op := range m.Operators {
				declared[op] = struct{}{}
			}
		}

		// Pass 2: harvest overlays[].kind strings from the full body.
		// Overlay specs ride multiple slot paths — Request.Overlays,
		// Crosstab.Overlays, ChainRequest.Overlays, ComposedRequest.Overlays,
		// FacetRequest.Overlays — so a generic recursive walk over the
		// decoded JSON is cheaper and more future-proof than enumerating
		// each path explicitly.
		var tree any
		if err := json.Unmarshal(data, &tree); err != nil {
			t.Fatalf("%s: parse body: %v", p, err)
		}
		harvestOverlayKinds(tree, kinds)
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded examples: %v", err)
	}
	return declared, kinds
}

// harvestOverlayKinds recursively walks a decoded JSON tree and records
// every `kind` field whose value begins with `OVERLAY_`. The prefix gate
// keeps the walk specific to overlay specs even though `kind` is a common
// field name in other request shapes (label tables, sample specs); none of
// those parallel uses adopt the `OVERLAY_` value prefix, so collision-free.
func harvestOverlayKinds(node any, into map[string]struct{}) {
	switch v := node.(type) {
	case map[string]any:
		if k, ok := v["kind"].(string); ok && strings.HasPrefix(k, "OVERLAY_") {
			into[k] = struct{}{}
		}
		for _, child := range v {
			harvestOverlayKinds(child, into)
		}
	case []any:
		for _, child := range v {
			harvestOverlayKinds(child, into)
		}
	}
}

// stringify projects any []T whose underlying type is string into a
// plain []string. Used to flatten types.AllAggregationTypes() and
// kin into a per-category slice the gate can sort and iterate.
func stringify[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}
