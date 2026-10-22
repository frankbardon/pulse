```yaml
name: op-test-paired-t
description: Paired-sample t-test on the per-row difference d = Field − Field2; streamable via Welford on d.
kind: operator
category: TEST
operator: TEST_PAIRED_T
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, paired, parametric, before-after, comparison, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether the average change between two measurements of the same rows, such as before and after, differs from zero.

Questions it answers:

- On average, did customers' spend differ between the period before and after the loyalty programme started?
- Do patients' scores differ between the first and second visit?

Use something else:

- `TEST_WELCH` when the two sets of values come from different, unrelated subjects.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `partition_by` | list | — | Over two or more groupers the post-test runs once per partition: the rows sharing these leading group key columns (default every key but the last; [] = one run over every row). |

Slot params: `Field` (required, numeric), `Field2` (required, numeric — the pre / before value).
- `weight` — one weight per row = per pair (a null in either field drops the pair); one-sample t on d over w*, `df` = N*−1 (Σw or Kish n_eff). `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`Field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `Field2` — numeric (same set; same row pairs with Field).

## Output

`Statistic` = t; `DF` = n − 1; `PValue` two-sided via Student-t. `Details`: `mean_diff`, `variance` (of d), `n`, `ci_low`, `ci_high`. Effect size Cohen's d_z (unbanded).

## Reading the output

- `statistic`: t is the average of the per-row differences (Field - Field2) measured in standard errors of that average. Values far from 0 in either direction are unlikely if the true average difference is zero.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.mean_diff`: The average of Field - Field2 over complete pairs, in the field's own units.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`field2`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD2_NOT_NUMERIC`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- Pairing is **per-row**: Field and Field2 must already encode the (post, pre) pair on the same record. If pairing is across rows, build a paired column upstream first.
- Streamable — Welford runs on d = Field − Field2 in a single pass.
- A null in Field or Field2 drops the pair; `n` counts complete pairs.
- Tier-2 variant `TEST_PAIRED_T/paired_two_sided_post` runs over two output columns of the result set. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used.

## See

- `pulse_examples_search tags=[paired]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-wilcoxon-sr`](op-test-wilcoxon-sr.md), [`op-test-t`](op-test-t.md), [`op-test-pearson-r`](op-test-pearson-r.md)
