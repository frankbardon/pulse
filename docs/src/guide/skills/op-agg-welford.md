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
  - Caveat: variance is 0 when n is below 2: no spread could be estimated, which is not the same as values that agree.
  - Caveat: variance here divides by n - 1, so on the same rows it is larger than AGG_VARIANCE (which divides by n).

## Components

Weighted slots add floor keys `sum_weights` (Σw), `n_eff` (Kish; `probability` only) and `n_weight_invalid`; absent ⇒ unweighted. `n`/`n_null` stay raw counts.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `m2` | float64 | Sum of squared deviations |
| `variance` | float64 | Unbiased sample variance (`m2/(n-1)`) |
| `stddev` | float64 | Sample standard deviation |

- Mergeability: `Mergeable` (Chan-Welford combine); margin recompute
- Streaming: per-chunk; rich payload exposed at terminal flush

## Gotchas

- Sample variance (`n-1`), not population (`n`).
- The rich triple is what cell-level t / z overlays read, via `Components.Crosstab.CellComponents`.
- Margin reducibility = recompute, not pool by addition.

## See

- `pulse_examples_search tags=[welford-triple]`
- Skills: [`aggregation-design`](aggregation-design.md), [`overlay-system`](overlay-system.md), [`response-components`](response-components.md)
