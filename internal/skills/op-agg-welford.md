---
name: op-agg-welford
description: Streaming Welford-Pébaÿ moment triple — running mean, sample variance (n-1), and observed count.
kind: operator
category: AGG
operator: AGG_WELFORD
type: reference
applies_to: process, compose, predict
examples_tags: [welford-triple, distribution-shape, buffered-pipeline]
---

<!-- generated: use-when -->

## Params

Weight: honours a resolved row weight (`weight` on the request or slot, or `Options.DefaultWeight`; `"weight": null` opts out) — weighted the variance is the sample m2_w / (Σw − 1); `N` stays the contributing row count. Invalid weights (null, negative, NaN/Inf, fractional under `frequency`) are excluded and warned (`PULSE_WEIGHT_INVALID_ROWS`); zero contributes nothing.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | strict scalar numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64` |

`decimal128`, `date`, and bit-packed types rejected.

## Output

Scalar `float64` — running mean (NaN no rows; `null` in JSON). Rich: `WelfordTriple{Mean, Variance, N}` via `RichAggregator`.

<!-- generated: reading-the-output -->

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
- Skills: `aggregation-design`, `overlay-system`, `response-components`
