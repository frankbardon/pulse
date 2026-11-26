# Adding a Grouper

> **Run the site checklist first.** `make sites ARGS="new-op grouper"` prints every file and declaration a new `GROUP_*` lands in — derived from where the existing ones appear — with the tests to rerun and the goldens to regenerate; `make sites ARGS="op <NAME>"` does the same for an existing one. See [Finding Change Sites](finding-change-sites.md). This recipe explains each step.

> **Embedding Pulse, not contributing to it?** This recipe adds a *built-in* in `internal/processing/`, which embedders cannot import. To add your own operator, implement the matching contract in the public `extend` package instead — see [Extension Points](extension-points.md).

**Audience:** Pulse internals contributors adding a new `GROUP_*`
operator — a bucketing function that maps each record to a group key
the orchestrator uses to partition the aggregation.

The recipe mirrors the [aggregator recipe](adding-aggregator.md); the
grouper-specific moving parts are the per-bucket key contract, the
grouper registry, and the `ComponentSchema` / `MetaGrouper` emission
path.

## 1. Declare the type constant

Add the new constant to `types/types.go` and the slice returned by
`types.AllGroupTypes()`:

```go
const (
    // ... existing constants ...
    GROUP_DECILE GroupType = "GROUP_DECILE"
)

```

Append it to `types.AllGroupTypes()` and classify it in
`GroupType.Streamable()` / `Mergeable()` (`types/streamability.go`, plus
the `types/streamability_test.go` row).

## 2. Implement and register

Implement the grouper in `internal/processing/`. Register the factory in
`grouperRegistry` (`internal/processing/registry.go`). The interface choice
depends on whether the grouper can run in the streaming path:

- **`Grouper`** — every grouper. `Group(records, field)` returns the
  records partitioned by bucket key (`map[string][]*Record`). A grouper
  that implements only this (quantile-class groupers, which need the
  full sorted view) runs buffered.
- **`StreamableGrouper` / `StreamingGrouper`** — streaming.
  `KeyFor(record)` / `KeyForRow(record, field)` returns a bucket key as
  the iterator walks rows; the orchestrator accumulates per key.
- **`MultiKeyStreamingGrouper`** — multi-emit streaming. `KeysForRow`
  returns N keys per row; used by set-typed groupers
  (`GROUP_SET_PER_ELEMENT`).

## 3. Tests

Add tests in `internal/processing/grouper_test.go` (or the per-grouper test
file) before the implementation. Cover empty input, single-value
input, null-bearing input, the `Include` filter slot if your grouper
honours it, and the streaming-vs-buffered parity assertions where
applicable.

## 4. Declare the capability metadata

