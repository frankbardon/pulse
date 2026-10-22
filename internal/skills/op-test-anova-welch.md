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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `n_col` | string | (required) | Both required on the post-test (summary) form: result-row columns holding each group's size and sample variance. |
| `variance_col` | string | (required) | Both required on the post-test (summary) form: result-row columns holding each group's size and sample variance. |
| `partition_by` | list | — | Over two or more groupers the post-test runs once per partition: the rows sharing these leading group key columns (default every key but the last; [] = one run over every row). |

- `weight` — per-group moments on w*, `weights` = N*_j/s²_j, df on N*_j−1 (Σw or Kish n_eff). `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (required, ≥ 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = Welch F*; `DF` = k−1; `PValue` via F survival. `Details`: `groups`, `n`, `group_means`, `group_variances`, `weights`, `weighted_mean`, `df_between`, `df_within` (Welch df). `effect_size.omega_squared` = df_b(F*−1)/(df_b(F*−1)+N) clamped ≥ 0 (Lakens 2013).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Streamable — same per-group Welford as classic one-way ANOVA; only the statistic + denominator change.
- Use when group spreads may differ.
- Tier-2 `welch_one_way_post` reads per-group `{mean, variance, n}`; same keys. A group row with a null in a used column drops (listwise, R's NA default).
- Post-hoc: Tukey HSD assumes equal variance<!-- feature: TEST_WELCH, capability:multiplicity -->; for unequal fall back to pairwise `TEST_WELCH` with a `multiplicity` block<!-- /feature -->.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-brown-forsythe`, `op-test-tukey-hsd`
