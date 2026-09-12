---
name: op-test-mann-whitney-u
description: Nonparametric two-sample location test; buffered tie-corrected rank sum (Mann-Whitney U).
kind: operator
category: TEST
operator: TEST_MANN_WHITNEY_U
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, two-sample, comparison, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (required, exactly 2 groups) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = U_A; `PValue` two-sided via normal approximation with tie correction. `Details.per_group` = `{n_a, n_b, r_a}`; effect size = rank-biserial correlation in `Details.r_rb`.

## Gotchas

- Buffered — combined values ranked under tie correction; mean-rank ties consume memory.
- Robust alternative to `TEST_T` / `TEST_WELCH` when normality fails.
- Tests stochastic equality, not mean difference — divergence from `TEST_WELCH` is signal, not a bug.
- Small-n exact p not yet shipped — `PULSE_TEST_INSUFFICIENT_N` warns below the asymptotic threshold (n_a + n_b < 20).
- Paired data → `TEST_WILCOXON_SR`; k-group extension → `TEST_KRUSKAL_WALLIS`.

## See

- `pulse_examples_search tags=[nonparametric]`
- Skills: `statistical-testing`, `op-test-kruskal-wallis`, `op-test-wilcoxon-sr`, `op-test-welch`
