---
name: response-components
description: How Response.Components carries the constituent parts of every aggregation, grouper, filterer, and crosstab cell
kind: design
type: guide
applies_to: process, compose, predict, sample, facet
---

# `Response.Components`

The constituent parts behind every emitted value — aggregator scalars, grouper bucket counts, filterer N-in/N-out, crosstab cells — auditable from the envelope that delivered the value. Canonical source for the cross-cutting shape; per-operator key tables live in each `op-*` atomic's `## Components` section and in `manifest.components_schemas`.

Always-on for built-ins, required for extension operators (probe-validation rejects an emitter with no matching `ComponentSchema`). Additive `omitempty` throughout — `Response.Components` is `*ResponseComponents`, so a run producing nothing components-shaped emits no `components` key and is byte-identical to the pre-Components wire form. Additive keys never bump `format_version` (`"1.1"`, moved by the Compose facade lift, not by anything here); CLAUDE.md "Output Format Contract" is the source.

## Universal floor

Filled by the ORCHESTRATOR, not by the operator's `Components()` — a no-op implementation or a floor-only extension still surfaces it. Filled unconditionally even when `ComponentSchema.Keys` is empty; `AGG_COUNT` is the canonical floor-only aggregator.

| Shell | Floor fields | JSON keys |
|---|---|---|
| `AggregationComponents` | `N int`, `NNull int` | `n`, `n_null` |
| `GrouperComponents` | `TotalN int`, `NNull int` | `total_n`, `n_null` |
| `FiltererComponents` | `NIn int`, `NOut int`, `NNullInput int` | `n_in`, `n_out`, `n_null_input` |
| crosstab cell (`CellComponents[r][c] map[string]any`) | `n int`, `n_null int` | `n`, `n_null` |

Aggregation / crosstab-cell floor is therefore `{n, n_null}`.

Operator-specific keys (`mean`, `variance`, `mode_count`, `range_min`, `range_max`, …) ride `Operator map[string]any` on the aggregation / grouper shells, or the cell map directly, per `descriptor.Manifest.ComponentsSchemas`.

## Five sub-blocks

`ResponseComponents` = `Aggregations []AggregationComponents` (`aggregations`) · `Groupers []GrouperComponents` (`groupers`) · `Crosstab *CrosstabComponents` (`crosstab`) · `Filterers []FiltererComponents` (`filterers`) · `Run *RunComponents` (`run`) — every slot `omitempty`.

| Block | Cardinality · slot identity | Carries |
|---|---|---|
| `Aggregations` | one per `Request.Aggregations`, declared order · `Label` (mirrors `Aggregation.Label`) | floor + `Operator`; per-AGG keys in `aggregation-design` |
| `Groupers` | one per `Request.Groups`, declared order · `Field` + `Label` when a field repeats | floor + `Operator` (bucket edges, dict mappings, `range_min`/`range_max`); per-GROUP keys in `grouper-design` |
| `Crosstab` | only when the Request carried `Crosstab` | below + `crosstab-guide` |
| `Filterers` | one per `Request.Filterers`, declared order · `Label` | floor only — no `Operator` slot today; `MetaFilterer` exists for extension parity |
| `Run` | always on a successful `Process` | `TotalRecords` (pre-filter), `FilteredRecords`, `NullRecords`, `ShardCount` (0 = single file), `PartialCohortReason` (free-form; a shard failed to open) |

`TotalN` = sum of bucket counts for a single-key grouper; for a multi-key streaming grouper (`GROUP_SET_PER_ELEMENT`) the bucket sum EXCEEDS `TotalN` — one record lands in several buckets. `Run` coexists with `Response.Metadata`: `Metadata.TotalRows == Run.TotalRecords` at every orchestrator exit; `Metadata` keeps non-numerical run facts (cohort filename), `Run` the typed counters.

## Crosstab block

Mirrors `MatrixPayload` coordinate-for-coordinate — same `(r, c)` tuple as `Cells` / `RowMargins` / `ColumnMargins` / `GrandTotal`. Slots: `CellCounts[r][c]`, `CellComponents[r][c]` (`nil` for an empty cell), `RowMarginCounts` / `RowMarginComponents` (column symmetric), `GrandTotalCount` / `GrandTotalComponents`, `RowKeyComponents` / `ColumnKeyComponents`, sanity counters `IncludedRecords` / `ExcludedRecords`. Full indexing contract: `crosstab-guide`.

