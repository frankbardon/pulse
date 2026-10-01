---
name: aggregation-design
description: Aggregation + filterer slot semantics — what `aggregations` does, how it composes with `filterers`, when smart defaults fire, when to use `attributes` instead. Topical design; per-AGG / per-FILTER detail lives in atomic op-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [AGG, FILTER, aggregations, filterers]
---

# Aggregation design

`aggregations` and `filterers` drive `Response.Data` rows and per-stage counters in `Response.Components`. Design contract here; per-operator detail in atomic `op-agg-*` / `op-filter-*` skills.

## Slot identity

| Slot | Type | When | Output |
|---|---|---|---|
| `filterers` | `[]types.Filterer` | Before grouping + aggregation | drops rows; counters → `Components.Filterers[i]` |
| `aggregations` | `[]types.Aggregation` | After grouping | scalar (or Rich payload) per group → `Response.Data` |

Request order: `features → filterers → attributes → groups → aggregations → windows → sort`. Filters precede aggregation by contract.

## Shapes

`aggregations` entry: `{type, field, label, params?}`. `type` is the operator (`AGG_SUM`, `AGG_WELFORD`, ...); `field` is the source column; `label` names the output cell; `params` carries op-specific knobs.

`filterers` entry: `{type, field?, values?, expression?, label}`. Only keys relevant to each filterer's `type` are consulted. 11 filterers cover include/exclude, range, null-presence, boolean truthiness, expression predicate, and four `set_*` membership ops.

## Chaining

Without `groups`, one aggregation produces ONE scalar per Request. With `groups`, one scalar per group key.

`filterers` chain in declared order — the next filterer sees only rows the previous kept. Counters fold: `n_in[i] == n_out[i-1]`, `n_in[0]` == decoded record count. Reconstruct the funnel without re-reading the Request.

## Smart defaults

When an `aggregations` entry names `field` but omits `type`, the engine infers from schema type: numeric → `AGG_SUM`, categorical / packed-bool → `AGG_FREQUENCY`, `set_*` → `AGG_SET_FREQUENCY`, `date` → never defaulted. `filterers` are never defaulted.

Predict reports filled slots under `data.defaults_applied`. Disable via `pulse.Options{DisableDefaults: true}` / `--no-defaults`. Full table: `request-envelope`.

## `attributes` vs `aggregations`

- **`attributes`** add a derived COLUMN per row (z-score, formula, percentile rank). Per-record. Does not collapse groups.
- **`aggregations`** collapse rows inside a group to a SCALAR (or rich payload). Per-group.

Compose both when a derived column must flow into aggregation: declare the attribute first (`attributes` runs before `groups`), then aggregate its label. See `attribute-composition`.

## Components contract (v0.20.0)

Every `Response.Components.Aggregations[i]` carries a **universal floor**: `n` (non-null inputs) + `n_null` (null inputs). Filled by the orchestrator's floor pass — operator code only emits its declared per-operator keys. Floor-only operators (`AGG_COUNT`) leave the operator map empty; consumers still see `{n, n_null}`.

Operator-specific keys ride inside `Operator map[string]any`. Authoritative key list is declared per operator on its `ComponentSchema` in `descriptor/capabilities_aggregators.go` and mirrored at `manifest.components_schemas.aggregators[<op>].keys`. Per-AGG semantics live in the atomic `op-agg-*` skill.

Every aggregator declares one mergeability classification:

- **`Mergeable`** — folds across chunks via the same `MergeOnline` path as the scalar. Streaming chunks carry `ComponentsDelta`. Welford family, sums / counts / extrema, CI bounds, weighted-mean, ratio, mergeable set-* ops.
- **`Partial`** — map / set merges staged at terminal flush. `AGG_FREQUENCY`, `AGG_MODE`, `AGG_DISTINCT_COUNT`, `AGG_DISTINCT_SUM`, `AGG_SET_FREQUENCY`.
- **`None`** — non-mergeable; components emit only on terminal buffered flush. `AGG_MEDIAN`, `AGG_PERCENTILE`. Predict surfaces `BufferedComponents=true`.

Three examples of the same floor surfacing different `Operator` payloads:

- `AGG_SUM` (mergeable) — `Operator = {sum}`. Floor + one running scalar.
- `AGG_WELFORD` (mergeable) — `Operator = {mean, m2, variance, stddev}`. Floor + Welford-Pébaÿ triple; `(mean, variance, n)` byte-equal to `TEST_WELCH`.
- `AGG_FREQUENCY` (partial) — `Operator = {distinct_count, mode_value, mode_count}`. Floor + map-merge results at terminal flush.

`Response.Components.Filterers[i]` carries a uniform `{n_in, n_out, n_null_input}` floor across every filterer; no per-operator slot today.

Full Components contract — typed shells, indexing rules, mergeability axis, streaming chunk behaviour — lives in `response-components`.

## Type admissibility

Each aggregator declares accepted schema types. Numeric-only ops on a categorical field surface `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`; count / frequency / mode are universally accepted. Decimal inputs route through a precision-preserving path that may fall back to `f64` with `PULSE_PRECISION_LOSS` — see `financial-cohorts`. Per-AGG admit/reject tables live in atomic skills.

## Gotchas

- Defaults never cross categories — categorical `field` with `type` omitted gets `AGG_FREQUENCY`, not `AGG_SUM`.
- Filterers chain in declared order; reordering changes per-stage counters but not the final row set.
- Filterers can't see attribute output (`filterers` runs before `attributes`). To filter a derived column, use `FILTER_EXPRESSION` on source fields, or stage via Compose / ProcessChain.
- Forced-buffered ops (`AGG_MEDIAN`, `AGG_PERCENTILE`, decimal paths) materialize the union of shards on a shard archive — memory scales with shard count.
- Tiny groups produce unstable stats. Pair non-trivial aggregations with `AGG_COUNT`; higher moments need `n ≥ 2`.

## See

- Recipes: `pulse_examples_search tags=["cohort-analysis"]`, `tags=["welford-triple"]`, `tags=["welch"]`, `tags=["pre-filter"]` plus atomic `op-agg-<name>` / `op-filter-<name>`.
- `request-envelope` — slot keys, smart defaults, streamability.
- `response-components` — Components contract + per-family typed shape.
- `attribute-composition` — when to derive a column instead of aggregating.
- `grouper-design`, `financial-cohorts`, `cohort-schema-design` — partitions, decimal rules, `set_*` + sharded buffered-op memory.
