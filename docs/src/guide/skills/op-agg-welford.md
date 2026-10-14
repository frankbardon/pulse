```yaml
name: op-agg-welford
description: Streaming Welford-Pébaÿ moment triple — running mean, sample variance (n-1), and observed count.
kind: operator
category: AGG
operator: AGG_WELFORD
type: reference
applies_to: process, compose, predict
examples_tags: [welford-triple, distribution-shape, buffered-pipeline]
```

## Use when

Mean, sample variance and row count of a numeric field in one result: what a comparison of group means needs.

Questions it answers:

- What are the mean, variance and size of each cell, so cells can be tested against each other?
- How do the average and spread of scores compare across segments?

Use something else:

- `AGG_AVERAGE` when you want only the average.
- `TEST_WELCH` when you want the comparison itself on raw rows.

## Params

Weight: honours a resolved row weight (`weight` on the request or slot, or `Options.DefaultWeight`; `"weight": null` opts out) — weighted the variance is the sample m2_w / (Σw − 1); `N` stays the contributing row count. Invalid weights (null, negative, NaN/Inf, fractional under `frequency`) are excluded and warned (`PULSE_WEIGHT_INVALID_ROWS`); zero contributes nothing.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | strict scalar numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64` |

`decimal128`, `date`, and bit-packed types rejected.

## Output

Scalar `float64` — running mean (NaN no rows; `null` in JSON). Rich: `WelfordTriple{Mean, Variance, N}` via `RichAggregator`.

## Reading the output

- `value.*`: Three columns per group: mean (the average), variance (the SAMPLE variance, dividing by n - 1, in the field's units squared) and n (the rows with a value). Together they are what a t or z comparison of group means needs; the components add stddev, the square root of variance.
  - Caveat: variance reads null when n is below 2 (weighted: when the weights sum to 1 or less): no spread could be estimated. A 0 means values that agree.
  - Caveat: variance here divides by n - 1, so on the same rows it is larger than AGG_VARIANCE (which divides by n).

## Components

Weighted slots add floor keys `sum_weights` (Σw), `n_eff` (Kish; `probability` only) and `n_weight_invalid`; absent ⇒ unweighted. `n`/`n_null` stay raw counts.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `m2` | float64 | Sum of squared deviations |
| `variance` | float64 | Unbiased sample variance (`m2/(n-1)`); `null` when `n < 2` (weighted Σw ≤ 1) |
| `stddev` | float64 | Sample standard deviation |

- Mergeability: `Mergeable` (Chan-Welford combine); margin recompute
- Streaming: per-chunk; rich payload exposed at terminal flush

## Gotchas

- Sample variance (`n-1`), not population (`n`); undefined (`null`, triple and components) below 2 values (Σw ≤ 1), never 0 — a pairwise / t / z overlay skips that leg.
- The rich triple is what cell-level t / z overlays read, via `Components.Crosstab.CellComponents`.
- Margin reducibility = recompute, not pool by addition.
- Non-scalar row value: an overlay scoring it (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]`) is refused (`PROCESSING_CONFIG`, reason `non_scalar`) — except the two-sample t / z vs-ref overlays, which read the triple from the slot's per-group components (disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`); an unscored one beside a scalar runs.

## See

- `pulse_examples_search tags=[welford-triple]`
- Skills: [`aggregation-design`](aggregation-design.md), [`overlay-system`](overlay-system.md), [`response-components`](response-components.md)
