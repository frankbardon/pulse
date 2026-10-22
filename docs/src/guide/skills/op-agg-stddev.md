```yaml
name: op-agg-stddev
description: Population standard deviation via Welford's online algorithm.
kind: operator
category: AGG
operator: AGG_STDDEV
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
```

## Use when

Typical distance of a numeric field's values from their average, in the field's own units, over all rows or per group.

Questions it answers:

- How much do delivery times vary around the average in each region?
- Which product line's order values vary the most in absolute terms (divide by each line's average to compare relative consistency)?

## Params

Weighted: √(m2_w / Σw).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — population stddev (n-denominator); `decimal128` input yields a decimal-scaled result. Per-group when wired under a grouper.

## Reading the output

- `value`: The typical distance of a value from the group's mean, in the field's own units. 0 means every value is the same; for a roughly bell-shaped field about two thirds of the values sit within one standard deviation of the mean.
  - Caveat: It is the spread of the rows, not the precision of the mean: for how precisely the average is known, read AGG_CI_LOWER / AGG_CI_UPPER.
  - Caveat: Each distance is squared before averaging, so a few extreme values inflate it; for a skewed field a percentile range describes the spread better.

## Components

- `mean` — Welford running mean
- `m2` — Sum of squared deviations
- `variance` — `m2 / n`
- `stddev` — √variance

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `mean`, `m2`, `variance`, `stddev`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Population stddev (`n` denominator), not sample (`n-1`).
- `decimal128` is supported, but not by Welford: a decimal two-pass (mean, then Σ(x−μ)², then decimal `Sqrt`). An overflowing intermediate drops the WHOLE aggregate to an f64 pass and warns `PULSE_DECIMAL_PRECISION_LOSS`. The decimal claim therefore rests partly on an f64 fallback.
- Single-row group → 0; no value (or Σw = 0) → `null` (NaN), the components' `mean` / `variance` too.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
