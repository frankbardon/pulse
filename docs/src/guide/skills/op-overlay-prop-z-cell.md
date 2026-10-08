```yaml
name: op-overlay-prop-z-cell
kind: operator
category: OVERLAY
operator: OVERLAY_PROP_Z_CELL
description: Compose-host per-cell two-proportion z-test against the reference slot's matching cell.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, proportion-analysis]
```

Compose-only. Overlays decorate the host; no `Response.Components`.

## Use when

Two-proportion z-test for every crosstab cell in a Compose request: does the target's share in that cell differ from the reference's?

Questions it answers:

- Which answer shares changed between this wave and last wave?
- Where does the test market's share differ from the control market's?

Use something else:

- `OVERLAY_PROP_Z_PANEL` when you compare three or more requests at once.

## Params

`Scope` must be `cell`. `Reference` (string, required) — reference slot label. `Targets` ([]string, required) — target slot labels.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

COMPOSE — MATRIX crosstab on both reference + target slot. Cell value = success count; matching row margin = sample size. Sample reference reuses the two-proportion z-test's pooled-SE recurrence.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value as `float64`. Mirrors reference matrix's RowKeys / ColumnKeys. One layer per target. Layer `Baseline` unset (inferential).

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided p-value comparing the target's share (cell / row total) with the reference's share in the same cell.
  - Caveat: A cell whose row total is missing or zero on either side is NaN, with a warning (PULSE_OVERLAY_REF_ZERO): it gets no test.

## Gotchas

- Weighted slots (both kinds): p̂ = cell/row margin (Σw); n = the row base's N*: the margin Σw under frequency, its Kish `n_eff` under probability (never Σw; no readable floor ⇒ missing margin). A probability slot with components disabled ⇒ `PROCESSING_CONFIG` (predict AND runtime). `Summary.Parameters` adds `sum_weights` (+ `n_eff`).
- Pooled SE: `sqrt(pooled × (1-pooled) × (1/n_target + 1/n_ref))` where `pooled = (target+ref) / (n_target+n_ref)`. Reuses `normalTwoSidedP` — byte-equal to the two-proportion z-test on the same `(success, n)` pair.
- Degenerate inputs (`pooled ∈ {0,1}`, `se == 0`, missing/zero row margin on EITHER side — never the cell value as n) → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected cell.
- Schema-match + key-alignment gates at the slot barrier.
- Buffered (inferential).

## See

- Skills: [`overlay-system`](overlay-system.md), [`compose-requests`](compose-requests.md), [`op-overlay-prop-z-panel`](op-overlay-prop-z-panel.md), [`op-test-prop-z`](op-test-prop-z.md).
