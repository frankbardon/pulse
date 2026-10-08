```yaml
name: op-test-z-two-sample
description: Two-sample z-test on Field means across two SplitBy groups; identical SE to Welch's t-test but p-value via standard normal Φ.
kind: operator
category: TEST
operator: TEST_Z_TWO_SAMPLE
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, two-sample, z, proportion-analysis, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Large-sample check of whether two groups differ in average, with the p-value read from the normal distribution.

Questions it answers:

- In a large survey, do the two regions differ in average score?
- Across thousands of sessions, is average basket size different between the two app versions?

Use something else:

- `TEST_WELCH` when either group is small; the t-distribution p-value is more honest there.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, exactly 2 groups).
- `weight` — slot weight (`null` opts out), both kinds: per-group moments on w*, SE reads N*_g (Σw frequency, Kish n_eff probability). `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = z (= Welch t numerator over the same pooled SE); `PValue` two-sided via standard normal Φ; no `DF`. `Details.per_group` = `{n, mean, variance}` per arm.

## Reading the output

- `statistic`: z: the first group's mean minus the second's, in standard errors built from each group's sample variance, read against the normal distribution. Values far from 0 in either direction are unlikely if the true gap is zero.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.diff`: The first group's mean minus the second's, in the field's own units.

## Gotchas

- Streamable — reads the same per-group Welford buckets as the t-tests.
- Statistic + SE byte-equal to Welch's t; **p-value differs** (Φ vs Student-t). For small n the divergence is non-trivial; predict surfaces no warning — choose intentionally.
- Use only when n is large per group AND survey conventions demand normal-CDF p.

## See

- `pulse_examples_search tags=[z]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-welch`](op-test-welch.md), [`op-test-prop-z`](op-test-prop-z.md), [`overlay-system`](overlay-system.md)
