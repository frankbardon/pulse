---
name: op-test-spearman-r
description: Rank-based correlation between Field and Field2 (monotonic association); buffered mid-ranks then Pearson.
kind: operator
category: TEST
operator: TEST_SPEARMAN_R
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, correlation-analysis, buffered-pipeline]
---

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |

Slot params: `Field` (required, numeric), `Field2` (required, numeric).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `Field2` | numeric (same set) |

## Output

`Statistic` = ρ (Spearman correlation, `[-1, 1]`); `DF` = n − 2; `PValue` two-sided via `t = ρ·√((n−2)/(1−ρ²))`. `Details.n_ties_x` and `Details.n_ties_y` surface tie counts.

## Gotchas

- Buffered — mid-ranks each column under tie correction before running Pearson on the ranks.
- Detects monotonic association; robust to outliers (rank transform).
- Tier-2 variant `TEST_SPEARMAN_R/rank_pearson_post` runs over result columns.
- Heavy ties degrade the asymptotic p-value; large tie fraction → switch to `TEST_KENDALL_TAU`.
- Linear association preferred → `TEST_PEARSON_R` (parametric, streamable).

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: `statistical-testing`, `op-test-pearson-r`, `op-test-kendall-tau`
