# Adding a Grouper

**Audience:** Pulse internals contributors adding a new `GROUP_*`
operator — a bucketing function that maps each record to a group key
the orchestrator uses to partition the aggregation.

The recipe mirrors the aggregator recipe; the grouper-specific moving
parts are the per-bucket key contract, the grouper registry, and the
v0.20.0 `ComponentSchema` / `MetaGrouper` emission path.

## 1. Declare the type constant

Add the new constant to `types/types.go` and the slice returned by
`types.AllGroupTypes()`:

```go
const (
    // ... existing constants ...
    GROUP_DECILE GroupType = "GROUP_DECILE"
)

func AllGroupTypes() []GroupType {
    return []GroupType{
        // ... existing entries, alphabetised ...
        GROUP_DECILE,
    }
}
```

## 2. Implement and register

Implement the grouper in `processing/`. Register the factory in
`grouperRegistry` (`processing/registry.go`). The interface choice
depends on whether the grouper can run in the streaming path:

- **`Grouper`** — buffered-only. `Group(rows)` returns a slice of
  bucket assignments. Used by quantile-class groupers that need the
  full sorted view.
- **`StreamingGrouper` / `StreamableGrouper`** — streaming. The
  per-record `KeyFor(record)` / `KeyForRow(record)` returns a bucket
  key as the iterator walks rows; the orchestrator accumulates per
  key.
- **`MultiKeyStreamingGrouper`** — multi-emit streaming. `KeysForRow`
  returns N keys per row; used by set-typed groupers
  (`GROUP_SET_PER_ELEMENT`).

## 3. Tests

Add tests in `processing/grouper_test.go` (or the per-grouper test
file) before the implementation. Cover empty input, single-value
input, null-bearing input, the `Include` filter slot if your grouper
honours it, and the streaming-vs-buffered parity assertions where
applicable.

## 4. Declare the capability metadata

Add a row to `descriptor/capabilities_groupers.go` with the grouper's
params, accepted field types, and the
[`ComponentSchema`](#5-declare-the-componentschema-responsecomponents-contract)
that follows. `TestManifestOperatorsComplete` enforces a capability
row per registered grouper.

## 5. Declare the `ComponentSchema` (Response.Components contract)

Every registered grouper MUST declare a `ComponentSchema` on its
capability row in `descriptor/capabilities_groupers.go`, tagged with
one of three mergeability classes:

| Class | Wire value | When to use |
|---|---|---|
| `Mergeable` | `"mergeable"` | Per-bucket counts and bucket-key state fold associatively across chunks and shards. Used by `GROUP_CATEGORY`, `GROUP_RANGE`, `GROUP_ROUNDED`, `GROUP_DATE`. |
| `Partial` | `"partial"` | Counts fold trivially but per-bucket key state is dict-bearing (e.g. growing label sets). The orchestrator may stage the merge at terminal flush. |
| `None` | `"none"` | Bucket definitions cannot be known until the full input is seen — quantile-class groupers (`GROUP_QUANTILE`) need the full sorted view. Streaming chunks omit components; emission lands on terminal buffered flush only. |

The `groupSchema` helper in `capabilities_groupers.go` prepends the
universal floor (`{"total_n", "n_null"}`) automatically — do not list
those keys in your `extra` slice:

```go
{
    Name:        string(types.GROUP_DECILE),
    Category:    "grouper",
    Description: "Partition the numeric field into ten equally-sized quantile buckets.",
    AcceptsTypes: numericFieldTypesAnalyticsNoDecimal,
    ComponentSchema: groupSchema(None,
        ComponentKey{Name: "edges", Type: "[]float64", Description: "Decile boundary values (length 9)."},
        ComponentKey{Name: "buckets", Type: "map[string]int", Description: "Per-decile record counts keyed by label."},
    ),
},
```

## 6. Emit per-operator component values at runtime

Implement `processing.MetaGrouper` on the grouper type and add a
compile-time assertion in the grouper implementation file:

```go
type decileGrouper struct {
    edges   []float64
    buckets map[string]int
    // ...
}

func (g *decileGrouper) Components() (map[string]any, error) {
    return map[string]any{
        "edges":   g.edges,
        "buckets": g.buckets,
    }, nil
}

var _ MetaGrouper = (*decileGrouper)(nil)
```

The orchestrator owns the universal floor `{total_n, n_null}` — the
post-filter record walker fills it unconditionally. Your `Components()`
MUST NOT re-emit those keys.

For single-key groupers, `total_n` equals the sum of bucket counts.
For multi-key streaming groupers (`GROUP_SET_PER_ELEMENT`), the sum
of bucket counts exceeds `total_n` because a single record contributes
to multiple buckets — `total_n` reflects the row count.

The full Response.Components contract for groupers — streaming
behaviour by mergeability class, the orchestrator-owned floor, the
extension-side parity contract — lives in the [response-components
skill](https://github.com/frankbardon/pulse/blob/main/skills/response-components.md).
Embedder extensions implement the same `MetaGrouper` interface via
`ComponentsFunc` or directly; see [extension-points
skill](https://github.com/frankbardon/pulse/blob/main/skills/extension-points.md).

## 7. The `Include` inclusion-list slot

If your grouper honours an `Include` whitelist of allowed bucket
labels, update `processing/grouper.go` + `processing/grouper_set.go`
so the include filter is applied at key-emission time, not on the
output table after the fact. The contract is enforced by the
following gates:

- `TestGroupCategory_IncludeFiltersLabels`
- `TestGroupSetValue_IncludeFiltersCompositeKey`
- `TestGroupSetPerElement_IncludeFilters`

## 8. Update the grouper-design skill

Add a section in `skills/grouper-design.md` covering the new
grouper's bucket-naming convention, the params it accepts, the
streamability classification, and any cardinality bound.

## 9. Update CLAUDE.md

Bump the registered-grouper count in CLAUDE.md's "Skill Pack" section.

## 10. Run the gates

```bash
go test ./skills/ -run TestSkillsCoverAllComponents
go test ./descriptor/ -run TestManifestOperatorsComplete
go test ./processing/ -run TestGroup
```

The Update Demand row for groupers (and the `Group.Include` slot) covers
all of these in one PR; see [The Update Demand](update-demand.md).
