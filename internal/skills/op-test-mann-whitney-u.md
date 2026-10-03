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

`Statistic` = U_min; `PValue` two-sided normal approx, tie-corrected. `Details`: `groups` (sorted), `n`, `u_a`, `u_b`, `u_min`, `r_a`, `r_b`, `mu_u`, `var_u`, `z`; `effect_size.rank_biserial` = (U_A − U_B)/(n_A·n_B) — > 0 ⇒ `groups[0]` tends larger (sign of `z`).

## Gotchas

- Buffered — combined values mid-ranked under tie correction.
- Robust alternative to `TEST_T` / `TEST_WELCH` when normality fails.
- Tests stochastic equality, not mean difference — divergence from `TEST_WELCH` is signal, not a bug.
- Asymptotic only (no exact p); n < 2 per group is `PULSE_TEST_INSUFFICIENT_N`.
- Paired data → `TEST_WILCOXON_SR`; k-group extension → `TEST_KRUSKAL_WALLIS`.

## See

- `pulse_examples_search tags=[nonparametric]`
- Skills: `statistical-testing`, `op-test-kruskal-wallis`, `op-test-wilcoxon-sr`, `op-test-welch`