**`CellCounts[r][c]` is a RECORD count; the floor's `n` counts NON-NULL observations — `CellCounts[r][c] == n + n_null`.** Never read `CellCounts` as the aggregator's sample size. Same split on every margin counterpart.

### Auxiliary margin figures

Present when the Request declared `crosstab.margin_aggregations`: `RowMarginAggregations[r]` / `ColumnMarginAggregations[c]` / `GrandTotalAggregations`, one map per margin slot keyed by each auxiliary's effective label (`label`, else `TYPE_field`; validators force it unique across the slot and distinct from the cell's, so a label addresses exactly one figure). Entry = `MarginAggregationFigure{value, present, components}`; `components` is the floor over ADMITTED records merged with that operator's `ComponentSchema` keys, so an `AGG_DISTINCT_SUM` auxiliary's `distinct_count` is reachable PER MARGIN SLOT beside the scalar sum — two rendered figures off one scan.

**ADMISSION RULE — least guessable property of this surface. A record contributes to an auxiliary ONLY IF IT CONTRIBUTED TO A CELL.** Two exclusions follow and they are the entire difference from the margin components beside it: a record whose CELL FIELD IS NULL, and a record whose AXIS KEY A GROUPER `Include` EXCLUDED. The cell's own margins count both. Deliberate, no knob — an auxiliary is a base the cells beside it are read against, so it must see the records they saw; a cohort-wide base answers a different question while looking identical. Reading an auxiliary's `n` as a cohort count is wrong SILENTLY: every figure still renders, only the base is off. Both arms implement the rule and must AGREE — dispatch picks fused or buffered on request SHAPE and nothing in `Response` reports which ran.

- They sit BESIDE `RowMarginComponents[r]`, never inside it: that is the CELL aggregator's own margin, counting every filter-passing record routed to that row — a DIFFERENT admission. Merging the key sets would file two differently-based figures under one roof with nothing saying so.
- **`present` is load-bearing, not a zero value.** A slot admitting no record carries `present: false` and NO `value` key — an aggregator over an empty set has no defined output, and a fabricated `0` is indistinguishable on the wire from a real one. Its `components` still carry the floor, whose `n = 0` is a true statement about the slot.
- **Allocation is wider than emission; the gap is warned, not closed.** Both arms ALLOCATE on `NeedsRowMargin` / `NeedsColumnMargin` / `NeedsGrandMargin` (display OR normalize); EMISSION rides the display flag alone (`margins.rows` / `.columns` / `.grand`), the rule the cell's own margin counts and components already follow — a margin computed only as a normalize denominator stays off the wire on both. So margins-all-false + `normalize=row|column|total` + a declared auxiliary accumulates every figure and emits none; `pulse predict` warns `PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED` there. Its predicate `types.CrosstabSpec.MarginAggregationsObserved` reads the DISPLAY flags and deliberately NOT the `Needs*Margin` trio, which answers the CELL's question — an auxiliary is never a denominator, so a normalize direction is not a landing site. **Expecting figures and the block is absent? check the display flag first.**
- Declaring no auxiliary emits none of the three: byte-identical to the pre-slot form, `format_version` unmoved.

## Opting out

| Knob | Scope |
|---|---|
| `pulse.Options.DisableComponents bool` | engine default for every request the instance runs |
| `types.Request.DisableComponents *bool` | per-request override — `nil` inherits, `true` forces off, `false` forces ON even on an engine shipping them off. The pointer is what separates "inherit" from "explicit false" |
| `--no-components` | CLI, on `pulse api process` / `pulse api process-chain` / `pulse api compose`; request JSON `"disable_components": true` / `false` wins over it |

`effective = req.DisableComponents != nil ? *req.DisableComponents : opts.DisableComponents`.

