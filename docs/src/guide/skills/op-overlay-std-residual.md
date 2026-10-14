```yaml
name: op-overlay-std-residual
kind: operator
category: OVERLAY
operator: OVERLAY_STD_RESIDUAL
description: Per-cell Haberman adjusted standardized residual of the host crosstab, with expected counts and two-sided p-values as parts.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Adjusted residual for every crosstab cell: how far its count sits above or below what its row and column totals predict.

Questions it answers:

- Which cells drive the association in this crosstab?
- Which brand-by-attribute cells are over- or under-represented?

Use something else:

- `OVERLAY_CHISQ_MATRIX` when you want one test for the whole table.

## Params

`Scope` must be `cell`. `Ref` empty (implicit-margin). `Level`/`Within` `0`.

- `multiplicity` — optional; adjusts `parts.p` into `payload.p_adjusted` / `significant_adjusted`; raw p untouched.

## Host shape

MATRIX crosstab.

## Output

MATRIX residual `(O − E) / sqrt(E (1 − r/N) (1 − c/N))`, `E = r c / N` (R `chisq.test()$stdres`). Parts `expected` and `p` (two-sided normal), MATRIX on the same keys. `Summary`: `Baseline` 0, `Min`/`Max`/`Count` of defined residuals.

## Reading the output

- `cells.value`: The cell's adjusted standardized residual: (observed - expected) divided by its standard error, where expected is the count the row and column totals predict if the two fields were independent. Under independence it is about standard normal, so a value beyond plus or minus 1.96 is unusual at the 5% level; the cells with the largest absolute values are the ones driving the table's chi-square.
  - Caveat: A cell whose expected count is 0 (an empty row or column) has no residual and is null.

## Gotchas

- Weights as [`op-overlay-chisq-matrix`](op-overlay-chisq-matrix.md) (Kish-scaled under probability).
- Absent or undefined (NaN) cell = 0 (missing). `E = 0` ⇒ residual, p `null`. Any `E < 5` ⇒ one `PULSE_OVERLAY_EXPECTED_LOW`.
- Host < 2×2, zero total or a `Ref` ⇒ `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Code cell (`AGG_MODE`) ⇒ `PROCESSING_CONFIG` `categorical_code`.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-chisq-matrix`](op-overlay-chisq-matrix.md), [`op-overlay-fisher-exact-cell`](op-overlay-fisher-exact-cell.md).
