---
name: formula-totals
description: total(expr) inside the formula attribute — a cohort-wide post-filter sum on every row, how weights enter it, the extra pass it costs and how shards and workers run it, where it is refused, and the pro-rata and subset-total recipes. Topical design; the attribute's params in its atomic skill.
type: guide
kind: design
applies_to: process, compose, predict
covers: [total, total(expr), pro-rata, subset total, share of total, two-pass, PULSE_FORMULA_TOTAL_SITE, PULSE_ADVISORY_EXTRA_PASS]
requires: [ATTR_FORMULA]
---

# `total(expr)` in a formula

`total(expr)` is the cohort-wide sum of a row-level `expr` over the post-filter rows: the same figure on every row, also when grouped. It exists in the formula attribute ONLY — lowercase, one argument, no nesting. Expression basics: `expression-language`; composing attributes: `attribute-composition`.

## Semantics

- Rows whose argument is null are skipped.
- An empty total is `null`; dividing by a zero total gives `null`.
- A binned, order-free sum: the same bits on every execution arm and shard split.

<!-- feature: capability:weighting -->
## Weights

A `frequency` weight in force on the slot weights it: Σw·expr, the expanded-row total; invalid or zero-weight rows add nothing. A `probability` weight is `PULSE_WEIGHT_UNSUPPORTED` at predict and at runtime alike — the total would scale with the weights; opt the slot out with `"weight": null`. A formula without `total()` reads no weight (skipped). See `weighting`.
<!-- /feature -->

## Recipes

| Want | Formula |
|---|---|
| Pro-rata to 1000 | `x * 1000 / total(x)` |
| Share of the total | `x / total(x)` |
| Scale only a subset's total to 1000 | `x * (1 - 1000 / total(week_index >= 52 ? x : 0))` |
| Count of filtered rows | `total(1)` |

## Cost and execution

A `total` formula is **two-pass**, like the z-score attribute: a pre-pass sums, the main pass scores.

- It streams alone and buffers beside groups or features.
- `ShardWorkers` / `DecodeWorkers` run it two-phase (a parallel pre-pass, same bits) when ungrouped and no `total()` argument reads another total; otherwise sequentially.
- `ProcessChain` refuses it (`PULSE_CHAIN_NOT_MERGEABLE`).
- Predict adds one `PULSE_ADVISORY_EXTRA_PASS` per such formula (`added_scans`; `predict-advisories`).

## Refusals

- `total()` in an expression filter or an overlay formula (Compose host too) → `PULSE_FORMULA_TOTAL_SITE`; an overlay formula reads its `total` variable instead<!-- feature: OVERLAY_FORMULA --> (`overlay-formula-namespace`)<!-- /feature -->.
- Nested → `PULSE_FORMULA_TOTAL_NESTED`; arity ≠ 1 → `PULSE_FORMULA_TOTAL_ARITY`.
- An embedder `ExprFunction` named `total` → `PULSE_EXTENSION_NAME_RESERVED`.

## See

- `expression-language`, `attribute-composition`, `predict-advisories`, `overlay-formula-namespace`<!-- feature: capability:weighting -->, `weighting`<!-- /feature -->
