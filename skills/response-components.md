---
name: response-components
description: How Response.Components carries the constituent parts of every aggregation, grouper, filterer, and crosstab cell
type: guide
applies_to: process, compose, predict, sample, facet
---

# Response.Components — universal contract

`Response.Components` carries the constituent parts behind every value a Pulse
processing run emits, so every aggregator scalar, every grouper bucket count,
every filterer N-in/N-out, and every crosstab cell is auditable from the same
envelope that delivered the value. The slot is always-on for built-in operators
and required for extension-registered operators (probe-validation rejects an
emitter without a matching `ComponentSchema`). The shape is additive
omitempty — `Response.Components` itself is `*ResponseComponents` so a run that
produces nothing components-shaped marshals to no `components` key at all,
byte-identical to the pre-Components wire form. `format_version` stays at
`"1.0"` because the slot is additive — only renames/removals bump per the
Output Format Contract in CLAUDE.md.

This is the single source of truth for the universal contract; the per-category
skills (`aggregation-guide.md`, `grouper-design.md`, `crosstab-guide.md`,
`overlay-system.md`, `extension-points.md`) carry per-operator key tables and
point back here for the cross-cutting shape.

## Universal floor

Every typed shell has a typed floor — the orchestrator fills it, NOT the
operator's `Components()` method. Operator code only emits its declared
schema-keys (`mean`, `variance`, `mode_count`, `range_min`, etc.); the
universal floor is wired in service-layer code so a no-op `Components()`
implementation (or a floor-only extension) still surfaces `{n, n_null}`.

| Shell | Floor fields | JSON keys |
|---|---|---|
| `AggregationComponents` | `N int`, `NNull int` | `n`, `n_null` |
| `GrouperComponents` | `TotalN int`, `NNull int` | `total_n`, `n_null` |
| `FiltererComponents` | `NIn int`, `NOut int`, `NNullInput int` | `n_in`, `n_out`, `n_null_input` |
| Crosstab cell | `n int`, `n_null int` (inside `CellComponents[r][c] map[string]any`) | `n`, `n_null` |

Operator-specific keys ride inside the typed shell's `Operator map[string]any`
slot (Aggregation / Grouper) or directly inside the cell map for crosstab
cells, per the schema declared in `descriptor.Manifest.ComponentsSchemas`.

The floor is filled unconditionally even when the operator's
`ComponentSchema.Keys` slice is empty. `AGG_COUNT` is the canonical floor-only
aggregator — it declares no operator-specific keys but consumers still see
`{n, n_null}` on every entry.

## Per-family typed shape

`ResponseComponents` carries five sub-blocks. Every nested slice / pointer /
map is `omitempty` so a partially populated run marshals to byte-identical
wire output against the pre-Components baseline.

```go
type ResponseComponents struct {
    Aggregations []AggregationComponents `json:"aggregations,omitempty"`
    Groupers     []GrouperComponents     `json:"groupers,omitempty"`
    Crosstab     *CrosstabComponents     `json:"crosstab,omitempty"`
    Filterers    []FiltererComponents    `json:"filterers,omitempty"`
    Run          *RunComponents          `json:"run,omitempty"`
}
```

- **`Aggregations []AggregationComponents`** — one entry per
  `Request.Aggregations` slot in matching declared order. Slot identity rides
  on `Label` (mirrors `Aggregation.Label`). Universal floor fields `N` and
  `NNull` plus operator-specific keys inside `Operator map[string]any`. See
  `skills/aggregation-guide.md` for the per-AGG key tables.

- **`Groupers []GrouperComponents`** — one entry per `Request.Groups` slot in
  matching declared order. Slot identity rides on `Field` plus `Label` when
  multiple groupers share a field. Universal floor fields `TotalN` and
  `NNull` plus operator-specific keys (bucket edges, dict mappings,
  `range_min`, `range_max`, etc.) inside `Operator map[string]any`. For
  single-key groupers `TotalN` equals the sum of bucket counts; for
  multi-key streaming groupers (e.g. `GROUP_SET_PER_ELEMENT`) the sum of
  bucket counts exceeds `TotalN` because a single record contributes to
  multiple buckets. See `skills/grouper-design.md` for per-GROUP keys.

