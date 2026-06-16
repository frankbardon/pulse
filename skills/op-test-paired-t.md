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

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |

Slot params: `Field` (required, numeric), `Field2` (required, numeric — the pre / before value).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `Field2` | numeric (same set; same row pairs with Field) |

## Output

`Statistic` = t; `DF` = n − 1; `PValue` two-sided via Student-t. `Details.mean_diff`, `Details.sd_diff`, `Details.n`. Effect size Cohen's d_z.

## Gotchas

- Pairing is **per-row**: Field and Field2 must already encode the (post, pre) pair on the same record. If pairing is across rows, build a paired column upstream first.
- Streamable — Welford runs on d = Field − Field2 in a single pass.
- Null in either Field or Field2 drops the pair; reported in `Details.n_dropped`.
- Severe non-normality in d → switch to `TEST_WILCOXON_SR`.
- Tier-2 variant `TEST_PAIRED_T/paired_two_sided_post` runs over two output columns of the result set.

## See

- `pulse_examples_search tags=[paired]`
- Skills: `statistical-testing`, `op-test-wilcoxon-sr`, `op-test-t`, `op-test-pearson-r`
