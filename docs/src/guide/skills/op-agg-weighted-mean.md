```yaml
name: op-agg-weighted-mean
description: Weighted arithmetic mean — sum(field * weight) / sum(weight).
kind: operator
category: AGG
operator: AGG_WEIGHTED_MEAN
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, comparison]
```

## Use when

Average of a numeric field where each row counts in proportion to a weight field, such as a survey weight.

Questions it answers:

- What is the weighted average satisfaction per region?
- What is the average price per unit when each order counts by its quantity?

Use something else:

- `AGG_AVERAGE` when every row should count equally.
- `AGG_RATIO` when you want one total divided by another.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `weight_field` | string | (none) | Per-row weight field; sugar for a slot `weight` of kind `probability` |

The weighted mean of the core family: same figure and engine, own type name and components. The weight is `weight_field`, else the slot / request `weight`, else `Options.DefaultWeight`. A `weight_field` that differs from the slot `weight` (or a `"weight": null`), or no weight resolving at all, is `PROCESSING_CONFIG`. Invalid weights (null, negative, NaN/Inf) are excluded and warned (`PULSE_WEIGHT_INVALID_ROWS`).

## Inputs

`Field` — admitted types: see `## Contract`.

- `weight_field` — numeric, same family

## Output

Scalar `float64` — exact `Σ field·w / Σw` (a few ULP from releases that used the running mean).

## Reading the output

- `value`: The average of the field with each row counted in proportion to its weight: sum(value * weight) / sum(weight), in the field's own units. With equal weights it equals AGG_AVERAGE.
  - Caveat: Rows missing the value or the weight, or with weight 0, are left out of the average yet still count in components n; read sum_weights for the base.
  - Caveat: It reads null when no row has a usable weight (the weights sum to 0), never 0.

## Components

All float64. With Σw = 0, `weighted_mean` and the scalar are `null` and the rest 0.

- `sum_weighted` — Σ field·w
- `sum_weights` — Σw
- `weighted_mean` — Σ field·w / Σw
- `m2_weighted` — Σw(x − mean)²
- `sum_weights_sq` — Σw²
- `weighted_variance` — m2/(Σw−1); `null` if Σw ≤ 1
- `n_eff` — Kish (Σw)²/Σw²
- `n_weight_invalid` — rows excluded for an invalid weight.

## Contract

- Types (`field`): `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `sum_weighted`, `sum_weights`, `weighted_mean`, `m2_weighted`, `sum_weights_sq`, `weighted_variance`, `n_eff`; optional `n_weight_invalid`.

## Gotchas

- Invalid/zero-weight rows skip the mean but STILL count in floor `n` (`n`/`n_null` track `Field`); size from `sum_weights`/`n_eff`.
- Unknown `weight_field` → `SERVICE_VALIDATION` (predict + runtime).
- Scalar and every Σ key, `weighted_mean`, `n_eff`: binned sums, the same bits on every worker count and shard split; `m2_weighted` / `weighted_variance` are Welford — within ULP.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-overlay-pairwise-weighted-two-means-z`](op-overlay-pairwise-weighted-two-means-z.md)
