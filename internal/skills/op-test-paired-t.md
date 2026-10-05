---
name: op-test-paired-t
description: Paired-sample t-test on the per-row difference d = Field − Field2; streamable via Welford on d.
kind: operator
category: TEST
operator: TEST_PAIRED_T
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, paired, parametric, before-after, comparison, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.

Slot params: `Field` (required, numeric), `Field2` (required, numeric — the pre / before value).
- `weight` — slot weight (`null` opts out), both kinds: one weight per row = per pair (a null in either field drops the pair); one-sample t on d over w*, `df` = N*−1 (Σw or Kish n_eff). `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.
<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

`Field` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `Field2` — numeric (same set; same row pairs with Field).

## Output

`Statistic` = t; `DF` = n − 1; `PValue` two-sided via Student-t. `Details`: `mean_diff`, `variance` (of d), `n`, `ci_low`, `ci_high`. Effect size Cohen's d_z (unbanded).

## Gotchas

- Pairing is **per-row**: Field and Field2 must already encode the (post, pre) pair on the same record. If pairing is across rows, build a paired column upstream first.
- Streamable — Welford runs on d = Field − Field2 in a single pass.
- A null in Field or Field2 drops the pair; `n` counts complete pairs.
- Tier-2 variant `TEST_PAIRED_T/paired_two_sided_post` runs over two output columns of the result set.

## See

- `pulse_examples_search tags=[paired]`
- Skills: `statistical-testing`, `op-test-wilcoxon-sr`, `op-test-t`, `op-test-pearson-r`
