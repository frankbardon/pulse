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

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — frequency only: a pair of weight w ranks as w identical pairs; `DF` = Σw − 2 — equals the test on the expanded pairs. `Details` add `sum_weights` beside `n` (raw rows).

Slot params: `Field` (required, numeric), `Field2` (required, numeric).


## Inputs

`Field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `Field2` — numeric (same set).

## Output

`Statistic` = ρ (Spearman correlation, `[-1, 1]`); `DF` = n − 2; `PValue` two-sided, R `cor.test` default: exact (AS 89) when untied and n ≤ 1290, else `t = ρ·√((n−2)/(1−ρ²))`. `Details.n_ties_x` and `Details.n_ties_y` surface tie counts.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — mid-ranks each column under tie correction before running Pearson on the ranks.
- Detects monotonic association; robust to outliers (rank transform).
- Tier-2 variant `TEST_SPEARMAN_R/rank_pearson_post` runs over result columns. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used.
<!-- feature: MAT_CORRELATION -->
- Same p as a `MAT_CORRELATION` spearman cell on the same rows.
<!-- /feature -->
- ≥ 50% tied in either column → warning `PULSE_TEST_TIES_DOMINATE`: the test then mostly compares the share of tied values, not their order; the asymptotic p is unreliable.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: `statistical-testing`, `op-test-pearson-r`, `op-test-kendall-tau`
