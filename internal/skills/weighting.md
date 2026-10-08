---
name: weighting
description: Row weights on a Request — the request / per-slot weight and the instance default, probability vs frequency kinds, resolution order, invalid-weight exclusion, which operators honour, skip or refuse a weight, weighted inference on the effective sample size, the unweighted-base recipe and the weighted floor keys.
type: guide
kind: design
applies_to: process, compose, predict, inspect
covers: [Request, WeightSpec, SlotWeight, weight, sum_weights, n_eff, n_weight_invalid]
requires: [capability:weighting]
---
# Row weighting

Survey and sampled cohorts carry a per-row weight column. Name it once and every weight-aware figure counts each row as `w` rows. A request naming no weight is byte-identical to before.

## Surface

| Where | Shape | Notes |
|---|---|---|
| Request `weight` | `{"field": "wt", "kind": "probability"}` | `kind` ∈ `probability` (default) \| `frequency` |
| per-slot `weight` | `"wt"`, `{field, kind}` or `null` | aggregations<!-- feature: capability:crosstab --> (crosstab `cell`, `margin_aggregations[i]`)<!-- /feature -->, `tests`, `post_tests`, regressions, attributes, overlays, `groups`<!-- feature: capability:crosstab -->, crosstab axes<!-- /feature -->. Windows have none |
| `pulse.Options.DefaultWeight` | `*types.WeightSpec` | instance default; never reaches facets |

**`null` ≠ absent.** Absent inherits; `"weight": null` opts that ONE slot out. Compose and chain have no top-level weight. The field must be `u4`…`u64`, `f32` or `f64`; else `PROCESSING_CONFIG` (unknown name `SERVICE_VALIDATION`).

## Kinds

- `probability` (default) — design weights. Means, shares, ratios and quantiles are scale-free; totals scale. Adds `n_eff`.
- `frequency` — a row stands for `w` identical rows; integer weights reproduce the duplicated cohort exactly. A fractional weight is invalid.

## Resolution

Per slot: its own `weight` (`null` ⇒ opted out) → request `weight` → `Options.DefaultWeight` → none. `pulse_predict` lists every slot under `data.weights[]` `{slot, operator, field, kind, status, source}` (`status` ∈ `applied | skipped_not_weight_aware | opted_out | none`). Predict refuses exactly what the run refuses — predict first.

## Invalid weights

Never coerced. Zero is valid and contributes nothing. Null, negative, NaN / ±Inf and (`frequency`) non-integer weights drop the row from that slot (`n_weight_invalid`), with one `PULSE_WEIGHT_INVALID_ROWS` warning per weight column (`details.by_reason`); `--strict` makes it an error.

## What honours a weight

The manifest is the table: an entry's `weight_kinds` lists the kinds it computes a weighted figure under (`weight_aware: true` beside it).

| Class | Explicit weight | Only `Options.DefaultWeight` |
|---|---|---|
| `weight_kinds` both kinds | weighted | weighted |
| `weight_kinds: ["frequency"]` | `frequency` weighted; `probability` ⇒ `PULSE_WEIGHT_UNSUPPORTED` | same |
| min / max / distinct-style aggregators, min-max rescale | `PROCESSING_CONFIG` | skipped |
| windows | request weight `PROCESSING_CONFIG` | skipped |
| filters, features, row-local attributes, most groupers | skipped | skipped |
| inferential, no `weight_kinds` | `PULSE_WEIGHT_UNSUPPORTED` | same |

`PULSE_WEIGHT_UNSUPPORTED` details: `reason` (permanent: post-tests, regression resample / selection, a few tests), `alternative` (a weighted twin), `kind` + `supported_kinds` (frequency-only). Opt out with `"weight": null` — under an instance default, on every refused slot. A weight-aware aggregator over `decimal128` refuses too. Extensions declaring `weight_aware` run under both kinds; others are `PULSE_EXTENSION_NOT_WEIGHT_AWARE`.

Quantiles (and quantile buckets) follow Hmisc `wtd.quantile`; probability weights are rescaled so Σw = n. Share and index overlays read the weighted host payload, no setting.<!-- feature: AGG_WEIGHTED_MEAN, AGG_AVERAGE --> `AGG_WEIGHTED_MEAN` is weighted `AGG_AVERAGE`; its `params.weight_field` is slot-weight sugar.<!-- /feature -->

## Weighted inference

Tests, regressions, CI bounds and inferential overlays use ONE rule: the unweighted formula with sample size N* = Σw (`frequency`) or Kish n_eff = (Σw)²/Σw² (`probability`). Point estimates (means, r, β, proportions) match across kinds; SEs, df (may be fractional) and p-values read N*.

- Results keep raw `n` / `n_obs` and add `sum_weights` + `n_eff` (test `details`, regression results, overlay `summary.parameters`). Read those as the sample size, never `n`.
- Probability n_eff below the test's minimum (regressions: predictors + 1) warns `PULSE_WEIGHT_LOW_NEFF` (strict: error); the p-value is fragile.
- Frequency-only operators (rank, exact and distribution tests) equal the duplicated rows; a probability weight is refused naming the kind.
- Unequal weighting only (weights assumed unrelated to the outcome): no strata, clusters or FPC. Probability χ² is a first-order Kish approximation, not Rao-Scott.
<!-- feature: capability:crosstab -->
- Overlays read each host cell's N* from its floor keys, even when only the cell's own weight set it. There `n_source` raw-count and distinct-key modes are `PROCESSING_CONFIG` — omit it. A probability host with components disabled refuses the χ² and proportion overlays.
<!-- /feature -->

<!-- feature: capability:crosstab -->
## Crosstab and the unweighted base

Cells, margins and normalizations recompute from the WEIGHTED rows; `CellCounts` and margin counts stay raw. Show the unweighted base with an auxiliary that opts out:

```json
{"weight": {"field": "wt"},
 "crosstab": {"rows": [...], "columns": [...],
   "cell": {"type": "<weighted metric>", "field": "spend", "label": "wspend"},
   "margins": {"rows": true, "columns": true, "grand": true},
   "margin_aggregations": [{"type": "<count>", "field": "id", "label": "base", "weight": null}]}}
```

A record reaches an auxiliary margin only if it reached a CELL.
<!-- /feature -->

## Floor keys

A weighted slot's components add `sum_weights`, `n_eff` (`probability` only) and `n_weight_invalid`; absence of `sum_weights` is the ONLY "unweighted" signal. `n`, `n_null`, counts and grouper `total_n` stay raw. A figure undefined under the weights is `null` on the wire.

<!-- feature: io_format:spss -->
## SPSS suggestion

`pulse_inspect` / `pulse_predict` report `suggested_weight {field, source: "spss_sidecar", kind: "probability"}` from `WEIGHT BY`. Never applied — name it yourself; `WEIGHT BY` replicates cases, so `kind: "frequency"` is usually faithful.
<!-- /feature -->

## See

`response-components` (floor) · `request-envelope` · `aggregation-design` · `statistical-testing` · `regression-modeling` · `overlay-system`<!-- feature: capability:crosstab --> · `pairwise-n-sources` · `crosstab-margin-aggregations`<!-- /feature --> · `pulse_errors_lookup` for `PULSE_WEIGHT_*`.
