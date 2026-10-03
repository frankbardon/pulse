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

- `alpha` — float, default `0.05`, in `(0, 1)`.

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (required, ≥ 2 groups) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = Welch F*; `DF` = k−1; `PValue` via F survival. `Details`: `groups`, `n`, `group_means`, `group_variances`, `weights`, `weighted_mean`, `df_between`, `df_within` (Welch df). `effect_size.omega_squared` = df_b(F*−1)/(df_b(F*−1)+N) clamped ≥ 0 (Lakens 2013).

## Gotchas

- Streamable — same per-group Welford as classic one-way ANOVA; only the statistic + denominator change.
- Use when group spreads may differ.
- Tier-2 `welch_one_way_post` reads per-group `{mean, variance, n}`; same keys.
- Post-hoc: Tukey HSD assumes equal variance<!-- feature: TEST_WELCH -->; for unequal fall back to pairwise `TEST_WELCH` + Bonferroni<!-- /feature -->.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-brown-forsythe`, `op-test-tukey-hsd`
