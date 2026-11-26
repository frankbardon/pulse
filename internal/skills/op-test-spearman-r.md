---
name: op-test-spearman-r
description: Rank-based correlation between field and field2 (monotonic association); buffered mid-ranks then Pearson.
kind: operator
category: TEST
operator: TEST_SPEARMAN_R
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, correlation-analysis, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

- `weight` — frequency only: a pair of weight w ranks as w identical pairs; `df` = Σw − 2 — equals the test on the expanded pairs. `details` add `sum_weights` beside `n` (raw rows).

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `field2` (required) — numeric (same set).

## Output

`statistic` = ρ (Spearman correlation, `[-1, 1]`); `df` = n − 2; `p_value` two-sided, R `cor.test` default: exact (AS 89) when untied and n ≤ 1290, else `t = ρ·√((n−2)/(1−ρ²))`. `details`: `n`, `t`, and `ties_x` / `ties_y` (the size of each tie group).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — mid-ranks each field under tie correction before running Pearson on the ranks.
- Detects monotonic association; robust to outliers (rank transform).
- Tier-2 variant `TEST_SPEARMAN_R/rank_pearson_post` runs over result columns. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used.
<!-- feature: MAT_CORRELATION -->
- Same p as a `MAT_CORRELATION` spearman cell on the same rows.
<!-- /feature -->
- ≥ 50% tied in either field → warning `PULSE_TEST_TIES_DOMINATE`: the test then mostly compares the share of tied values, not their order; the asymptotic p is unreliable.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: `statistical-testing`, `op-test-pearson-r`, `op-test-kendall-tau`
