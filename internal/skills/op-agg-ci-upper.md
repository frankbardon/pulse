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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `confidence` | float | 0.95 | Confidence level in (0, 1). |
| `method` | string | `"normal"` | `"normal"` (Welford-streamable) today; `"bootstrap"` reserved. |

Bound = mean ∓ z·√(s²/n), z = `qnorm(1 − α/2)`. Weight (both kinds; `"weight": null` opts out): weighted mean, s² on w* = w·N*/Σw, stderr √(s²/N*), N* = Σw (`frequency`) or Kish n_eff (`probability`); same z. Invalid weights excluded and warned.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `u4` |

## Output

Scalar `float64` — upper CI bound. NaN when `n < 2` (weighted: N* ≤ 1; `null` in JSON).

<!-- generated: reading-the-output -->

## Components

<!-- feature: capability:weighting -->Weighted: floor adds `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.<!-- /feature -->

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `stderr` | float64 | Standard error of the mean |
| `alpha` | float64 | `1 - confidence` |
| `t_critical` | float64 | Normal critical z (`qnorm(1 − α/2)`) |
| `upper` | float64 | Resolved upper bound |

- Mergeability: `Mergeable`
- Streaming: per-chunk Welford

## Gotchas

- Normal z, not t: small samples get narrower intervals than t.
- `n < 2` (weighted N* ≤ 1) → NaN.
- `"bootstrap"` method returns `PROCESSING_CONFIG` until the buffered follow-up lands.

## See

- `pulse_examples_search tags=[hypothesis-test]`
- Skills: `aggregation-design`, `statistical-testing`, `response-components`
