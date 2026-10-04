---
name: op-agg-skewness
description: Population skewness g1 (m3 / m2^1.5, dividing by n) via online moments.
kind: operator
category: AGG
operator: AGG_SKEWNESS
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
---

## Params

Weight-aware (`"weight": null` opts out): weighted population moments, Σw in place of n. Invalid weights excluded (`PULSE_WEIGHT_INVALID_ROWS`).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `u4` |

## Output

Scalar `float64` — population skewness g1 = `m3 / m2^1.5` (moments divide by `n`), NOT the adjusted G1 of Excel `SKEW` / SPSS. Per-group when wired under a grouper.

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
- `decimal128` rejected<!-- feature: ATTR_FORMULA --> — cast via `ATTR_FORMULA`<!-- /feature -->.
- Sensitive to outliers; pre-filter or use rank-based alternatives.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
