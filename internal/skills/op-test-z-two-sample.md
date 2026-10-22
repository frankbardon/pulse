---
name: op-test-z-two-sample
description: Two-sample z-test on Field means across two SplitBy groups; identical SE to Welch's t-test but p-value via standard normal Φ.
kind: operator
category: TEST
operator: TEST_Z_TWO_SAMPLE
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, two-sample, z, proportion-analysis, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `reference` | string | — | The `SplitBy` level read second, so `diff` and the effect size read other − reference (default: alphabetical). Not a level → `PULSE_TEST_REFERENCE_INVALID`; unobserved → `PULSE_TEST_REFERENCE_MISSING`. Detail: `test-variant-options`. |
| `covariate` | string | — | A numeric pre-period column (CUPED): the test runs on the adjusted `Field − θ(x − x̄)`; `Details` add `diff_unadjusted`, `theta`, `variance_reduction`. Needs `SplitBy`; else `PULSE_TEST_COVARIATE_INVALID`. Detail: `test-variant-options`. |

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, exactly 2 groups).
- `weight` — per-group moments on w*, SE reads N*_g (Σw frequency, Kish n_eff probability). `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`Field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = z (= Welch t numerator over the same pooled SE); `PValue` two-sided via standard normal Φ; no `DF`. `Details.per_group` = `{n, mean, variance}` per arm.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Streamable — reads the same per-group Welford buckets as the t-tests.
- Statistic + SE byte-equal to Welch's t; **p-value differs** (Φ vs Student-t). For small n the divergence is non-trivial; predict surfaces no warning — choose intentionally.
- Use only when n is large per group AND survey conventions demand normal-CDF p.

## See

- `pulse_examples_search tags=[z]`
- Skills: `statistical-testing`, `op-test-welch`, `op-test-prop-z`, `overlay-system`
