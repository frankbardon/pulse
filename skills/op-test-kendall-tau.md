---
name: op-test-kendall-tau
description: Concordance-based correlation between Field and Field2; buffered O(n²) pair count under tie correction.
kind: operator
category: TEST
operator: TEST_KENDALL_TAU
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, correlation-analysis, small-sample, buffered-pipeline]
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

`Statistic` = τ_b (Kendall tau-b, `[-1, 1]`); `PValue` two-sided via normal approximation with tie-variance adjustment. `Details.concordant`, `Details.discordant`, `Details.n_ties_x`, `Details.n_ties_y`.

## Gotchas

- Buffered O(n²) pair enumeration — expensive on large n; cap with `FILTER_RANGE` or pre-sample.
- Preferred over `TEST_SPEARMAN_R` for small samples and heavy ties (tau-b corrects for ties in either column).
- Distribution-free; no normality assumption.
- Tier-2 variant `TEST_KENDALL_TAU/tau_b_post` runs over result columns.
- For high-cardinality numeric fields the O(n²) cost dominates — `TEST_SPEARMAN_R` is asymptotically cheaper.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: `statistical-testing`, `op-test-spearman-r`, `op-test-pearson-r`
