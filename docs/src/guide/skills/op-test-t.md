```yaml
name: op-test-t
description: One-sample or two-sample Welch t-test on a numeric field; SplitBy switches to the two-sample variant.
kind: operator
category: TEST
operator: TEST_T
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, t-test, tier-1-test, parametric, two-sample, one-sample, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether a numeric field's average differs from a target value, or between two groups (Welch's version).

Questions it answers:

- Is the average order value different from our target of 50?
- Do new and returning customers spend different amounts on average?

Use something else:

- `TEST_PAIRED_T` when the same subjects are measured twice, such as before and after.
- `TEST_ANOVA_WELCH` when there are three or more groups.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `mu` — float, default `0.0`. Hypothesized mean, one-sample only; ignored when `SplitBy` is set.
- `reference` — string, two-sample: the `SplitBy` level read second, so `diff`, CI, t and `cohens_d` read other − reference (default: alphabetical, second level). Not a level, or no `SplitBy` → `PULSE_TEST_REFERENCE_INVALID` (predict too); neither observed group → `PULSE_TEST_REFERENCE_MISSING` (grouped: that group's figures `null`).
- `covariate` — string, two-sample: a numeric pre-period column (CUPED), one pass: θ = slope of `Field` on it pooled over both groups, and `diff`, CI, t, p, `mean` / `variance` and `cohens_d` read the adjusted `Field − θ(x − x̄)` (equal to the two-step: θ first, then the test on the adjusted column). `Details` add `diff_unadjusted`, `theta`, `covariate_mean`, `variance_reduction` (1 − SE²adjusted / SE²raw; with `theta` `null` when x is constant) and `n_covariate_dropped` (null-covariate rows, dropped listwise). Composes with `reference`; weighted (both kinds) it is the weighted two-step (θ, x̄ on the weights; each group's N*); grouped, θ is per group. On any other test, without `SplitBy`, non-numeric or `= Field` → `PULSE_TEST_COVARIATE_INVALID` (predict too).
- `weight` — moments on w*, N* = Σw (frequency) or Kish n_eff (probability); `df` = N*−1 (two-sample: Welch df on N*_g), may be fractional. `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (optional → two-sample Welch) — `categorical_u8`/`u16`/`u32` or `packed_bool` (levels `"0"`/`"1"`).

## Output

`Statistic` = t; `DF`; `PValue` two-sided. One-sample `Details`: `mu`, `n`, `mean`, `variance`, `ci_low`/`ci_high`, `effect_size.cohens_d` = (mean − mu)/sd. Two-sample: per-group `n`/`mean`/`variance`, `diff`, CI, pooled-SD `cohens_d`.

## Reading the output

- `statistic`: t is the gap between averages measured in standard errors: the sample mean minus the target value (one-sample), or the first group's mean minus the second's (two-sample, Welch). Values far from 0 in either direction are unlikely if the true gap is zero.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.diff`: Two-sample only: the first group's mean minus the second's, in the field's own units.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- Two-sample needs exactly 2 `SplitBy` groups, else `PULSE_TEST_SPLIT_GROUPS_LT_2`.
- Streamable — running Welford state per `SplitBy` group.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.
- n < 2 (per group) → `PULSE_TEST_INSUFFICIENT_N`.
- With `groups`: one result per group, each with `group_key`; `split_by` a grouper → `PULSE_TEST_SPLIT_GROUPS_LT_2`; a too-sparse group → `null` figures + a warning ([`statistical-testing`](statistical-testing.md)).

## See

- `pulse_examples_search tags=[t-test]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-welch`](op-test-welch.md), [`op-test-z-two-sample`](op-test-z-two-sample.md), [`op-test-paired-t`](op-test-paired-t.md)
