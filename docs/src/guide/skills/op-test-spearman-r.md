```yaml
name: op-test-spearman-r
description: Rank-based correlation between field and field2 (monotonic association); buffered mid-ranks then Pearson.
kind: operator
category: TEST
operator: TEST_SPEARMAN_R
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, correlation-analysis, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Measures how consistently one numeric field rises (or falls) as the other rises, using ranks, so the link need not be a straight line.

Questions it answers:

- Do higher-ranked products also tend to sell more, even if not in proportion?
- Does satisfaction tend to rise with tenure?

Use something else:

- `TEST_PEARSON_R` when you want the strength of a straight-line link.
- `TEST_CHISQ` when both fields are categories.

## Params

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

- `weight` — frequency only: a pair of weight w ranks as w identical pairs; `df` = Σw − 2 — equals the test on the expanded pairs. `details` add `sum_weights` beside `n` (raw rows).

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `field2` (required) — numeric (same set).

## Output

`statistic` = ρ (Spearman correlation, `[-1, 1]`); `df` = n − 2; `p_value` two-sided, R `cor.test` default: exact (AS 89) when untied and n ≤ 1290, else `t = ρ·√((n−2)/(1−ρ²))`. `details`: `n`, `t`, and `ties_x` / `ties_y` (the size of each tie group).

## Reading the output

- `statistic`: Spearman's rho is Pearson's r computed on ranks, from -1 to +1: how consistently one field rises (or falls) as the other rises, whether or not in a straight line.
  - Sign: positive means the two fields tend to rise together; negative means one field tends to fall as the other rises.
  - Caveat: Correlation is not causation: a third factor may drive both fields.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`field2`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD2_NOT_NUMERIC`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Buffered — mid-ranks each field under tie correction before running Pearson on the ranks.
- Detects monotonic association; robust to outliers (rank transform).
- Tier-2 variant `TEST_SPEARMAN_R/rank_pearson_post` runs over result columns. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used.
- Same p as a `MAT_CORRELATION` spearman cell on the same rows.
- ≥ 50% tied in either field → warning `PULSE_TEST_TIES_DOMINATE`: the test then mostly compares the share of tied values, not their order; the asymptotic p is unreliable.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-pearson-r`](op-test-pearson-r.md), [`op-test-kendall-tau`](op-test-kendall-tau.md)