Disabled ⇒ `Response.Components` stays `nil`, wire form byte-identical to the pre-Components baseline, `format_version` NOT bumped. The gate sits at each execution path's emission block (`processStreaming` / `processStreamingGrouped` / `processStreamingTwoPass` / `processRecords` exits, plus `RunCrosstab` and `FusedCrosstabState.Finalize`), so `MetaAggregator.Components` / `MetaGrouper.Components` construction is SKIPPED — not built then discarded. Parallel-buffered and per-shard reducers gate at `service.attachMergedRunComponents` via a `disableComponents` parameter threaded through `finalizeMergedPartial`. `pulse.ProcessStream` inherits the nil block, so `.Components()` returns `nil` on terminal flush — streaming consumers MUST tolerate it. Compose: each `ComposedRequest.Requests[i]` carries its own override; the Compose-host overlay fold treats per-slot Components read-only, so dropping the slot never interacts with `Overlays[i].Warnings`. MCP tools do NOT surface the knob (matches `DisableDefaults` / `EchoRequest` — Options-level knobs stay off the tool parameter surface); embedders set it at `pulse.New()`.

## Manifest declaration

`descriptor.Manifest.ComponentsSchemas` is a `ComponentsSchemasBlock` of three operator-name-keyed maps: `Aggregators` (`json:"aggregators,omitempty"`), `Groupers` (`json:"groupers,omitempty"`), `Filterers` (`json:"filterers,omitempty"`). Each value is a `ComponentSchema` = `Keys []ComponentKey` (`json:"keys,omitempty"`) + `Mergeability ComponentsMergeability` (`json:"mergeability"`). Each `ComponentKey` = `Name` (`json:"name"`, snake_case wire key) + `Type` (`json:"type"` — `"int"`, `"float64"`, `"WelfordTriple"`, `"map[string]int"`) + `Description` (`json:"description"`, one-sentence prose).

Sorted deterministically at serialization. `Keys` is the operator-specific set in emission order; the floor (`{"n", "n_null"}`) is unconditional and NOT listed. Empty `Keys` with `Mergeability == Mergeable` is valid — a floor-only operator. Declared per operator in `descriptor/capabilities_*.go` (`capabilities_aggregators.go`, `capabilities_groupers.go`, `capabilities_filterers.go`). The same value appears TWICE by design: inside the per-operator `Operator` entry (self-contained drilldown) and in top-level `components_schemas`, so an LLM plans `ResponseComponents.Components[]` consumption in one O(N) scan. Fetch via `pulse_manifest` (session-cached) or `pulse manifest --json | jq .components_schemas`.

## Mergeability

`ComponentsMergeability` (`types/streamability.go`; re-exported `descriptor.Mergeable` / `descriptor.Partial` / `descriptor.None`) classifies the fold across streaming chunks and parallel-shard / parallel-segment partitions.

| Constant | Wire | Semantics | Canonical operators |
|---|---|---|---|
| `Mergeable` | `"mergeable"` | folds via the scalar's own associative/commutative path; constant-space `MergeOnline`; safe per chunk | `AGG_SUM`, `AGG_COUNT`, `AGG_WELFORD`, `AGG_WEIGHTED_MEAN`, `AGG_RATIO`, `AGG_SET_UNION`, `AGG_SET_CARDINALITY_SUM` |
| `Partial` | `"partial"` | associative but not constant-space (map / set unions); orchestrator may stage the merge at terminal flush | `AGG_FREQUENCY`, `AGG_MODE`, `AGG_DISTINCT_COUNT`, `AGG_DISTINCT_SUM`, `AGG_SET_FREQUENCY` |
| `None` | `"none"` | not computable from a per-chunk partial — needs a sorted (or equivalent) view of the full input | `AGG_MEDIAN`, `AGG_PERCENTILE`, `GROUP_QUANTILE` |

Predict surfaces per-slot `BufferedComponents` = `(Mergeability == None)`; check it once when planning a streaming request.

## Streaming

`pulse.ProcessStream` chunks carry `Components *types.ResponseComponents` beside the row payload. `Mergeable`: every chunk is the running state — render it, or fold consumer-side through the same `MergeOnline` path the orchestrator uses. `Partial`: chunks 1..N-1 carry per-chunk partial maps, consumer-side union supported but optional, terminal chunk authoritative. `None`: chunks 1..N-1 emit `Operator: nil` on the affected entry, so consumers MUST wait for the terminal chunk — predict flags the slot `BufferedComponents: true` upfront.

