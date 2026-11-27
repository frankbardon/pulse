---
name: op-test-brown-forsythe
description: Homogeneity-of-variance test; one-way ANOVA on absolute deviations from per-group medians.
kind: operator
category: TEST
operator: TEST_BROWN_FORSYTHE
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, homogeneity-test, parametric, k-sample, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

- `weight` — frequency only: weighted group medians, ANOVA on Σw — equals the expanded rows, the `n ≥ 2` per-group floor on Σw too (one row of weight 2 runs). `details` add `sum_weights` beside `n` (raw rows).

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `split_by` (required, ≥ 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`statistic` = F (ANOVA on absolute deviations from per-group medians); `df` = k-1 (`df_within` = N-k in `details`); `p_value` via F-distribution survival. Flat `details`: `groups`, `n`, `group_medians`, `abs_dev_means`, `ss_between`, `ss_within`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — per-group medians require a sort.
- Not a pre-ANOVA gate: a large p is not evidence of equal spread.
- More robust than mean-based Levene under non-normality.
- Tier-2 variant `TEST_BROWN_FORSYTHE/median_post` runs over result columns. A row with a null in a used column drops listwise (R's NA default); `details.n` counts the rows used.
- Tiny groups destabilize the median; `n_i < 2` or N ≤ k → `PULSE_TEST_INSUFFICIENT_N`.
- Zero within-group spread (each group's `|x − median|` constant, `ss_within` = 0): F = +Inf (`null` on the wire), p = 0 when groups differ; F and p `null` (no rejection) when all deviations are equal — R `anova(lm(...))`'s exact limit.

## See

- `pulse_examples_search tags=[homogeneity-test]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-anova-welch`
