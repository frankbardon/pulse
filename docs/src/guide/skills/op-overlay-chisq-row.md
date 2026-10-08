```yaml
name: op-overlay-chisq-row
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_ROW
description: Per-row χ² goodness-of-fit test across the host crosstab's contingency table.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Chi-square test per crosstab row: checks whether each row's spread across the columns departs from the table's overall column mix.

Questions it answers:

- Which regions have an answer mix unlike the overall mix?
- Which product lines get a different spread of complaint types?

Use something else:

- `OVERLAY_CHISQ_COL` when the groups are the columns rather than the rows.

## Params

`Scope` (enum, required) — must be `row`. `Ref` (object, empty) — implicit-margin — leave empty. `Level`/`Within` must be `0`.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Family: implicit-margin χ² (no `Ref`). Compatible with any crosstab regardless of cell aggregator; row-axis twin of the column χ².

## Output

SERIES — `OverlayLayer.Payload.Shape = "series"`. One `SeriesEntry` per row key carrying `Summary.Statistic` (row χ²), `Summary.PValue`, `Summary.Parameters["df"]` = `cols - 1`. Entries align element-for-element with host `RowKeys`. Layer `Baseline` unset.

## Reading the output

- `summary.statistic`: In each series entry (one per crosstab row): chi-square for that row's counts against the counts its row total would give at the table's overall column shares. 0 means the row's mix matches the overall mix; read the entry's p_value rather than the raw value.
- `summary.p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- Weighted host (both kinds, any source): cells are Σw; under probability each row is scaled to its margin's Kish `n_eff` (expected-low on the scaled row) — first-order Kish, not Rao-Scott. Each entry's `Parameters` adds that row's `sum_weights` (+ `n_eff`). Probability host with components disabled ⇒ `PROCESSING_CONFIG` (no `n_eff` to scale by; predict AND runtime).
- Reuses `chiSquareSurvival` — byte-equal p-values to every χ² test and overlay on the same contingency.
- Any `expected < 5` in a row emits ONE `PULSE_OVERLAY_EXPECTED_LOW` per offending row.
- Absent host cell treated as observed count of 0.
- Scope MUST be `row`. Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inherent).

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-chisq-col`](op-overlay-chisq-col.md), [`op-test-chisq`](op-test-chisq.md).
