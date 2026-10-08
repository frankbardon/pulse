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

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), kind `frequency` only (a probability weight is `PULSE_WEIGHT_UNSUPPORTED` naming the kind): weighted group medians, ANOVA on Σw — equals the expanded rows. `Details` add `sum_weights` beside `n` (raw rows).

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, ≥ 2 groups).

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

`Field` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = F (ANOVA on absolute deviations from per-group medians); `DF` = k-1 (`df_within` = N-k in Details); `PValue` via F-distribution survival. Flat `Details`: `groups`, `n`, `group_medians`, `abs_dev_means`, `ss_between`, `ss_within`.

<!-- generated: reading-the-output -->

## Gotchas

- Buffered — per-group medians require a sort.
- Not a pre-ANOVA gate: a large p is not evidence of equal spread.
- More robust than Levene (mean-based) under non-normality — that's the whole point.
- Tier-2 variant `TEST_BROWN_FORSYTHE/median_post` runs over result columns.
- Tiny groups destabilize the median; `n_i < 2` or N ≤ k → `PULSE_TEST_INSUFFICIENT_N`.

## See

- `pulse_examples_search tags=[homogeneity-test]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-anova-welch`
