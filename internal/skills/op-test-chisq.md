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

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.

Slot params: `Rows` (required, categorical), `Cols` (required, categorical). `Field` is ignored.

## Inputs

`Rows` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`. `Cols` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = χ² (no Yates); `DF` = `(rows−1)(cols−1)`; `PValue` via χ² survival. `Details`: `contingency`, `row_labels`/`col_labels`, `row_totals`/`col_totals`, `n`, `expected_min`. `effect_size.cramers_v` = √(χ²/(n·(min(r,c)−1))); `effect_size.phi` = √(χ²/n), 2×2 only.

## Gotchas

- Any expected cell `< 5` emits `PULSE_TEST_EXPECTED_COUNT_TOO_LOW`.
- Streamable — builds the contingency table during the row scan.
- Sparse high-cardinality axes blow memory; filter them first.
- Pairs with `Response.Crosstab`<!-- feature: OVERLAY_CHISQ_VS_POP -->; `OVERLAY_CHISQ_VS_POP` is the FACET-host equivalent<!-- /feature -->.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: `statistical-testing`, `crosstab-guide`, `op-test-fisher-exact`, `op-test-prop-z`
