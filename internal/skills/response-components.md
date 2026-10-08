---
name: response-components
description: How Response.Components carries the constituent parts of every aggregation, grouper, filterer, and crosstab cell
kind: design
type: guide
applies_to: process, compose, predict, sample, facet
---

# `Response.Components`

The constituent parts behind every emitted value. Reach for it when a figure must be checked or re-based: "what n is this mean over?". This skill is the shared shape; per-operator keys live in each atomic skill's `## Components` and in `manifest.components_schemas`.

Additive `omitempty` — a run producing nothing components-shaped emits no `components` key. Nothing here moves `format_version` (`"1.1"`).

## Universal floor

Filled by the ENGINE, not the operator — every slot carries it, even a floor-only operator.

| Shell | JSON keys |
|---|---|
| `AggregationComponents` | `n`, `n_null` |
| `GrouperComponents` | `total_n`, `n_null` |
| `FiltererComponents` | `n_in`, `n_out`, `n_null_input` |
| crosstab cell (`CellComponents[r][c]`) | `n`, `n_null` |

<!-- feature: capability:weighting -->Weighted slots (crosstab cells and margins too) add `sum_weights` / `n_eff` / `n_weight_invalid`. <!-- /feature -->An aggregator's `n` counts NON-NULL inputs; a grouper's `total_n` counts every post-filter record it partitioned and `n_null` those that took the null / skip path. Operator keys (`mean`, `mode_count`, …) ride `operator` on the aggregation / grouper shells, or the cell map directly.

## Sub-blocks

`ResponseComponents` = `aggregations` · `groupers` · `crosstab` · `filterers` · `run`<!-- feature: capability:matrices --> · `matrices`<!-- /feature --> — every slot `omitempty`.

| Block | Cardinality · identity | Carries |
|---|---|---|
| `aggregations` | one per `Request.Aggregations`, declared order · `label` | floor + `operator`; grouped: cohort-wide floor + `groups` (below) |
| `groupers` | one per `Request.Groups`, declared order · `field` (+ `label` when a field repeats) | floor + `operator` (bucket edges, dictionary mappings, `buckets`) |
| `crosstab` | only when the Request carried `crosstab` | cell / margin / axis-key components (below) |
| `filterers` | one per `Request.Filterers`, declared order · `label` | floor only |
| `run` | always on a successful run | `total_records` (pre-filter), `filtered_records`, `null_records`, `shard_count` (0 = single file), `partial_cohort_reason` (a shard failed to open) |
<!-- feature: capability:matrices -->
| `matrices` | one per `Response.Matrices` result, same order · `name` | `n` (weight-0 rows count), `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted floor; `operator` |
<!-- /feature -->

- **Grouped runs** (`Request.Groups`, no crosstab): each `aggregations[i]` carries the COHORT-WIDE floor (every filter-passing record, no `operator`) and `groups[]` — one `{group_key, n, n_null, weighted floor, operator}` per `Data` row, in `Data` order (`sort` included), equal to an ungrouped run over that bucket. A stream carries `groups[]` on its terminal chunk only.
- A fan-out grouper (one record → several buckets, e.g. each option of a multi-select) has a bucket sum EXCEEDING `total_n` — correct.
- `run` coexists with `Response.Metadata`: `Metadata.TotalRows == Run.TotalRecords`; `Metadata` keeps non-numerical run facts (cohort filename), `run` the typed counters.

## Crosstab block

Mirrors the matrix coordinate-for-coordinate (cells, row / column margins, grand total, axis keys); `CellComponents[r][c]` is `nil` for an empty cell.<!-- feature: capability:crosstab --> Indexing: `crosstab-guide`.<!-- /feature -->

**`CellCounts[r][c]` is a RECORD count; the floor's `n` counts NON-NULL observations — `CellCounts[r][c] == n + n_null`.** Never read `CellCounts` as the aggregator's sample size (it stays raw under a weight; Σw is `sum_weights`). Same split on every margin counterpart.

Auxiliary margin-only figures land BESIDE the margin components, and a record reaches an auxiliary margin only if it reached a CELL<!-- feature: capability:crosstab -->: `crosstab-margin-aggregations`<!-- /feature -->.

## Opting out

| Knob | Scope |
|---|---|
| `pulse.Options.DisableComponents bool` | engine default for every request the instance runs |
| `types.Request.DisableComponents *bool` | per request — `nil` inherits, `true` forces off, `false` forces ON even on an engine shipping them off |
| `--no-components` | CLI, on `pulse api process`<!-- feature: capability:process_chain --> / `process-chain`<!-- /feature --><!-- feature: capability:compose --> / `compose`<!-- /feature -->; request JSON `"disable_components"` wins over it |

Disabled ⇒ `Response.Components` stays `nil` (wire form byte-identical to the pre-Components baseline; the work is skipped). Streaming consumers MUST tolerate a `nil` block. Compose: each `requests[i]` carries its own override. MCP tools do not surface the knob.

## Manifest declaration

`manifest.components_schemas` holds operator-name-keyed maps — `aggregators`, `groupers`, `filterers`<!-- feature: capability:matrices -->, `matrices`<!-- /feature -->. Each value is a `ComponentSchema`: `keys` (each `{name, type, description}`) in emission order, plus `mergeability`. Aggregators<!-- feature: capability:matrices --> and matrices<!-- /feature --> list their floor; empty `keys` is a valid floor-only operator.

## Mergeability

`mergeability` classifies how an operator's components fold across streaming chunks and parallel partitions.

| Class | Wire | Meaning for a consumer |
|---|---|---|
| `Mergeable` | `"mergeable"` | constant-space running state; every chunk renders |
| `Partial` | `"partial"` | grows (maps / sets); terminal authoritative |
| `None` | `"none"` | needs the full sorted input; keys only at terminal flush |

Predict flags each `None` slot with `buffered_components: true` — check it once when planning a streaming request. Per-chunk behaviour: `streaming-and-watching`.

**Worker counts never change the answer** — parallel runs emit the blocks a serial run emits. Examples: `pulse_examples_search tags=["welford-triple"]`.

## Extensions

An extension operator declares a `ComponentSchema` and supplies the keys it emits; `pulse.New` probes declaration against emission and refuses a mismatch. Recipe and codes: `docs/src/internals/extension-points.md`.

## See

`aggregation-design` (per-aggregator keys) · `grouper-design` (grouper floor)<!-- feature: capability:crosstab --> · `crosstab-guide` (crosstab indexing)<!-- /feature --><!-- feature: capability:crosstab --> · `crosstab-margin-aggregations` (auxiliary margin figures)<!-- /feature --> · `streaming-and-watching` (per-chunk components) · `overlay-system` (overlays that read cell components).
