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

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `reference` — string: the `SplitBy` level read second, so `diff`, CI, z and `cohens_d` read other − reference (default: alphabetical, second level). Not a level, or no `SplitBy` → `PULSE_TEST_REFERENCE_INVALID` (predict too); neither observed group → `PULSE_TEST_REFERENCE_MISSING` (grouped: that group's figures `null`).
- `covariate` — string, a numeric pre-period column (CUPED), one pass: θ = slope of `Field` on it pooled over both groups, and `diff`, CI, z, p, `mean` / `variance` and `cohens_d` read the adjusted `Field − θ(x − x̄)` (equal to the two-step: θ first, then the test on the adjusted column). `Details` add `diff_unadjusted`, `theta`, `covariate_mean`, `variance_reduction` (1 − SE²adjusted / SE²raw; with `theta` `null` when x is constant) and `n_covariate_dropped` (null-covariate rows, dropped listwise). Composes with `reference`; weighted (both kinds) it is the weighted two-step (θ, x̄ on the weights; each group's N*); grouped, θ is per group. On any other test, without `SplitBy`, non-numeric or `= Field` → `PULSE_TEST_COVARIATE_INVALID` (predict too).

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
