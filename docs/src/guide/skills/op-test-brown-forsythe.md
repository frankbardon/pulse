```yaml
name: op-test-brown-forsythe
description: Homogeneity-of-variance test; one-way ANOVA on absolute deviations from per-group medians.
kind: operator
category: TEST
operator: TEST_BROWN_FORSYTHE
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, homogeneity-test, parametric, k-sample, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether the spread of a numeric field differs across groups: a robust test of equal variances.

Questions it answers:

- Is delivery time more variable at some warehouses than at others?
- How different is the spread of the measure across groups?

Use something else:

- `TEST_ANOVA_WELCH` when you want to compare the group averages themselves.
- `TEST_SHAPIRO_WILK` when you want to check whether a measure is bell-shaped.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — frequency only: weighted group medians, ANOVA on Σw — equals the expanded rows, the `n ≥ 2` per-group floor on Σw too (one row of weight 2 runs). `Details` add `sum_weights` beside `n` (raw rows).

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, ≥ 2 groups).


## Inputs

`Field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = F (ANOVA on absolute deviations from per-group medians); `DF` = k-1 (`df_within` = N-k in Details); `PValue` via F-distribution survival. Flat `Details`: `groups`, `n`, `group_medians`, `abs_dev_means`, `ss_between`, `ss_within`.

## Reading the output

- `statistic`: F on each row's distance from its group median: larger values are stronger evidence that the groups differ in spread; the p-value says whether this F is large enough to be surprising.
  - Caveat: It tests spread only, not averages.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Buffered — per-group medians require a sort.
- Not a pre-ANOVA gate: a large p is not evidence of equal spread.
- More robust than Levene (mean-based) under non-normality — that's the whole point.
- Tier-2 variant `TEST_BROWN_FORSYTHE/median_post` runs over result columns. A row with a null in a used column drops listwise (R's NA default); `details.n` counts the rows used.
- Tiny groups destabilize the median; `n_i < 2` or N ≤ k → `PULSE_TEST_INSUFFICIENT_N`.
- Zero within-group spread (each group's `|x − median|` constant, `ss_within` = 0): F = +Inf (`null` on the wire), p = 0 when groups differ; F and p `null` (no rejection) when all deviations are equal — R `anova(lm(...))`'s exact limit.

## See

- `pulse_examples_search tags=[homogeneity-test]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-anova-welch`](op-test-anova-welch.md)
