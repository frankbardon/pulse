---
name: op-test-kendall-tau
description: Concordance-based correlation between field and field2; buffered O(n²) pair count under tie correction.
kind: operator
category: TEST
operator: TEST_KENDALL_TAU
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, correlation-analysis, small-sample, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

- `weight` — frequency only: a row pair counts w_i·w_j times — equals τ-b on the expanded pairs. `details` add `sum_weights` beside `n` (raw rows); pair counts become Σ w_i·w_j.

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `field2` (required) — numeric (same set).

## Output

`statistic` = τ_b (Kendall tau-b, `[-1, 1]`); `p_value` two-sided, R `cor.test` default: exact when untied and n < 50, else normal approximation (tie-adjusted variance, no continuity correction); `details.z` = S/√Var(S); also `concordant`, `discordant`, `ties_x`, `ties_y`, `s`, `var_s`, `n`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered O(n²) pair enumeration — expensive on large n; filter or pre-sample first.
- Suits small samples and heavy ties (tau-b corrects for ties in either field).
- ≥ 50% tied → warning `PULSE_TEST_TIES_DOMINATE`: the test then mostly compares the share of tied values, not their order; the asymptotic p is unreliable.
- Distribution-free; no normality assumption.
- Tier-2 variant `TEST_KENDALL_TAU/tau_b_post` runs over result columns. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used.
<!-- feature: MAT_CORRELATION -->
- Same p as a `MAT_CORRELATION` kendall cell on the same rows.
<!-- /feature -->
- For high-cardinality numeric fields the O(n²) cost dominates.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: `statistical-testing`, `op-test-spearman-r`, `op-test-pearson-r`
