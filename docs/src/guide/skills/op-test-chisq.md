```yaml
name: op-test-chisq
description: Chi-square independence test on a 2D Rows × Cols contingency table, or goodness of fit of Rows against expected proportions.
kind: operator
category: TEST
operator: TEST_CHISQ
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, cross-tabulation, proportion-analysis, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether two categorical fields are associated, or whether one categorical field's counts match planned proportions.

Questions it answers:

- Is preferred channel associated with age band?
- Is the mix of plan types different across regions?

Use something else:

- `TEST_FISHER_EXACT` when the table is 2x2 and some expected counts are small.
- `TEST_SPEARMAN_R` when both fields are numeric.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), both kinds: table of Σw per cell. Frequency: Pearson on the Σw table. Probability: Pearson on the table scaled to n_eff — first-order Kish approximation; not Rao-Scott (`survey::svychisq`). `expected_min`, the `< 5` guard, V, φ read the scaled table; `Details` add scalar `sum_weights` (+ `n_eff`); `n` stays raw rows.

- `expected` — object, optional: `{level: proportion}` → goodness of fit (`Variant` `goodness_of_fit`), `Cols` omitted. Each > 0, sum 1 ± 1e-9 (never renormalised), ≥ 2 labels; a listed level never observed counts as 0; an observed level not listed → `PULSE_TEST_EXPECTED_LABEL_MISSING`. Bad map / with `Cols` / `packed_bool` label ≠ `"0"`/`"1"` → `PULSE_TEST_EXPECTED_INVALID` (predict too). The SRM check: `{"control":0.5,"treatment":0.5}`.

Slot params: `Rows` (required, categorical or `packed_bool`), `Cols` (required unless `expected`, same). `Field` is ignored.

- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Rows`, `Cols` — categorical: `categorical_u8`/`u16`/`u32`, or `packed_bool` (levels named `"0"`/`"1"`; labels follow first appearance, as for any categorical); any other type is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime (rows first).

## Output

`Statistic` = χ² (no Yates); `DF` = `(rows−1)(cols−1)` (goodness of fit: labels − 1); `PValue` via χ² survival. Goodness of fit `Details`: `labels` (sorted), `observed`, `expected` (counts; Σw when weighted), `expected_proportions`, `n`, `expected_min`; no effect size. `Details`: `contingency`, `row_labels`/`col_labels`, `row_totals`/`col_totals`, `n`, `expected_min`. `effect_size.cramers_v` = √(χ²/(n·(min(r,c)−1))); `effect_size.phi` = √(χ²/n), 2×2 only.

## Reading the output

- `statistic`: Pearson's chi-square adds up, over every cell, how far the observed count is from the count expected if the two fields were unrelated. Larger values mean the table departs more from that pattern. With params.expected (Variant goodness_of_fit) the cells are the rows levels and the expected counts come from the given proportions, so it measures how far the observed mix is from the planned one.
- `details.expected_min`: The smallest expected count across all cells.

## Gotchas

- Any expected cell `< 5` emits `PULSE_TEST_EXPECTED_COUNT_TOO_LOW`.
- Streamable — builds the contingency table during the row scan.
- Sparse high-cardinality axes blow memory; filter them first.
- With `groups`: one result per group, each with `group_key`; `rows` / `cols` a grouper → `PULSE_TEST_CONTINGENCY_DEGENERATE`; a single-level group → `null` figures + a warning ([`statistical-testing`](statistical-testing.md)).
- Pairs with `Response.Crosstab`; `OVERLAY_CHISQ_VS_POP` is the FACET-host equivalent.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: [`statistical-testing`](statistical-testing.md), [`crosstab-guide`](crosstab-guide.md), [`op-test-fisher-exact`](op-test-fisher-exact.md), [`op-test-prop-z`](op-test-prop-z.md)
