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

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)` (else `PULSE_TEST_INVALID_ALPHA`); also sets the interval's coverage, `1 − alpha`. |

- `weight` — frequency only: the exact test on the Σw table — equals the expanded rows. `contingency` becomes the Σw table; `n` stays raw rows, `sum_weights` beside it.

## Inputs

`Rows` / `Cols` (both required, 2 levels each) — categorical (`packed_bool` levels `"0"`/`"1"`) (a yes/no x yes/no table is the natural 2x2). `Field` ignored.

## Output

`Statistic` = odds ratio; `PValue` = exact two-sided via hypergeometric tail (sum of tables with probability ≤ observed × (1 + 1e-7), `fisher.test`'s tie rule). `Details.contingency` = `[[a, b], [c, d]]` with rows / columns sorted byte-wise (`row_labels` / `col_labels`); `odds_ratio` (= `Statistic`) is the conditional MLE R's `fisher.test` reports, not ad/bc: zero a or d -> 0, else zero b or c -> +Inf (`null` on the wire). `ci_low` / `ci_high` = `fisher.test`'s exact conditional interval on it at `1 − alpha`: lower exactly 0 with a zero a or d, upper +Inf (`null`, key kept) with a zero b or c.

## Reading the output

- `statistic`: The conditional maximum-likelihood odds ratio of the 2x2 table in details.contingency (the estimate R's fisher.test reports), also in details.odds_ratio: 1 means the odds of the first column are the same in both rows, above 1 higher in the first row, below 1 lower.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.ci_high`: Upper end of the exact conditional confidence interval for the odds ratio.

## Contract

- Types (`rows`, `cols`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Strictly 2×2 — other shapes → `PULSE_TEST_CONTINGENCY_DEGENERATE`.
- The canonical small-sample choice when any expected cell `< 5`.
- Buffered — needs the full contingency table.
- Two-sided p sums every table no more likely than observed (ties within 1e-7 relative count, as R); tools doubling one tail disagree slightly.
- Effect size = odds ratio (not Cramér's V); take log for symmetry around 0.

## See

- `pulse_examples_search tags=[exact-test]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-chisq`](op-test-chisq.md), [`op-test-prop-z`](op-test-prop-z.md)
