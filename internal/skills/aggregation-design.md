---
name: aggregation-design
description: Aggregation + filterer slot semantics — what `aggregations` does, how it composes with `filterers`, when smart defaults fire, when to use `attributes` instead. Topical design; per-AGG / per-FILTER detail lives in atomic op-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [AGG, FILTER, aggregations, filterers]
---

# Aggregation design

`aggregations` and `filterers` drive `Response.Data` rows and per-stage counters in `Response.Components`. Per-operator detail: atomic `op-agg-*` / `op-filter-*` skills.

## Slot identity

| Slot | Type | When | Output |
|---|---|---|---|
| `filterers` | `[]types.Filterer` | Before grouping + aggregation | drops rows; counters → `Components.Filterers[i]` |
| `aggregations` | `[]types.Aggregation` | After grouping | scalar (or Rich payload) per group → `Response.Data` |

Request order: `features → filterers → attributes → groups → aggregations → windows → sort`.

## Shapes

`aggregations` entry: `{type, field, label, params?}`. `type` is the aggregator constant; `field` is the source column; `label` names the output cell; `params` carries op-specific knobs.

`filterers` entry: `{type, field?, values?, expression?, label}`. Only keys relevant to each filterer's `type` are consulted; the manifest lists the filterers this instance offers.

## Choosing an aggregator

Pick by the question, then let each operator's guidance name its alternatives. `pulse_skills_get intents` → `describe` (typical value, spread), `composition` (counts, shares), `distribution_shape` (skew, tails); keep the manifest `components.aggregators` entries carrying that intent, then read `op-agg-<name>`. Four criteria decide most cases:

- **Unit** — every row, each distinct key, or one particular value's rows?
- **Shape** — skewed or extreme values favour an order statistic over a mean.
- **Weights** — rows carrying weights need a weighted form.
- **Uncertainty** — need spread or an interval, not just a centre? Pick the Welford triple or CI bounds, which also feed tests.

## Chaining

Without `groups`, one aggregation yields ONE scalar per Request; with `groups`, one per group key.

`filterers` chain in declared order — the next filterer sees only rows the previous kept. Counters fold: `n_in[i] == n_out[i-1]`, `n_in[0]` == decoded record count. The funnel reconstructs without re-reading the Request.

## Smart defaults

When an `aggregations` entry names `field` but omits `type`, the engine infers from schema type: numeric → a sum, categorical / packed-bool → the modal count, `set_*` → the per-member set frequency, `date` → never defaulted. `filterers` are never defaulted.

Predict reports filled slots under `data.defaults_applied`. Disable via `pulse.Options{DisableDefaults: true}` / `--no-defaults`. Full table: `request-envelope`.

## `attributes` vs `aggregations`

**`attributes`** add a derived COLUMN per row and never collapse groups; **`aggregations`** collapse a group's rows to a SCALAR (or rich payload). To aggregate a derived column, declare the attribute (it runs before `groups`) and aggregate its label — `attribute-composition`.

## Components contract

Every `Response.Components.Aggregations[i]` carries a **universal floor**: `n` (non-null inputs) + `n_null` (null inputs), filled by the orchestrator; floor-only operators (a plain row count) leave the operator map empty. Operator-specific keys ride in `Operator map[string]any`, declared per operator on its `ComponentSchema` (`internal/descriptor/capabilities_aggregators.go`) and mirrored at `manifest.components_schemas.aggregators[<op>].keys`.

Each aggregator declares one class (`components_schemas.aggregators[<op>].mergeability`); it follows from the state the statistic needs:

- **`Mergeable`** — fixed-size running state (sums, counts, extrema, the Welford triple, CI bounds, one value's count). Folds across chunks via the same `MergeOnline` path as the scalar; streaming chunks carry `ComponentsDelta`.
- **`Partial`** — state is a map or set of values (modes, distinct counts / sums, per-member set frequencies); the merge is staged at terminal flush.
- **`None`** — needs every value at once (order statistics: median, percentiles); components emit only on terminal buffered flush and predict surfaces `BufferedComponents=true`.

The same floor surfaces different `Operator` payloads — a sum carries `{sum}`; the Welford aggregator `{mean, m2, variance, stddev}`, whose `(mean, variance, n)` is byte-equal to the Welch t-test's numerators; the modal count `{distinct_count, mode_value, mode_count}`, filled at terminal flush.

`Response.Components.Filterers[i]` carries a uniform `{n_in, n_out, n_null_input}` floor across every filterer; no per-operator slot today.

Full Components contract: `response-components`.

## Type admissibility

Numeric-only ops on a categorical field surface `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`; count / frequency / mode accept any type. Decimal inputs may fall back to `f64` with `PULSE_PRECISION_LOSS` — see `financial-cohorts`.

## Gotchas

- Defaults never cross categories — a categorical `field` with `type` omitted gets the modal count, never a sum.
- Filterers chain in declared order; reordering changes per-stage counters but not the final row set.
- Filterers can't see attribute output (`filterers` runs before `attributes`). To filter a derived column, restate it as an expression filter over source fields, or stage via Compose / ProcessChain.
- Forced-buffered ops (order statistics, decimal paths) materialize the union of shards on a shard archive — memory scales with shard count.
- Tiny groups produce unstable stats. Pair non-trivial aggregations with a row count; higher moments need `n ≥ 2`.

## See

- Recipes: `pulse_examples_search tags=["cohort-analysis"]`, `tags=["welford-triple"]`, `tags=["welch"]`, `tags=["pre-filter"]` plus atomic `op-agg-<name>` / `op-filter-<name>`.
- `request-envelope` — slot keys, smart defaults, streamability.
- `response-components` — Components contract + per-family typed shape.
- `attribute-composition` — when to derive a column instead of aggregating.
- `grouper-design`, `financial-cohorts`, `cohort-schema-design` — partitions, decimal rules, `set_*` + sharded buffered-op memory.
