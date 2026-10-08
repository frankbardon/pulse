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

Weight-aware (`"weight": null` opts out): weighted population moments, Σw in place of n. Invalid weights excluded (`PULSE_WEIGHT_INVALID_ROWS`).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `u4` |

## Output

Scalar `float64` — population excess kurtosis g2 = `m4 / m2^2 − 3` (moments divide by `n`), NOT the adjusted G2 of Excel `KURT` / SPSS. Per-group when wired under a grouper.

## Reading the output

- `value`: How heavy the tails are next to a normal distribution. Pulse computes the population EXCESS kurtosis g2: the average fourth-power z-score minus 3, (m4/n) / (m2/n)^2 - 3, where the components m2 and m4 are SUMS of squared and fourth-power distances from the mean (not yet divided by n), so a normal distribution scores 0. It can never fall below -2.
  - Caveat: Small-n bias: g2 differs from the adjusted G2 that Excel KURT and SPSS print, and on small groups the gap is large; neither is stable on a handful of rows.

## Components

Weighted adds floor `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Running mean |
| `m2` | float64 | Second-moment accumulator |
| `m3` | float64 | Third-moment accumulator |
| `m4` | float64 | Fourth-moment accumulator |
| `kurtosis` | float64 | Derived from m2, m4, n |

- Mergeability: `Mergeable` (Chan moments combine)
- Streaming: per-chunk online moments

## Gotchas

- `n <= 1` or zero variance → `0` (not NaN): check `n` before reading a 0.
- Small-n bias vs adjusted G2 is large on small groups; g2 ≥ −2 always.
- Excess kurtosis (normal = 0), NOT raw kurtosis (normal = 3).
- `decimal128` rejected.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
