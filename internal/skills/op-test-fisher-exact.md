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

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.

## Inputs

`Rows` / `Cols` (both required, 2 levels each) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`. `Field` ignored.

## Output

`Statistic` = odds ratio; `PValue` = exact two-sided via hypergeometric tail (sum of tables with probability ≤ observed). `Details.contingency` = `[[a, b], [c, d]]`; `Details.odds_ratio`.

## Gotchas

- Strictly 2×2 — k > 2 levels → `PULSE_TEST_FISHER_NOT_2X2`. Use `TEST_CHISQ` for larger tables.
- Canonical small-sample alternative to `TEST_CHISQ` when any expected cell `< 5`.
- Buffered — needs the full contingency table.
- Exact two-sided p uses the "sum of less-likely tables" convention (Fisher's original); other tools sometimes use 2× the smaller one-sided tail and disagree slightly.
- Effect size = odds ratio (not Cramér's V); take log for symmetry around 0.

## See

- `pulse_examples_search tags=[exact-test]`
- Skills: `statistical-testing`, `op-test-chisq`, `op-test-prop-z`
