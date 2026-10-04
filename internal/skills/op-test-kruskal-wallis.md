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

`Statistic` = tie-corrected H; `DF` = k − 1; `PValue` via χ² survival. `Details`: `groups`, `n`, `rank_sums`, `n_total`, `tie_factor`; `effect_size.epsilon_squared` = H·(n+1)/(n²−1), omitted when every value ties.

## Gotchas

- No weighted form yet: any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED`; set `"weight": null` on the slot to run it unweighted.
- Buffered — combined values ranked across all groups under tie correction.
- Nonparametric alternative to one-way ANOVA for skewed or heavy-tailed data.
- Global only — no Dunn / Conover post-hoc<!-- feature: TEST_MANN_WHITNEY_U -->; use pairwise `TEST_MANN_WHITNEY_U` + manual Holm/Bonferroni<!-- /feature -->.
- Groups under ~5 rows: shaky p; only N < 2k is refused (`PULSE_TEST_INSUFFICIENT_N`). ε² unbanded.
- Tests stochastic equality, not equal medians — differing shapes can reject on shape alone.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-mann-whitney-u`, `op-test-anova-welch`
