---
name: op-test-brown-forsythe
description: Homogeneity-of-variance test; one-way ANOVA on absolute deviations from per-group medians.
kind: operator
category: TEST
operator: TEST_BROWN_FORSYTHE
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, homogeneity-test, parametric, k-sample, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, ≥ 2 groups).

## Inputs

`Field` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = F (ANOVA on absolute deviations from per-group medians); `DF` = (k − 1, N − k); `PValue` via F-distribution survival. `Details.per_group` = `{n, median, mean_abs_dev}`.

## Gotchas

- Buffered — per-group medians require a sort.
- Pre-ANOVA gate: rejection means equal-variance assumption fails — switch from `TEST_ANOVA_F` to `TEST_ANOVA_WELCH`.
- More robust than Levene (mean-based) under non-normality — that's the whole point.
- Tier-2 variant `TEST_BROWN_FORSYTHE/median_post` runs over result columns.
- Tiny groups (`n_i < 3`) destabilize the median; gate with `PULSE_TEST_INSUFFICIENT_N`.

## See

- `pulse_examples_search tags=[homogeneity-test]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-anova-welch`
