```yaml
name: op-test-wilcoxon-sr
description: Wilcoxon signed-rank test on per-row difference d = Field − Field2; buffered tie-corrected sign-rank.
kind: operator
category: TEST
operator: TEST_WILCOXON_SR
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, paired, before-after, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Rank-based check of whether paired before/after values tend to shift in one direction, without assuming a bell curve.

Questions it answers:

- Did each customer's rating tend to go up after the redesign?
- Do patients' skewed symptom scores tend to fall between visits?

Use something else:

- `TEST_MANN_WHITNEY_U` when the two sets of values come from different, unrelated subjects.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `partition_by` | list | — | Over two or more groupers the post-test runs once per partition: the rows sharing these leading group key columns (default every key but the last; [] = one run over every row). |

- `weight` — frequency only: a pair of weight w ranks as w identical pairs — equals the test on the expanded pairs. `Details` add `sum_weights` beside `n`; `n` and `zero_diffs` stay raw row counts.

Slot params: `Field` (required, numeric), `Field2` (required, numeric — the pre value).

## Inputs

`Field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `Field2` — numeric (same set; paired per-row with Field).

## Output

`Statistic` = min(W⁺, W⁻); `PValue` two-sided normal approx, tie-corrected. `Details`: `n` (non-zero pairs), `w_plus`, `w_minus`, `mu_w`, `var_w`, `z`, `zero_diffs`; `effect_size.rank_biserial` = (W⁺ − W⁻)/(W⁺ + W⁻) — > 0 ⇒ Field tends to exceed Field2 (sign of `z`).

## Reading the output

- `statistic`: The smaller of the two signed-rank sums: rank the non-zero differences (Field - Field2) by size, add the ranks of the positive ones and of the negative ones, and keep the smaller. The smaller it is, the more one direction dominates.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.z`: The normal score the p-value is read from, with a continuity correction of one half.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`field2`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD2_NOT_NUMERIC`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Buffered — |d| ranked across the whole set.
- ≥ 50% of |d| tied → warning `PULSE_TEST_TIES_DOMINATE`: the test then mostly compares the share of tied values, not their order; the asymptotic p is unreliable.
- Zero-diff pairs are dropped (Wilcoxon convention); reported in `Details.zero_diffs`.
- Nonparametric alternative to a paired t-test when d is non-normal.
- Asymptotic only; < 6 non-zero pairs is `PULSE_TEST_INSUFFICIENT_N`.
- Tier-2 variant `asymptotic_post` runs over two result-row columns. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used.
- Pairing is per row: both values sit on one record.

## See

- `pulse_examples_search tags=[paired]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-paired-t`](op-test-paired-t.md), [`op-test-mann-whitney-u`](op-test-mann-whitney-u.md)
