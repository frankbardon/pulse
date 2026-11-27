---
name: op-test-wilcoxon-sr
description: Wilcoxon signed-rank test on per-row difference d = field − field2; buffered tie-corrected sign-rank.
kind: operator
category: TEST
operator: TEST_WILCOXON_SR
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, paired, before-after, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

- `weight` — frequency only: a pair of weight w ranks as w identical pairs — equals the test on the expanded pairs. `details` add `sum_weights` beside `n`; `n` and `zero_diffs` stay raw row counts.


## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `field2` (required, the pre value) — numeric (same set), on the same record.

## Output

`statistic` = min(W⁺, W⁻); `p_value` two-sided normal approx, tie-corrected. `details`: `n` (non-zero pairs), `w_plus`, `w_minus`, `mu_w`, `var_w`, `z`, `zero_diffs`; `effect_size.rank_biserial` = (W⁺ − W⁻)/(W⁺ + W⁻) — > 0 ⇒ `field` tends to exceed `field2` (sign of `z`).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — |d| ranked across the whole set.
- ≥ 50% of |d| tied → warning `PULSE_TEST_TIES_DOMINATE`: the test then mostly compares the share of tied values, not their order; the asymptotic p is unreliable.
- Zero-diff pairs are dropped (Wilcoxon convention); reported in `details.zero_diffs`.
- Nonparametric alternative to a paired t-test when d is non-normal.
- Asymptotic only; < 6 non-zero pairs is `PULSE_TEST_INSUFFICIENT_N`.
- Tier-2 variant `asymptotic_post` runs over two result-row columns. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used.

## See

- `pulse_examples_search tags=[paired]`
- Skills: `statistical-testing`, `op-test-paired-t`, `op-test-mann-whitney-u`
