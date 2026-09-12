---
name: op-test-kruskal-wallis
description: Nonparametric k-group location test; ANOVA-like rank sums under tie correction.
kind: operator
category: TEST
operator: TEST_KRUSKAL_WALLIS
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, k-sample, comparison, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (required, ≥ 2 groups) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = H = `(12/(N(N+1))) · Σ (R_i²/n_i) − 3(N+1)`; `DF` = k − 1; `PValue` via χ² survival. `Details.per_group` carries `{n, mean_rank}`; effect size = ε² (epsilon-squared).

## Gotchas

- Buffered — combined values ranked across all groups under tie correction.
- Nonparametric alternative to `TEST_ANOVA_F` when normality fails or distributions are heavy-tailed.
- Global only — Dunn / Conover post-hoc not yet shipped; use repeated `TEST_MANN_WHITNEY_U` with manual Bonferroni.
- Tiny groups (`n_i < 5`) inflate type-I; gate with `PULSE_TEST_INSUFFICIENT_N`.
- Tests stochastic equality, not equal medians — differing shapes can reject on shape alone.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-mann-whitney-u`, `op-test-anova-welch`
