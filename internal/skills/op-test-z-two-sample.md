---
name: op-test-z-two-sample
description: Two-sample z-test on field means across two split_by groups; identical SE to Welch's t-test but p-value via standard normal Φ.
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
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `reference` | string | — | The baseline `split_by` level, by value: `diff` and the effect size read other − reference (default: the alphabetically second level). Not a level → `PULSE_TEST_REFERENCE_INVALID`; unobserved → `PULSE_TEST_REFERENCE_MISSING`. Detail: `test-variant-options`. |
| `covariate` | string | — | A numeric pre-period field (CUPED): the test runs on the adjusted `y − θ(x − x̄)`; `details` add `diff_unadjusted`, `theta`, `variance_reduction`. Needs `split_by`; else `PULSE_TEST_COVARIATE_INVALID`. Detail: `test-variant-options`. |

- `weight` — per-group moments on w*, SE reads N*_g (Σw frequency, Kish n_eff probability). `details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `split_by` (required, exactly 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`statistic` = z = `diff` over the unpooled (Welch) SE √(s²_a/n_a + s²_b/n_b); `p_value` two-sided via standard normal Φ; no `df`. `details`: `groups`, per-arm `n` / `mean` / `variance` (sample, n − 1), `diff`, normal `ci_low`/`ci_high` on `diff`, pooled-SD `effect_size.cohens_d`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Streamable — reads the same per-group Welford buckets as the t-tests.
- `statistic` and SE equal Welch's t; **p-value differs** (Φ vs Student-t). For small n the divergence is non-trivial; predict surfaces no warning — choose intentionally.
- Use only when n is large per group AND survey conventions demand normal-CDF p.

## See

- `pulse_examples_search tags=[z]`
- Skills: `statistical-testing`, `op-test-welch`, `op-test-prop-z`, `overlay-system`