**In every class the terminal chunk is byte-equal to the buffered `Process` result for the same Request** — streaming is presentation over one compute path, never a divergent one. `pulse.Options.ShardWorkers` / `pulse.Options.DecodeWorkers` partials fold identically: mergeable without buffering, partial allocates, none falls back to single-pass at the terminal merge.

## Overlay parity reads

`OVERLAY_T_CELL` / `OVERLAY_Z_CELL` / `OVERLAY_T_VS_REF` / `OVERLAY_Z_VS_REF` read `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` — keys `CellComponents[r][c]["n"]`, `["mean"]`, `["variance"]`. Canonical audit-aligned path for the Welch t-test and two-sample z-test parity families.

The legacy `processing.WelfordTriple` smuggled inside `MatrixCell.Value` is GONE: an `AGG_WELFORD` cell's `MatrixCell.Value` now carries the scalar mean, like every other cell payload (what `Aggregate()` returns). Formulas unchanged — only the triple's source moved off `MatrixCell.Value.(processing.WelfordTriple)`. Additive fallback preserved: with no Components at the coordinate (or keys absent) the handler reads the scalar mean plus `Params["variance_*"]` / `Params["sample_size_*"]`. Resolver flow: `overlay-system` (Migration note).

## Extension contract

Extensions registered via `pulse.Options.Extensions` declare a `ComponentSchema` and implement either `ComponentsFunc` (closure) or the sibling interface (`processing.MetaAggregator` / `MetaGrouper` / `MetaFilterer`). Probe-validation at `pulse.New()` checks declaration-vs-emission parity:

| Code | Cause | Fix |
|---|---|---|
| `PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA` | emitter supplied (`ComponentsFunc` non-nil or sibling implemented) but `ComponentSchema.Keys` empty | declare the keys, or drop the emitter |
| `PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH` | probe emitted a key set diverging from the declaration | align `Components()` to the declared list, in declared order |

Floor-only extensions are valid: empty `Keys` + nil `ComponentsFunc` + no sibling interface ⇒ floor only, no probe error (the `AGG_COUNT`-equivalent shape). The manifest `extensions` block projects per-extension `ComponentSchema`; predict reads it via `descriptor.ExtensionsSnapshot`; schema-bound MCP tools carry extension names in the per-category enums.

## Reading it

```go
for _, a := range resp.Components.Aggregations {       // floor + operator keys
    mean, _ := a.Operator["mean"].(float64)
    fmt.Println(a.Label, a.N, a.NNull, mean)
}
for _, f := range resp.Components.Filterers {          // attrition: NIn -> NOut, NNullInput
    fmt.Println(f.Label, f.NIn, f.NOut, f.NNullInput)
}
if run := resp.Components.Run; run != nil {            // + FilteredRecords/NullRecords/ShardCount
    fmt.Println(run.TotalRecords, run.PartialCohortReason)
}
if ct := resp.Components.Crosstab; ct != nil {
    if cell := ct.CellComponents[row][col]; cell != nil { // nil for an empty cell — null-check first
        n, mean := cell["n"].(int), cell["mean"].(float64)
        _, _ = n, mean                                 // + cell["variance"] → Welch / two-sample z
    }
}
```

Runnable, `//go:embed`-registered and `TestExamples_*`-validated, so both surface via `pulse_examples_search` / `pulse_examples_get`: `examples/aggregations/08_welford_components.json` — `AGG_WELFORD` over `experiment.pulse#revenue`, value `{Mean, Variance, N}`, projection `{n, n_null, mean, m2, variance, stddev}`; `examples/crosstab/16_welford_components_revenue_by_region_treatment.json` — `region` × `treatment` (`GROUP_CATEGORY` both axes, all margins), `Crosstab` fully populated. Build fixtures once via `./examples/fixtures/build.sh`, then `bin/pulse api process --request <path> --json`. Emission benchmarks + regression frontier: `docs/src/ops/performance.md` (Components emission baselines).

## See

`aggregation-design` (per-AGG keys) · `grouper-design` (per-GROUP keys) · `crosstab-guide` (`CrosstabComponents` indexing, cell vs margin vs grand total) · `overlay-system` (parity migration) · `docs/src/internals/extension-points.md` (registration, probe errors, `FieldInputs`).
