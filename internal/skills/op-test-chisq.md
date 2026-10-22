---
name: op-test-chisq
description: Chi-square independence test on a 2D Rows × Cols contingency table, or goodness of fit of Rows against expected proportions.
kind: operator
category: TEST
operator: TEST_CHISQ
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, cross-tabulation, proportion-analysis, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — table of Σw per cell. Frequency: Pearson on the Σw table. Probability: Pearson on the table scaled to n_eff — first-order Kish approximation; not Rao-Scott (`survey::svychisq`). `expected_min`, the `< 5` guard, V, φ read the scaled table; `Details` add scalar `sum_weights` (+ `n_eff`); `n` stays raw rows.

- `expected` — object, optional: `{level: proportion}` → goodness of fit (`Variant` `goodness_of_fit`), `Cols` omitted. Each > 0, sum 1 ± 1e-9 (never renormalised), ≥ 2 labels; a listed level never observed counts as 0; an observed level not listed → `PULSE_TEST_EXPECTED_LABEL_MISSING`. Bad map / with `Cols` / `packed_bool` label ≠ `"0"`/`"1"` → `PULSE_TEST_EXPECTED_INVALID` (predict too). The SRM check: `{"control":0.5,"treatment":0.5}`.

Slot params: `Rows` (required, categorical or `packed_bool`), `Cols` (required unless `expected`, same). `Field` is ignored.


## Inputs

`Rows`, `Cols` — categorical: `categorical_u8`/`u16`/`u32`, or `packed_bool` (levels named `"0"`/`"1"`; labels sorted byte-wise, as for any categorical).

## Output

`Statistic` = χ² (no Yates); `DF` = `(rows−1)(cols−1)` (goodness of fit: labels − 1); `PValue` via χ² survival. Goodness of fit `Details`: `labels` (sorted), `observed`, `expected` (counts; Σw when weighted), `expected_proportions`, `n`, `expected_min`; no effect size. `Details`: `contingency` (indexed by `row_labels`/`col_labels`, both sorted byte-wise), `row_totals`/`col_totals`, `n`, `expected_min`. `effect_size.cramers_v` = √(χ²/(n·(min(r,c)−1))); `effect_size.phi` = √(χ²/n), 2×2 only.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Any expected cell `< 5` emits `PULSE_TEST_EXPECTED_COUNT_TOO_LOW`.
- Streamable — builds the contingency table during the row scan.
- Sparse high-cardinality axes blow memory; filter them first.
- With `groups`: one result per group, each with `group_key`; `rows` / `cols` a grouper → `PULSE_TEST_CONTINGENCY_DEGENERATE`; a single-level group → `null` figures + a warning (`statistical-testing`).
- Pairs with `Response.Crosstab`<!-- feature: OVERLAY_CHISQ_VS_POP -->; `OVERLAY_CHISQ_VS_POP` is the FACET-host equivalent<!-- /feature -->.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: `statistical-testing`, `crosstab-guide`, `op-test-fisher-exact`, `op-test-prop-z`
