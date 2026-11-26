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

`scope` must be `cell`. `reference` (string, required) — reference slot label. `targets` (list, required) — target slot labels.


## Host shape

COMPOSE — MATRIX crosstab on both reference + target slot. Cell value = success count; its row margin = sample size.

## Output

MATRIX — `payload.matrix.cells[r][c].value` = the two-sided p-value, on the reference matrix's keys. `summary.baseline` unset (inferential). One layer per target, in `targets` order: one target keeps `name`; two or more are `<name>__<target>`, each the one-target layer. Over `options.max_panel_targets` (16) → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided p-value comparing the target's share (cell / row total) with the reference's share in the same cell.
  - Caveat: A cell whose row total is missing or zero on either side is NaN, with a warning (PULSE_OVERLAY_REF_ZERO): it gets no test.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `row`, `column`, `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- An undefined (NaN) target or reference cell is a MISSING pair: the cell stays absent, no warning, out of `summary.count`.
- Weighted slots (both kinds): p̂ = cell/row margin (Σw); n = the row base's N*: the margin Σw under frequency, its Kish `n_eff` under probability (never Σw; no readable floor ⇒ missing margin). A probability slot with components disabled ⇒ `PROCESSING_CONFIG` (predict AND runtime). `summary.parameters` adds `sum_weights` (+ `n_eff`).
- Pooled SE: `sqrt(pooled × (1-pooled) × (1/n_target + 1/n_ref))` where `pooled = (target+ref) / (n_target+n_ref)`. p equals the two-proportion z-test's on the same `(success, n)` pair.
- Degenerate inputs (`pooled ∈ {0,1}`, `se == 0`, missing/zero row margin on EITHER side — never the cell value as n) → `null` cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected cell.
- Slots must match on schema and keys.
- A code cell (`AGG_MODE` over `categorical_*` / `packed_bool`) on either slot → `PROCESSING_CONFIG`, reason `categorical_code`; use a count cell.

## See

- Skills: [`overlay-system`](overlay-system.md), [`compose-requests`](compose-requests.md), [`op-overlay-prop-z-panel`](op-overlay-prop-z-panel.md), [`op-test-prop-z`](op-test-prop-z.md).
