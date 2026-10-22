```yaml
name: op-agg-kurtosis
description: Population excess kurtosis g2 (m4 / m2^2 - 3, dividing by n) via online moments.
kind: operator
category: AGG
operator: AGG_KURTOSIS
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
```

## Use when

How heavy a numeric field's tails are next to a normal distribution: positive when extreme values are more common.

Questions it answers:

- Do transaction amounts have more extreme values than a bell curve would give?
- Are response times prone to rare, very long waits?

Use something else:

- `AGG_SKEWNESS` when you want to know whether the field is lopsided.

## Params

Weighted: weighted population moments, Σw in place of n.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — population excess kurtosis g2 = `m4 / m2^2 − 3` (moments divide by `n`), NOT the adjusted G2 of Excel `KURT` / SPSS. Per-group when wired under a grouper.

## Reading the output

- `value`: How heavy the tails are next to a normal distribution. Pulse computes the population EXCESS kurtosis g2: the average fourth-power z-score minus 3, (m4/n) / (m2/n)^2 - 3, where the components m2 and m4 are SUMS of squared and fourth-power distances from the mean (not yet divided by n), so a normal distribution scores 0. It can never fall below -2.
  - Caveat: Small-n bias: g2 differs from the adjusted G2 that Excel KURT and SPSS print, and on small groups the gap is large; neither is stable on a handful of rows.

## Components

- `mean` — Running mean
- `m2` — Second-moment accumulator
- `m3` — Third-moment accumulator
- `m4` — Fourth-moment accumulator
- `kurtosis` — Derived from m2, m4, n

## Contract

- Types (`field`): `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `mean`, `m2`, `m3`, `m4`, `kurtosis`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- `n = 0` (or Σw = 0), `n = 1` or zero variance → `null` (NaN), scalar and component: no shape is visible there, never 0.
- Small-n bias vs adjusted G2 is large on small groups; g2 ≥ −2 always.
- Excess kurtosis (normal = 0), NOT raw kurtosis (normal = 3).

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
