```yaml
name: op-test-fisher-exact
description: Exact two-sided p-value for a 2×2 Rows × Cols contingency table; small-sample CHISQ alternative.
kind: operator
category: TEST
operator: TEST_FISHER_EXACT
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, exact-test, cross-tabulation, proportion-analysis, small-sample, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Exact test of whether two yes/no fields are associated, built for small 2x2 tables.

Questions it answers:

- In a small pilot, did the treated group recover more often than the control group?
- With only a few dozen responses, is opting in associated with the sign-up channel?

Use something else:

- `TEST_CHISQ` when either field has more than two categories.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), kind `frequency` only (a probability weight is `PULSE_WEIGHT_UNSUPPORTED` naming the kind): the exact test on the Σw table — equals the expanded rows. `contingency` becomes the Σw table; `n` stays raw rows, `sum_weights` beside it.
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Rows` / `Cols` (both required, 2 levels each) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`. `Field` ignored.

## Output

`Statistic` = odds ratio; `PValue` = exact two-sided via hypergeometric tail (sum of tables with probability ≤ observed). `Details.contingency` = `[[a, b], [c, d]]` in first-seen order (`row_labels` / `col_labels`); `odds_ratio` = ad/bc, no Haldane fix: zero b or c -> +Inf, zero a or d -> 0.

## Reading the output

- `statistic`: The sample odds ratio (a*d)/(b*c) of the 2x2 table in details.contingency, also in details.odds_ratio: 1 means the odds of the first column are the same in both rows, above 1 higher in the first row, below 1 lower.
  - Caveat: Bands apply to max(OR, 1/OR), so an odds ratio of 0.25 reads like one of 4.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- Strictly 2×2 — other shapes → `PULSE_TEST_CONTINGENCY_DEGENERATE`.
- The canonical small-sample choice when any expected cell `< 5`.
- Buffered — needs the full contingency table.
- Two-sided p sums every table no more likely than observed; tools doubling one tail disagree slightly.
- Effect size = odds ratio (not Cramér's V); take log for symmetry around 0.

## See

- `pulse_examples_search tags=[exact-test]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-chisq`](op-test-chisq.md), [`op-test-prop-z`](op-test-prop-z.md)