- **`Crosstab *CrosstabComponents`** — populated only when the originating
  Request carried `Crosstab`. Mirrors `MatrixPayload` coordinate-for-
  coordinate so consumers index components by the same `(r, c)` tuple they
  already use to read `Cells`, `RowMargins`, `ColumnMargins`, `GrandTotal`.
  Carries the per-cell record counts (`CellCounts[r][c]`), the per-cell
  aggregator components (`CellComponents[r][c]`), per-axis margin counts +
  components, grand-total counts + components, per-axis grouper components
  (`RowKeyComponents`, `ColumnKeyComponents`), and the sanity counters
  `IncludedRecords` / `ExcludedRecords`. See `skills/crosstab-guide.md` for
  the full indexing contract.

- **`Filterers []FiltererComponents`** — one entry per `Request.Filterers`
  slot in matching declared order. Slot identity rides on `Label`. Universal
  floor fields `NIn`, `NOut`, `NNullInput` — there is no operator-specific
  `Operator` slot today; `MetaFilterer` exists for extension parity and
  leaves room for future per-filter specifics.

- **`Run *RunComponents`** — always allocated on a successful Process call.
  Carries `TotalRecords` (cohort pre-filter), `FilteredRecords` (survived
  the filter chain), `NullRecords` (dropped due to null input),
  `ShardCount` (zero for single-file cohorts), and `PartialCohortReason`
  (free-form diagnostic when a shard failed to open). Coexists with
  `Response.Metadata`: `Metadata.TotalRows` equals `Run.TotalRecords` by
  construction at every orchestrator exit. Metadata retains non-numerical
  run facts (cohort filename); Run carries the typed counters consumers
  compute against.

## Manifest declaration

`descriptor.Manifest.ComponentsSchemas` projects the per-operator components
contract at manifest level so LLM clients can plan
`ResponseComponents.Components[]` consumption in one O(N) scan without
crawling per-category Operator entries.

```go
type ComponentsSchemasBlock struct {
    Aggregators map[string]ComponentSchema `json:"aggregators,omitempty"`
    Groupers    map[string]ComponentSchema `json:"groupers,omitempty"`
    Filterers   map[string]ComponentSchema `json:"filterers,omitempty"`
}

type ComponentSchema struct {
    Keys         []ComponentKey         `json:"keys,omitempty"`
    Mergeability ComponentsMergeability `json:"mergeability"`
}

type ComponentKey struct {
    Name        string `json:"name"`        // snake_case wire key
    Type        string `json:"type"`        // "int", "float64", "WelfordTriple", "map[string]int"
    Description string `json:"description"` // one-sentence prose
}
```

Each map is keyed by operator name and sorted deterministically at
serialization time. `Keys` enumerates the operator-specific keys in
emission order; the universal floor (`{"n", "n_null"}`) is filled
unconditionally and is NOT listed in `Keys`. An empty `Keys` slice paired
with `Mergeability == Mergeable` is a valid declaration — it signals a
floor-only operator (AGG_COUNT is canonical).

`ComponentSchema` is declared per-operator in `descriptor/capabilities_*.go`
files (`capabilities_aggregators.go`, `capabilities_groupers.go`,
`capabilities_filterers.go`). The same `ComponentSchema` value appears
twice in the manifest by design — once inside the per-operator
`Operator` entry (so a category-by-category drilldown is self-contained)
and once inside the top-level `components_schemas` block (so a whole-
catalog scan is one fetch).

Fetch from MCP via `pulse_manifest` (cached for the session) or from CLI
via `pulse manifest --json | jq .components_schemas`.

## Mergeability

`ComponentsMergeability` (defined in `types/streamability.go`, re-exported
as `descriptor.Mergeable` / `descriptor.Partial` / `descriptor.None`)
classifies how an operator's components fold across streaming chunks and
parallel-shard partitions:

