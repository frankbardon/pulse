---
name: op-test-wilcoxon-sr
description: Wilcoxon signed-rank test on per-row difference d = Field − Field2; buffered tie-corrected sign-rank.
kind: operator
category: TEST
operator: TEST_WILCOXON_SR
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, paired, before-after, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), kind `frequency` only (a probability weight is `PULSE_WEIGHT_UNSUPPORTED` naming the kind): a pair of weight w ranks as w identical pairs — equals the test on the expanded pairs. `Details` add `sum_weights` beside `n`; `n` and `zero_diffs` stay raw row counts.

Slot params: `Field` (required, numeric), `Field2` (required, numeric — the pre value).

## Inputs

`Field` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `Field2` — numeric (same set; paired per-row with Field).

## Output

`Statistic` = min(W⁺, W⁻); `PValue` two-sided normal approx, tie-corrected. `Details`: `n` (non-zero pairs), `w_plus`, `w_minus`, `mu_w`, `var_w`, `z`, `zero_diffs`; `effect_size.rank_biserial` = (W⁺ − W⁻)/(W⁺ + W⁻) — > 0 ⇒ Field tends to exceed Field2 (sign of `z`).

## Gotchas

- Buffered — |d| ranked across the whole set.
- Zero-diff pairs are dropped (Wilcoxon convention); reported in `Details.zero_diffs`.
- Nonparametric alternative to a paired t-test when d is non-normal.
- Asymptotic only; < 6 non-zero pairs is `PULSE_TEST_INSUFFICIENT_N`.
- Tier-2 variant `asymptotic_post` runs over two result-row columns.
- Pairing is per row: both values sit on one record.

## See

- `pulse_examples_search tags=[paired]`
- Skills: `statistical-testing`, `op-test-paired-t`, `op-test-mann-whitney-u`
