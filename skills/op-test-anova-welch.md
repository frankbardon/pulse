---
name: op-test-anova-welch
description: Heteroscedasticity-robust one-way ANOVA with Welch-Satterthwaite df correction; streamable per-group Welford.
kind: operator
category: TEST
operator: TEST_ANOVA_WELCH
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, k-sample, comparison, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`. Significance level in `(0, 1)`.

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, ≥ 2 groups).

## Inputs

`Field` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = F (Welch-weighted); `DF` numerator = k − 1; `Details.df_denominator` = Welch-Satterthwaite df; `PValue` via F-distribution survival. `Details.per_group` = `{n, mean, variance, weight}`; effect size ω².

## Gotchas

- Streamable — same per-group Welford as `TEST_ANOVA_F`; only the statistic + denominator change.
- Use when `TEST_BROWN_FORSYTHE` rejects equal-variance.
- Tier-2 variant `TEST_ANOVA_WELCH/welch_one_way_post` consumes upstream per-group `{mean, variance, n}`.
- Post-hoc: Tukey HSD assumes equal variance; for unequal fall back to pairwise `TEST_WELCH` + Bonferroni.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-brown-forsythe`, `op-test-tukey-hsd`