| Constant | Wire value | Semantics | Canonical operators |
|---|---|---|---|
| `Mergeable` | `"mergeable"` | Components fold via the same associative/commutative path as the scalar value. Constant-space `MergeOnline` works across chunks. Safe to emit per-chunk and merge online. | AGG_SUM, AGG_COUNT, AGG_WELFORD, AGG_WEIGHTED_MEAN, AGG_RATIO, AGG_SET_UNION, AGG_SET_CARDINALITY_SUM |
| `Partial` | `"partial"` | Components fold across chunks but at non-trivial allocation cost — map / set unions where the merge is associative but not constant-space. Orchestrator may stage merge at terminal flush. | AGG_FREQUENCY, AGG_MODE, AGG_DISTINCT_COUNT, AGG_SET_FREQUENCY |
| `None` | `"none"` | Components cannot be computed from a per-chunk partial — the operator needs a sorted view (or equivalent) of the full input. Streaming chunks omit components; emission lands only on the terminal buffered flush. | AGG_MEDIAN, AGG_PERCENTILE, GROUP_QUANTILE |

Predict surfaces a per-slot `BufferedComponents` flag that is
`(Mergeability == None)` — an LLM client checks this once when planning a
streaming request to know which slots will receive components only on the
terminal chunk.

## Streaming behavior

`pulse.ProcessStream` chunks carry `Components *types.ResponseComponents`
alongside the chunk's row payload. Behaviour by mergeability class:

- **Mergeable aggregators / groupers** — every chunk carries the running
  state. A consumer that needs intermediate visibility can read each
  chunk's `Components` and either render it directly or merge it across
  chunks using the same `MergeOnline` path the orchestrator uses
  internally. The terminal chunk is byte-equal to the buffered `Process`
  result.

- **Partial-merge aggregators (`AGG_FREQUENCY`, `AGG_MODE`,
  `AGG_DISTINCT_COUNT`, `AGG_SET_FREQUENCY`)** — chunks 1..N-1 carry the
  per-chunk partial maps. The terminal chunk carries the merged final.
  Consumer-side merge of per-chunk maps is supported (associative union)
  but optional — the terminal chunk is authoritative.

- **Non-mergeable aggregators (`AGG_MEDIAN`, `AGG_PERCENTILE`,
  `GROUP_QUANTILE`)** — chunks 1..N-1 emit `Operator: nil` on the
  affected entry. The terminal chunk carries the full buffered final.
  Streaming consumers that need these values MUST wait for the terminal
  chunk; predict flags the slot as `BufferedComponents: true` so the
  client knows upfront.

In every case the terminal chunk is byte-equal to the buffered `Process`
result for the same Request — streaming is a presentation layer over the
same compute path, never a divergent code path.

Parallel shards (`pulse.Options.ShardWorkers`) and parallel-segment
buffered decode (`pulse.Options.DecodeWorkers`) merge per-shard /
per-segment partials through the same path. Mergeability-axis applies
identically: mergeable folds without buffering, partial allocates, none
falls back to single-pass at the terminal merge step.

## Overlay parity reads

