---
name: op-test-anova-f
description: One-way ANOVA F-test comparing the means of a numeric Field across k groups defined by SplitBy.
kind: operator
category: TEST
operator: TEST_ANOVA_F
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, k-sample, parametric, comparison, streaming-friendly]
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

- `weight` — SS on w*, each group on its own N*_g (Σw_g or Kish n_eff_g); `df_within` = ΣN*_g−k, may be fractional. `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < k+1 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (required, ≥ 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = F; `DF` = k−1; `PValue` via F survival. `Details`: `groups`, `n`, `group_means`, `ss_between`, `ss_within`, `df_within`, `ms_within`; `effect_size.{eta_squared, omega_squared}` (ω² clamped ≥0).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Streamable — per-group Welford feeds both SS terms.
- Rejects globally, not per pair<!-- feature: TEST_TUKEY_HSD --> — follow with tier-2 `TEST_TUKEY_HSD` via `ms_within` / `df_within`<!-- /feature -->.
- Assumes equal spread; a large p from a spread test is not evidence of it.
- Normality: a normality test describes shape; it is not a gate.
- Tier-2 `one_way_from_summary` reads group summaries; same keys. A group row with a null in a used column drops (listwise, R's NA default).

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-tukey-hsd`, `op-test-anova-welch`, `op-test-kruskal-wallis`
