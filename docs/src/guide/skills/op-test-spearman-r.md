```yaml
name: op-test-spearman-r
description: Rank-based correlation between Field and Field2 (monotonic association); buffered mid-ranks then Pearson.
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

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), kind `frequency` only (a probability weight is `PULSE_WEIGHT_UNSUPPORTED` naming the kind): a pair of weight w ranks as w identical pairs; `DF` = Σw − 2 — equals the test on the expanded pairs. `Details` add `sum_weights` beside `n` (raw rows).

Slot params: `Field` (required, numeric), `Field2` (required, numeric).

- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `Field2` — numeric (same set).

## Output

`Statistic` = ρ (Spearman correlation, `[-1, 1]`); `DF` = n − 2; `PValue` two-sided via `t = ρ·√((n−2)/(1−ρ²))`. `Details.n_ties_x` and `Details.n_ties_y` surface tie counts.

## Reading the output

- `statistic`: Spearman's rho is Pearson's r computed on ranks, from -1 to +1: how consistently one field rises (or falls) as the other rises, whether or not in a straight line.
  - Sign: positive means the two fields tend to rise together; negative means one field tends to fall as the other rises.
  - Caveat: Correlation is not causation: a third factor may drive both fields.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- Buffered — mid-ranks each column under tie correction before running Pearson on the ranks.
- Detects monotonic association; robust to outliers (rank transform).
- Tier-2 variant `TEST_SPEARMAN_R/rank_pearson_post` runs over result columns.
- Heavy ties degrade the asymptotic p-value.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-pearson-r`](op-test-pearson-r.md), [`op-test-kendall-tau`](op-test-kendall-tau.md)
