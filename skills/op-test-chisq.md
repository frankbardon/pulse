---
name: op-test-chisq
description: Chi-square independence test on a 2D Rows × Cols contingency table.
kind: operator
category: TEST
operator: TEST_CHISQ
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, cross-tabulation, proportion-analysis, streaming-friendly]
---

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |

Slot params: `Rows` (required, categorical), `Cols` (required, categorical). `Field` is ignored.

## Inputs

| Param | Accepted field types |
|---|---|
| `Rows` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` |
| `Cols` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` |

## Output

`Statistic` = χ²; `DF` = `(rows−1)(cols−1)`; `PValue` via χ² survival. `Details.contingency` carries the observed table; `Details.expected` the expected counts; effect size Cramér's V.

## Gotchas

- Any expected cell `< 5` emits `PULSE_TEST_EXPECTED_COUNT_TOO_LOW` — switch to `TEST_FISHER_EXACT` (2×2 only).
- Streamable — builds the contingency table during the row scan.
- Sparse high-cardinality axes blow memory; pair with `FILTER_INCLUDE` on Rows / Cols.
- Pairs with `Response.Crosstab`; `OVERLAY_CHISQ_VS_POP` is the FACET-host equivalent.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: `statistical-testing`, `crosstab-guide`, `op-test-fisher-exact`, `op-test-prop-z`
