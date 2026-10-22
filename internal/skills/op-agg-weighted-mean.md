---
name: op-agg-weighted-mean
description: Weighted arithmetic mean — sum(field * weight) / sum(weight).
kind: operator
category: AGG
operator: AGG_WEIGHTED_MEAN
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, comparison]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `weight_field` | string | — | Per-row weight field; sugar for a slot `weight` of kind `probability`. |

The weighted mean of the core family: same figure and engine, own type name and components. The weight is `weight_field`, else the slot / request `weight`, else `Options.DefaultWeight`. A `weight_field` that differs from the slot `weight` (or a `"weight": null`), or no weight resolving at all, is `PROCESSING_CONFIG`. Invalid weights (null, negative, NaN/Inf) are excluded and warned (`PULSE_WEIGHT_INVALID_ROWS`).

## Inputs

`Field` — admitted types: see `## Contract`.

- `weight_field` — numeric, same family

## Output

Scalar `float64` — exact `Σ field·w / Σw` (a few ULP from releases that used the running mean).

<!-- generated: reading-the-output -->

## Components

All float64. With Σw = 0, `weighted_mean` and the scalar are `null` and the rest 0.

- `sum_weighted` — Σ field·w
- `sum_weights` — Σw
- `weighted_mean` — Σ field·w / Σw
- `m2_weighted` — Σw(x − mean)²
- `sum_weights_sq` — Σw²
- `weighted_variance` — m2/(Σw−1); `null` if Σw ≤ 1
- `n_eff` — Kish (Σw)²/Σw²
<!-- feature: capability:weighting -->
- `n_weight_invalid` — rows excluded for an invalid weight.
<!-- /feature -->

<!-- generated: contract -->

## Gotchas

- Invalid/zero-weight rows skip the mean but STILL count in floor `n` (`n`/`n_null` track `Field`); size from `sum_weights`/`n_eff`.
- Unknown `weight_field` → `SERVICE_VALIDATION` (predict + runtime).
- Scalar and every Σ key, `weighted_mean`, `n_eff`: binned sums, the same bits on every worker count and shard split; `m2_weighted` / `weighted_variance` are Welford — within ULP.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`, `op-overlay-pairwise-weighted-two-means-z`
