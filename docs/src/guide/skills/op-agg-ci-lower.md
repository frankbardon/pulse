```yaml
name: op-agg-ci-lower
description: Lower bound of the confidence interval for the mean.
kind: operator
category: AGG
operator: AGG_CI_LOWER
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, streaming-friendly]
```

## Use when

Lower end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down.

Questions it answers:

- How precisely do we know the average satisfaction in each segment?
- What is the lowest average order value the data are consistent with at 95% confidence?

Use something else:

- `AGG_CI_UPPER` when you want the upper end of the same interval.

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

Scalar `float64` — lower CI bound. NaN when `n < 2` (weighted: N* ≤ 1; `null` in JSON).

## Reading the output

- `value`: The lower end of a confidence interval for the group's mean at the requested confidence (95% by default): mean - z * standard error, where the standard error is the sample standard deviation (dividing by n - 1) over sqrt(n).
  - Caveat: The interval is about the MEAN, not about individual rows: most rows can, and usually do, fall outside it.
  - Caveat: The confidence level describes the method: over repeated samples, intervals built this way capture the true mean that share of the time. Any one interval either contains it or not.

## Components

Weighted: floor adds `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `stderr` | float64 | Standard error of the mean |
| `alpha` | float64 | `1 - confidence` |
| `t_critical` | float64 | Normal critical z (`qnorm(1 − α/2)`) |
| `lower` | float64 | Resolved lower bound |

- Mergeability: `Mergeable`
- Streaming: per-chunk Welford

## Gotchas

- Normal z, not t: small samples get narrower intervals than t.
- `n < 2` (weighted N* ≤ 1) → NaN (no variance estimate).
- `"bootstrap"` method returns `PROCESSING_CONFIG` until the buffered follow-up lands.

## See

- `pulse_examples_search tags=[hypothesis-test]`
- Skills: [`aggregation-design`](aggregation-design.md), [`statistical-testing`](statistical-testing.md), [`response-components`](response-components.md)
