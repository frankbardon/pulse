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

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, ≥ 2 groups).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `SplitBy` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` |

## Output

`Statistic` = F; `DF` = `k−1` (between); `Details.df_within`, `Details.ms_between`, `Details.ms_within`; `PValue` via F-distribution survival. Effect size η² in `Details`.

## Gotchas

- Streamable — per-group Welford feeds both SS_between and SS_within.
- Globally rejects but not which pair — follow with `TEST_TUKEY_HSD` (tier-2) using `ms_within` and `df_within`.
- Equal-variance gate: `TEST_BROWN_FORSYTHE`; if it rejects, switch to `TEST_ANOVA_WELCH`.
- Normality gate: `TEST_SHAPIRO_WILK` per group (n ≤ 5000); severe non-normality → `TEST_KRUSKAL_WALLIS`.
- Repeated-measures design → `TEST_ANOVA_RM` (needs `SubjectField`).
- Tier-2 variant `TEST_ANOVA_F/one_way_from_summary` consumes upstream per-group summaries.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-tukey-hsd`, `op-test-anova-welch`, `op-test-kruskal-wallis`