The four stat-test parity overlays —
`OVERLAY_T_CELL`, `OVERLAY_Z_CELL`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF` —
read `{n, mean, variance}` directly from
`Response.Components.Crosstab.CellComponents[r][c]`. This is the canonical
audit-aligned path for the Welch t-test parity family and the
two-sample z-test parity family.

The legacy `processing.WelfordTriple` typed payload smuggled inside
`MatrixCell.Value` is GONE. `MatrixCell.Value` for `AGG_WELFORD` cells now
carries the scalar mean — same shape as every other aggregator's cell
payload (the value the operator's `Aggregate()` contract returns). The
side channel that complicated the cell contract is eliminated; the
compute formulas (Welch t-test, two-sample z-test) are unchanged —
only the source of `{n, mean, variance}` moved from
`MatrixCell.Value.(processing.WelfordTriple)` to a typed read against
`CellComponents[r][c]["n"]`, `["mean"]`, `["variance"]`.

The handler still falls through to the scalar-plus-Params path when the
cell carries no Components payload at the coordinate (or the keys are
absent) — `MatrixCell.Value` returns the running mean and variance + n
come from `Params["variance_*"]` / `Params["sample_size_*"]`. The
additive contract is preserved: scalar cell + Params-supplied mean /
variance / N still works when no triple is attached.

See `skills/overlay-system.md` Migration note for the full per-side
resolver flow and worked examples.

## Extension contract

Embedder extensions registered via `pulse.Options.Extensions` declare a
`ComponentSchema` at registration time and implement either
`ComponentsFunc` (the closure form) or the matching sibling interface
(`processing.MetaAggregator`, `MetaGrouper`, `MetaFilterer`). Probe-
validation at `pulse.New()` checks parity between declaration and runtime
emission:

- `PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA` — the registration supplied
  a runtime emitter (`ComponentsFunc` non-nil or sibling interface
  implemented) but `ComponentSchema.Keys` is empty. Either declare the
  keys or drop the emitter.

- `PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH` — the runtime probe
  emitted a key set that diverges from the declared schema. Fix by
  aligning the emitter's `Components()` return to the declared key list,
  in declared order.

Floor-only extensions are valid: when both `ComponentSchema.Keys` is
empty and `ComponentsFunc` is nil (and no sibling interface is
implemented), the orchestrator fills the universal floor only — no
probe-validation error fires. This is the AGG_COUNT-equivalent shape
for extensions.

The manifest's `extensions` block surfaces per-extension
`ComponentSchema` projections; predict reads the same projection via
`descriptor.ExtensionsSnapshot`. Schema-bound MCP tools include
extension operator names in the per-category enums so an LLM client can
plan against the union of built-in + extension catalogs.

See `skills/extension-points.md` for the full registration recipe and
the `FieldInputs` projection hook.

## Examples

**Read every aggregation's components — label-join + operator-key lookup.**

```go
resp, _ := p.Process(ctx, req)
for _, a := range resp.Components.Aggregations {
    fmt.Printf("%-20s  n=%d  n_null=%d", a.Label, a.N, a.NNull)
    if mean, ok := a.Operator["mean"].(float64); ok {
        fmt.Printf("  mean=%.4f", mean)
    }
    if variance, ok := a.Operator["variance"].(float64); ok {
        fmt.Printf("  variance=%.4f", variance)
    }
    fmt.Println()
}
```

**Read a Welford triple from a crosstab cell — the canonical parity-overlay
read path.**

```go
ct := resp.Components.Crosstab
if ct != nil && r < len(ct.CellComponents) && c < len(ct.CellComponents[r]) {
    cell := ct.CellComponents[r][c]
    if cell != nil {
        n := cell["n"].(int)
        mean := cell["mean"].(float64)
        variance := cell["variance"].(float64)
        // ... pass to Welch t-test or two-sample z-test machinery
    }
}
```

**Check run-level cohort totals — partial-cohort diagnostic.**

```go
r := resp.Components.Run
fmt.Printf("total=%d  filtered=%d  null=%d  shards=%d\n",
    r.TotalRecords, r.FilteredRecords, r.NullRecords, r.ShardCount)
if r.PartialCohortReason != "" {
    fmt.Printf("partial cohort: %s\n", r.PartialCohortReason)
}
```

**Filter chain audit — per-stage attrition.**

```go
for i, f := range resp.Components.Filterers {
    fmt.Printf("[%d] %-20s  %d -> %d  (null_in=%d, drop=%d)\n",
        i, f.Label, f.NIn, f.NOut, f.NNullInput, f.NIn-f.NOut)
}
```

**Streaming consumer — fold mergeable + wait for terminal on non-mergeable.**

```go
stream, _ := p.ProcessStream(ctx, req)
var finalComponents *types.ResponseComponents
for chunk := range stream.Chunks() {
    if chunk.Components != nil {
        // Mergeable / Partial: chunk.Components is the running view.
        // None: chunk.Components.Aggregations[i].Operator is nil for the
        //   median / percentile slot until the terminal chunk.
        finalComponents = chunk.Components
    }
}
// finalComponents now byte-equal to buffered Process result.
```

## Cross-links

- [aggregation-guide](aggregation-guide.md) — per-AGG component keys + the
  per-category Components section model
- [grouper-design](grouper-design.md) — per-GROUP component keys
- [crosstab-guide](crosstab-guide.md) — `CrosstabComponents` indexing
  contract + cell vs margin vs grand-total layout
- [overlay-system](overlay-system.md) — parity-overlay migration (the four
  `OVERLAY_*_CELL` / `OVERLAY_*_VS_REF` kinds reading from
  `CellComponents`)
- [extension-points](extension-points.md) — extension `ComponentSchema`
  registration, probe-validation errors, and `FieldInputs` projection
