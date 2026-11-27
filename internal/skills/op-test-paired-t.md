---
name: op-test-paired-t
description: Paired-sample t-test on the per-row difference d = field − field2; streamable via Welford on d.
kind: operator
category: TEST
operator: TEST_PAIRED_T
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, paired, parametric, before-after, comparison, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

- `weight` — one weight per row = per pair (a null in either field drops the pair); one-sample t on d over w*, `df` = N*−1 (Σw or Kish n_eff). `details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `field2` (required, the pre / before value) — numeric (same set), on the same record.

## Output

`statistic` = t; `df` = n − 1; `p_value` two-sided via Student-t. `details`: `mean_diff`, `variance` (of d), `n`, `ci_low`, `ci_high`. `effect_size.cohens_d` = Cohen's d_z (mean of d over its sd).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Pairing is **per-row**: `field` and `field2` must already encode the (post, pre) pair on the same record. If pairing is across rows, reshape so both values sit on one record first.
- Streamable — Welford runs on d = `field` − `field2` in a single pass.
- A null in `field` or `field2` drops the pair; `n` counts complete pairs.
- Tier-2 variant `TEST_PAIRED_T/paired_two_sided_post` runs over two output columns of the result rows. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used.

## See

- `pulse_examples_search tags=[paired]`
- Skills: `statistical-testing`, `op-test-wilcoxon-sr`, `op-test-t`, `op-test-pearson-r`
