```yaml
name: op-overlay-fisher-exact-cell
kind: operator
category: OVERLAY
operator: OVERLAY_FISHER_EXACT_CELL
description: Per-cell Fisher's exact two-sided p-value over a 2×2 contingency formed from each host crosstab cell + its margins.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, exact-test, small-sample]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Fisher's exact test for every crosstab cell: checks whether being in that row goes with being in that column more or less than expected.

Questions it answers:

- Which answer-by-segment cells stand out in this banner table?
- In a small table, which cells hold more cases than the margins predict?

Use something else:

- `OVERLAY_CHISQ_MATRIX` when you want one test for the whole table.

## Params

`Scope` must be `cell`. `Ref` (object, empty) — implicit-margin — leave empty. `Level`/`Within` must be `0`.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

MATRIX crosstab. Implicit-margin family (no `Ref`). Canonical low-count χ² backstop — the correct surface when `expected < 5` breaks χ² approximation.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value as `float64`. Mirrors host RowKeys / ColumnKeys. Absent host cells stay absent on the overlay. Layer `Baseline` unset (inferential).

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided exact p-value of its own 2x2 table: this row versus the rest, by this column versus the rest.
  - Caveat: It does not say which way or how far the cell departs: compare the cell's count with what its row and column totals predict (or an OVERLAY_INDEX_VS_MARGIN layer) for direction and size.

## Gotchas

- Frequency-only: on a frequency-weighted host the exact test runs on the integer Σw table and `Summary.Parameters` adds `sum_weights`; a probability weight — on the overlay slot OR only on the host cell — is `PULSE_WEIGHT_UNSUPPORTED` naming the kind (predict AND runtime).
- 2×2 contingency: `[cell, row_margin-cell; col_margin-cell, grand-row_margin-col_margin+cell]`. Reuses `logHypergeometric` — byte-equal to the Fisher exact test on the same 2×2.
- Cochran rule: any of four expected counts `< 1` OR `>= 20%` of expected `< 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` per offending cell. Advisory only — Fisher stays exact.
- `grand_total <= 0` → absent cells everywhere + ONE `PULSE_OVERLAY_REF_ZERO`.
- Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inherent).

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-chisq-matrix`](op-overlay-chisq-matrix.md), [`op-test-fisher-exact`](op-test-fisher-exact.md).
