```yaml
name: op-test-chisq
description: Chi-square independence test on a 2D Rows × Cols contingency table.
kind: operator
category: TEST
operator: TEST_CHISQ
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, cross-tabulation, proportion-analysis, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether two categorical fields are associated by comparing a cross-tabulation with the counts expected if unrelated.

Questions it answers:

- Is preferred channel associated with age band?
- Is the mix of plan types different across regions?

Use something else:

- `TEST_FISHER_EXACT` when the table is 2x2 and some expected counts are small.
- `TEST_SPEARMAN_R` when both fields are numeric.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), both kinds: table of Σw per cell. Frequency: Pearson on the Σw table. Probability: Pearson on the table scaled to n_eff — first-order Kish approximation; not Rao-Scott (`survey::svychisq`). `expected_min`, the `< 5` guard, V, φ read the scaled table; `Details` add scalar `sum_weights` (+ `n_eff`); `n` stays raw rows.

Slot params: `Rows` (required, categorical), `Cols` (required, categorical). `Field` is ignored.

- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Rows` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`. `Cols` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = χ² (no Yates); `DF` = `(rows−1)(cols−1)`; `PValue` via χ² survival. `Details`: `contingency`, `row_labels`/`col_labels`, `row_totals`/`col_totals`, `n`, `expected_min`. `effect_size.cramers_v` = √(χ²/(n·(min(r,c)−1))); `effect_size.phi` = √(χ²/n), 2×2 only.

## Reading the output

- `statistic`: Pearson's chi-square adds up, over every cell, how far the observed count is from the count expected if the two fields were unrelated. Larger values mean the table departs more from that pattern.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.expected_min`: The smallest expected count across all cells.

## Gotchas

- Any expected cell `< 5` emits `PULSE_TEST_EXPECTED_COUNT_TOO_LOW`.
- Streamable — builds the contingency table during the row scan.
- Sparse high-cardinality axes blow memory; filter them first.
- Pairs with `Response.Crosstab`; `OVERLAY_CHISQ_VS_POP` is the FACET-host equivalent.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: [`statistical-testing`](statistical-testing.md), [`crosstab-guide`](crosstab-guide.md), [`op-test-fisher-exact`](op-test-fisher-exact.md), [`op-test-prop-z`](op-test-prop-z.md)
