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

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), kind `frequency` only (a probability weight is `PULSE_WEIGHT_UNSUPPORTED` naming the kind): a row of weight w ranks as w identical rows — equals the test on the expanded rows. `Details` add `sum_weights` shaped like `n`; `n` / `n_total` stay raw row counts.
<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (required, ≥ 2 groups) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = tie-corrected H; `DF` = k − 1; `PValue` via χ² survival. `Details`: `groups`, `n`, `rank_sums`, `n_total`, `tie_factor`; `effect_size.epsilon_squared` = H·(n+1)/(n²−1), omitted when every value ties.

<!-- generated: reading-the-output -->

## Gotchas

- Buffered — combined values ranked across all groups under tie correction.
- Nonparametric alternative to one-way ANOVA for skewed or heavy-tailed data.
- Global only — no Dunn / Conover post-hoc<!-- feature: TEST_MANN_WHITNEY_U, capability:multiplicity -->; use pairwise `TEST_MANN_WHITNEY_U` with a `multiplicity` block (Holm/Bonferroni)<!-- /feature -->.
- Groups under ~5 rows: shaky p; only N < 2k is refused (`PULSE_TEST_INSUFFICIENT_N`). ε² unbanded.
- Tests stochastic equality, not equal medians — differing shapes can reject on shape alone.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-mann-whitney-u`, `op-test-anova-welch`
