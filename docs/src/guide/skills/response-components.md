```yaml
name: response-components
description: How Response.Components carries the constituent parts of every aggregation, grouper, filterer, and crosstab cell
kind: design
type: guide
applies_to: process, compose, predict, sample, facet
```

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

Weighted slots (crosstab cells and margins too) add `sum_weights` / `n_eff` / `n_weight_invalid`. An aggregator's `n` counts NON-NULL inputs (a paired-input slot: of `field` alone); a grouper's `total_n` counts every post-filter record it partitioned and `n_null` those that took the null / skip path. Operator keys (`mean`, `mode_count`, …) ride `operator` on the aggregation / grouper shells, or the cell map directly.

## Sub-blocks

`ResponseComponents` = `aggregations` · `groupers` · `crosstab` · `filterers` · `run` · `matrices` · `attributes` — every slot `omitempty`.

| Block | Cardinality · identity | Carries |
|---|---|---|
| `aggregations` | one per `Request.Aggregations`, declared order · `label` | floor + `operator`; grouped: cohort-wide floor + `groups` (below) |
| `groupers` | one per `Request.Groups`, declared order · `field` (+ `label` when a field repeats) | floor + `operator` (bucket edges, dictionary mappings, `buckets`) |
| `crosstab` | only when the Request carried `crosstab` | cell / margin / axis-key components (below) |
| `filterers` | one per `Request.Filterers`, declared order · `label` | floor only |
| `run` | always on a successful run | `total_records` (pre-filter), `filtered_records`, `null_records`, `shard_count` (0 = single file), `partial_cohort_reason` (a shard failed to open) |
| `matrices` | one per `Response.Matrices` result, same order · `name` | `n` (weight-0 rows count), `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted floor; `operator` |
| `attributes` | one per attribute slot whose operator declares a schema · `label` + `type`, NOT index | `n` / `n_null` (present / null outputs) + `operator`; cohort-wide, never per bucket |

- **Grouped runs** (`Request.Groups`, no crosstab): each `aggregations[i]` carries the COHORT-WIDE floor (all filter-passing records, no `operator`) and `groups[]` — one `{group_key, n, n_null, weighted floor, operator}` per `Data` row, in `Data` order (`sort` included), equal to an ungrouped run over that bucket. A stream: terminal chunk only.
- Several `groups`: `group_key` is the tuple; one `groupers` entry per slot.
- A fan-out grouper's bucket sum may EXCEED `total_n`.
- `run` coexists with `Response.Metadata`: `Metadata.TotalRows == Run.TotalRecords`; `Metadata` keeps non-numerical facts (cohort filename), `run` the counters.

## Crosstab block

Mirrors the matrix coordinate-for-coordinate (cells, margins, grand total, axis keys); an empty cell's `CellComponents[r][c]` is `nil`. Indexing: [`crosstab-guide`](crosstab-guide.md).

**`CellCounts[r][c]` is a RECORD count; the floor's `n` counts NON-NULL observations — `CellCounts[r][c] == n + n_null`.** Never read it as the sample size (raw under a weight; Σw is `sum_weights`); same on every margin.

A vector `element` cell: its member's floor and `element_*`. Auxiliary margin-only figures land BESIDE the margin components, and a record reaches an auxiliary margin only if it reached a CELL: [`crosstab-margin-aggregations`](crosstab-margin-aggregations.md).

## Opting out

| Knob | Scope |
|---|---|
| `pulse.Options.DisableComponents bool` | engine default for every request the instance runs |
| `types.Request.DisableComponents *bool` | per request — `nil` inherits, `true` forces off, `false` forces ON even on an engine shipping them off |
| `--no-components` | CLI, on `pulse api process` / [`process-chain`](process-chain.md) / `compose`; request JSON `"disable_components"` wins over it |

Disabled ⇒ `Response.Components` stays `nil` (wire form byte-identical to the pre-Components baseline; the work is skipped). Consumers MUST tolerate a `nil` block. Compose: per `requests[i]`. MCP tools do not surface the knob.

## Manifest declaration

`manifest.components_schemas` holds operator-name-keyed maps — `aggregators`, `groupers`, `filterers`, `matrices`, `attributes`. Each value is a `ComponentSchema`: `keys` (each `{name, type, description}`) in emission order, plus `mergeability`. Aggregators, attributes and matrices list their floor; empty `keys` is a valid floor-only operator.

## Mergeability

`mergeability` classifies how an operator's components fold across streaming chunks and parallel partitions.

| Class | Wire | Meaning for a consumer |
|---|---|---|
| `Mergeable` | `"mergeable"` | constant-space running state; every chunk renders |
| `Partial` | `"partial"` | grows (maps / sets); terminal authoritative |
| `None` | `"none"` | needs the full sorted input; keys only at terminal flush |

Predict flags each `None` slot `buffered_components: true`. Per-chunk behaviour: [`streaming-and-watching`](streaming-and-watching.md).

**Worker counts never change the answer.** Examples: `pulse_examples_search tags=["welford-triple"]`.

## Extensions

An extension operator declares a `ComponentSchema` and supplies the keys it emits; `pulse.New` probes declaration against emission and refuses a mismatch. Recipe and codes: `docs/src/internals/extension-points.md`.

## See

[`aggregation-design`](aggregation-design.md) (per-aggregator keys) · [`grouper-design`](grouper-design.md) (grouper floor) · [`crosstab-guide`](crosstab-guide.md) (crosstab indexing) · [`crosstab-margin-aggregations`](crosstab-margin-aggregations.md) (auxiliary margin figures) · [`streaming-and-watching`](streaming-and-watching.md) (per-chunk components) · [`overlay-system`](overlay-system.md) (overlays that read cell components).
