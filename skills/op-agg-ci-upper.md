---
name: op-agg-ci-upper
description: Upper bound of the confidence interval for the mean.
kind: operator
category: AGG
operator: AGG_CI_UPPER
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `confidence` | float | 0.95 | Confidence level in (0, 1). |
| `method` | string | `"normal"` | `"normal"` (Welford-streamable) today; `"bootstrap"` reserved. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `nullable_*` |

## Output

Scalar `float64` — upper CI bound. NaN when `n < 2`.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `stderr` | float64 | Standard error of the mean |
| `alpha` | float64 | `1 - confidence` |
| `t_critical` | float64 | Scaled critical value |
| `upper` | float64 | Resolved upper bound |

- Mergeability: `Mergeable`
- Streaming: per-chunk Welford

## Gotchas

- `n < 2` → NaN.
- `"bootstrap"` method returns `PROCESSING_CONFIG` until the buffered follow-up lands.
- Pair with `AGG_CI_LOWER` for the full interval.

## See

- `pulse_examples_search tags=[hypothesis-test]`
- Skills: `aggregation-design`, `statistical-testing`, `response-components`