Add a row to `internal/descriptor/capabilities_groupers.go` with the grouper's
params, accepted field types, and the
[`ComponentSchema`](#5-declare-the-componentschema-responsecomponents-contract)
that follows. `TestManifestOperatorsComplete` enforces a capability
row per registered grouper.

## 5. Declare the `ComponentSchema` (Response.Components contract)

Every registered grouper MUST declare a `ComponentSchema` on its
capability row in `internal/descriptor/capabilities_groupers.go`, tagged with
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
    AcceptsTypes: tableAcceptsTypes(types.GROUP_DECILE),
    ComponentSchema: groupSchema(descriptor.None,
        descriptor.ComponentKey{Name: "edges", Type: "[]float64", Description: "Decile boundary values (length 9)."},
        descriptor.ComponentKey{Name: "buckets", Type: "map[string]int", Description: "Per-decile record counts keyed by label."},
    ),
},
```

## 6. Emit per-operator component values at runtime

Implement `MetaGrouper` (`internal/processing`) on the grouper type and add a
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

`total_n` is the number of post-filter records the grouper keyed into
at least one bucket, and `n_null` every other post-filter record (null
key, include rejection). For a single-key grouper `total_n` is the sum
of your `buckets[].count` when `Components()` returns a `buckets` list
of `[]map[string]any` with `int` counts (as `GROUP_CATEGORY` does),
otherwise the orchestrator's own keyed-record count. A fan-out grouper
(one implementing `MultiKeyStreamingGrouper`, e.g.
`GROUP_SET_PER_ELEMENT`) puts one record in several buckets, so its
bucket sum counts label observations and MAY exceed `total_n`; the
orchestrator counts its keyed records instead (distinct records across
the buffered partition, one per keyed record on the streaming and
parallel arms). `GROUP_SET_PER_ELEMENT` also keeps its empty-mask
records out of `n_null`: skipped, but not null.

### Merging under the parallel reducers

The parallel reducers (`ShardWorkers` over a shard archive,
`DecodeWorkers` over a large single file) build one grouper per
partition and fold the per-key aggregator buckets themselves. Your
grouper's own components state (the counters behind `Components()`)
is folded through `MergeableGrouper.MergeGrouperState(other)`
(`internal/processing/grouper_merge.go`); `other` is a fresh instance
of the same concrete type from the same spec. Implement it and return
true from `GroupType.Mergeable()` (`types/streamability.go`) only when
the fold is associative — a sum, or the same first/last-write rule the
serial tracker applies — so the merged `Components()` equals a serial
instance's. The reducers sum the assignment count across partitions
and hand it to `processing.FinalizeGroupedStream`, so `total_n` /
`n_null` match the serial path even for a bucket-less grouper. A
grouper not `Mergeable()` runs those requests serially
(`processing.CanMergeRequestWithExtensions`).

An **extension** grouper takes the same paths through the public
contract: declare `Streamable: true` and `Mergeable: true` on its
`pulse.GrouperRegistration` and, if it emits components, implement
`extend.MergeableGrouper.MergeState(other)`; the adapter hands
`MergeState` your own value. A grouper that emits no components needs
no method (the adapter folds nothing). Probe-validation refuses a
declaration the value cannot honour
(`PULSE_EXTENSION_MERGEABLE_MISMATCH`). See
[Extension points — Grouper](extension-points.md).

The full Response.Components contract for groupers — streaming
behaviour by mergeability class, the orchestrator-owned floor, the
extension-side parity contract — lives in the [response-components
skill](https://github.com/frankbardon/pulse/blob/main/internal/skills/response-components.md).
Embedder extensions emit components through the public `extend`
contract (a `ComponentsFunc` or a `Components()` method); see
[Extension Points](extension-points.md).

## 7. The `Include` inclusion-list slot

If your grouper honours an `Include` whitelist of allowed bucket
labels, update `internal/processing/grouper.go` + `internal/processing/grouper_set.go`
so the include filter is applied at key-emission time, not on the
output table after the fact. The contract is enforced by the
following gates (and `TestIncludeGroupers_MatchRuntime`, which pins
predict's list of include-honouring groupers to the runtime):

- `TestGroupCategory_IncludeFiltersLabels`
- `TestGroupSetValue_IncludeFiltersCompositeKey`
- `TestGroupSetPerElement_IncludeFilters`

### Bucket emission order

Buckets emit alphabetically unless the grouper declares an order. A
built-in grouper whose keys carry an intrinsic order (quantile ids,
numeric range edges) implements the unexported `keyOrderer` hook in
`internal/processing/grouper.go` — `keyRank(key string) (float64, bool)`,
derived from the key and the grouper's configuration only, never from
the records it saw — and adds a `_ keyOrderer = (*yourGrouper)(nil)`
lock. Every emission site (grouped rows on every arm, `groups`
composites, both crosstab arms, grouped matrices) then honours it; an
explicit `Include` and `Request.Sort` still win. Pin it with a shuffled
`orderKeysByInclude(includeFilterOf(g), keys)` case and a fused-vs-
buffered crosstab case (`grouper_bucket_order_test.go`).

## 8. The other per-operator tables

Every grouper also has an entry in `builtinFeatures`
(`internal/descriptor/features.go`), `internal/descriptor/purposes_groupers.go`,
`internal/descriptor/interpretation_reading.go`, the chain allowlist in
`processChainCapability` (`internal/descriptor/capabilities_chain.go`),
the crosstab fuse gate (`internal/crosstabfuse/gate.go`), the group-count
limit table (`internal/descriptor/limits.go`) and the accepted-types table
(`internal/fieldaccept/fieldaccept.go`); most also appear in the MCP
binding (`internal/mcp/bind.go`, `internal/mcp/toolmeta/meta.go`).
`make sites ARGS="new-op grouper"` names each with an exemplar line.

## 9. Skill and example

Write the atomic skill `skills/op-group-decile.md` (the operator name lowercased, `_` → `-`: `skills.OperatorStem`)
(`.claude/reference/skill-pack.md`; copy `skills/op-group-category.md`),
add the grouper to the topical `skills/grouper-design.md` (bucket
naming, streamability, cardinality bound), and add an
`internal/examples/groupers/NN_<name>.json` example whose
`_meta.operators` names it (`TestEveryOperatorHasAnExampleTag`).
CLAUDE.md is not a companion.

## 10. Run the gates

`make sites ARGS="op GROUP_DECILE"` prints the full gate set. At minimum:

```bash
go test -count=1 ./internal/skills/ -run 'TestOperatorHasAtomicSkill|TestSkillsCoverAllComponents'
go test -count=1 ./internal/descriptor/ -run TestManifestOperatorsComplete
go test -count=1 ./internal/processing/ -run 'TestGroup|TestIncludeGroupers_MatchRuntime'
go test -count=1 ./types/ ./internal/crosstabfuse/ ./internal/fieldaccept/ ./internal/mcp/
go test -count=1 ./descriptor/ -update && go test -count=1 ./descriptor/
make docs
```

See [The Update Demand](update-demand.md).
