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

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |

- `weight` — frequency only: a row of weight w ranks as w identical rows — equals the test on the expanded rows. `Details` add `sum_weights` shaped like `n`; `n` / `n_total` stay raw row counts.

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (required, ≥ 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = tie-corrected H; `DF` = k − 1; `PValue` via χ² survival. `Details`: `groups`, `n`, `rank_sums`, `n_total`, `tie_factor`; `effect_size.epsilon_squared` = H·(n+1)/(n²−1), omitted when every value ties.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — combined values ranked across all groups under tie correction.
- ≥ 50% tied → warning `PULSE_TEST_TIES_DOMINATE`: the test then mostly compares the share of tied values, not their order; the asymptotic p is unreliable.
- Nonparametric alternative to one-way ANOVA for skewed or heavy-tailed data.
- Global only — no Dunn / Conover post-hoc<!-- feature: TEST_MANN_WHITNEY_U, capability:multiplicity -->; use pairwise `TEST_MANN_WHITNEY_U` with a `multiplicity` block (Holm/Bonferroni)<!-- /feature -->.
- Groups under ~5 rows: shaky p; only N < 2k is refused (`PULSE_TEST_INSUFFICIENT_N`). ε² unbanded.
- Tests stochastic equality, not equal medians — differing shapes can reject on shape alone.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-mann-whitney-u`, `op-test-anova-welch`
