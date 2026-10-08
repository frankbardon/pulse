```yaml
name: op-agg-skewness
description: Population skewness g1 (m3 / m2^1.5, dividing by n) via online moments.
kind: operator
category: AGG
operator: AGG_SKEWNESS
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
```

## Use when

How lopsided a numeric field is: positive when a long tail runs to high values, negative when it runs to low ones.

Questions it answers:

- Are incomes in each region bunched low with a few very high ones?
- Is the delivery-time distribution symmetric or dragged out by slow orders?

Use something else:

- `AGG_KURTOSIS` when you want to know whether the tails are heavy rather than lopsided.

## Params

Weight-aware (`"weight": null` opts out): weighted population moments, Σw in place of n. Invalid weights excluded (`PULSE_WEIGHT_INVALID_ROWS`).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `u4` |

## Output

Scalar `float64` — population skewness g1 = `m3 / m2^1.5` (moments divide by `n`), NOT the adjusted G1 of Excel `SKEW` / SPSS. Per-group when wired under a grouper.

## Reading the output

- `value`: How lopsided the values are around their mean. Pulse computes the population moment coefficient g1: the average cubed z-score, (m3/n) / (m2/n)^1.5, where the components m2 and m3 are SUMS of squared and cubed distances from the mean (not yet divided by n). 0 means the values balance around the mean.
  - Sign: positive means a longer or heavier tail to the right: a few values sit far above the rest; negative means a longer or heavier tail to the left: a few values sit far below the rest.

## Components

Weighted adds floor `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Running mean |
| `m2` | float64 | Squared-deviation accumulator |
| `m3` | float64 | Cubed-deviation accumulator |
| `skewness` | float64 | Derived from m2, m3, n |

- Mergeability: `Mergeable` (Chan moments combine)
- Streaming: per-chunk online moments

## Gotchas

- `n <= 1` or zero variance → `0` (not NaN): check `n` before reading a 0.
- Small-n bias: G1 = g1·√(n(n−1))/(n−2), so |g1| < |G1| on small groups.
- `decimal128` rejected — cast via `ATTR_FORMULA`.
- Sensitive to outliers; pre-filter or use rank-based alternatives.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
