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

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |

Slot params: `Field` (required, numeric), `Field2` (required, numeric — the pre value).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `Field2` | numeric (same set; paired per-row with Field) |

## Output

`Statistic` = W (positive-rank sum); `PValue` two-sided via normal approximation with tie correction. `Details.n_effective` after zero-diff pairs drop; `Details.r_rb` rank-biserial effect.

## Gotchas

- Buffered — |d| must be ranked across the whole set.
- Zero-diff pairs are dropped (Wilcoxon convention); reported in `Details.n_zero`.
- Nonparametric alternative to `TEST_PAIRED_T` when d is non-normal.
- Asymptotic only — small-n exact p not yet shipped; `PULSE_TEST_INSUFFICIENT_N` warns below n_effective ≥ 10.
- Tier-2 variant `TEST_WILCOXON_SR/asymptotic_post` runs over two output columns of the result set.
- Pairing per-row — same caveat as `TEST_PAIRED_T`.

## See

- `pulse_examples_search tags=[paired]`
- Skills: `statistical-testing`, `op-test-paired-t`, `op-test-mann-whitney-u`
