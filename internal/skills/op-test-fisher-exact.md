---
name: op-test-fisher-exact
description: Exact two-sided p-value for a 2×2 Rows × Cols contingency table; small-sample CHISQ alternative.
kind: operator
category: TEST
operator: TEST_FISHER_EXACT
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, exact-test, cross-tabulation, proportion-analysis, small-sample, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`, in `(0, 1)` (else `PULSE_TEST_INVALID_ALPHA`); also sets the interval's coverage, `1 − alpha`.
- `weight` — slot weight (`null` opts out), kind `frequency` only (a probability weight is `PULSE_WEIGHT_UNSUPPORTED` naming the kind): the exact test on the Σw table — equals the expanded rows. `contingency` becomes the Σw table; `n` stays raw rows, `sum_weights` beside it.
<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

`Rows` / `Cols` (both required, 2 levels each) — categorical: `categorical_u8`/`u16`/`u32`, or `packed_bool` (levels `"0"`/`"1"`) (a yes/no x yes/no table is the natural 2x2); any other type is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime (rows first). `Field` ignored.

## Output

`Statistic` = odds ratio; `PValue` = exact two-sided via hypergeometric tail (sum of tables with probability ≤ observed × (1 + 1e-7), `fisher.test`'s tie rule). `Details.contingency` = `[[a, b], [c, d]]` with rows / columns sorted byte-wise (`row_labels` / `col_labels`); `odds_ratio` (= `Statistic`) is the conditional MLE R's `fisher.test` reports, not ad/bc: zero a or d -> 0, else zero b or c -> +Inf (`null` on the wire). `ci_low` / `ci_high` = `fisher.test`'s exact conditional interval on it at `1 − alpha`: lower exactly 0 with a zero a or d, upper +Inf (`null`, key kept) with a zero b or c.

<!-- generated: reading-the-output -->

## Gotchas

- Strictly 2×2 — other shapes → `PULSE_TEST_CONTINGENCY_DEGENERATE`.
- The canonical small-sample choice when any expected cell `< 5`.
- Buffered — needs the full contingency table.
- Two-sided p sums every table no more likely than observed (ties within 1e-7 relative count, as R); tools doubling one tail disagree slightly.
- Effect size = odds ratio (not Cramér's V); take log for symmetry around 0.

## See

- `pulse_examples_search tags=[exact-test]`
- Skills: `statistical-testing`, `op-test-chisq`, `op-test-prop-z`
