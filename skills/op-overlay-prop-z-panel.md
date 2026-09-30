---
name: op-overlay-prop-z-panel
kind: operator
category: OVERLAY
operator: OVERLAY_PROP_Z_PANEL
description: Compose-host multi-reference per-cell pairwise two-proportion z-test across N+1 slots.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, proportion-analysis]
---

Compose-only multi-reference. Buffered.

## Params

`Scope` = `cell`. `Reference` = panel 0, `Targets` = 1..N. `MaxPanelTargets` (`OverlayOptions`, default 16) caps `len(Targets)` and refuses FIRST (`PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`) — an `Options` knob, never a param.

`Params` → `PanelOverlayParams`; absent, `{}` and the explicit default stay byte-identical to the pre-params baseline. `n_source` picks each SLOT's n at the tested coordinate ("row" = that slot's own axis, never a pair axis):

- `row_margin_value` (default) — the row-margin VALUE off the payload; keeps the legacy `<= 0` fall back to the cell value.
- `cell_n_unweighted` — counted `n` from the slot's `Components.Crosstab`, BY KEY. No fallback; unreadable ⇒ skip the cell.
- `n_within` — the SAME payload row margin, optionally summed over a row-key prefix. No fallback.

`n_within_depth` (`*int`) is read by `n_within` alone, and the pointer is load-bearing: omitted ⇒ the exact per-slot row margin (no summing), `d` ⇒ margins summed over that slot's rows agreeing on the first `d+1` dims. Depth `0` ≠ omitted. Negative, or set with any other mode, ⇒ `PULSE_OVERLAY_PARAM_MISSING` on both arms; past a slot's row depth ⇒ same code at RUNTIME only, naming that slot. Slots may declare DIFFERENT row depths and one out-of-range slot refuses the whole spec (dropping it would change `M`).

`row_margin_n` is the `OVERLAY_PAIRWISE_*` spelling (a record COUNT — different carrier), NOT an alias: unknown here ⇒ `PULSE_OVERLAY_PARAM_MISSING` on both arms. `n_within` IS shared with that family by NAME only — there it sums `CellCounts` over a PAIR-axis slab at one fixed opposite index.

## Host shape

COMPOSE — MATRIX crosstab per slot, order `{Reference, Targets…}`. `cell_n_unweighted` needs components on EVERY slot: disabled / non-crosstab ⇒ `PULSE_OVERLAY_COMPONENTS_REQUIRED`, unresolved ⇒ `PULSE_OVERLAY_SLOT_NOT_CROSSTAB`. Mode-scoped — the default never gates.

## Output

MATRIX — `Cells[r][c].Value` is `[]float64`: upper-triangular p-values (row-major, no diagonal), length `M(M-1)/2`, `M = N+1`. Pair index `i*(2*M-i-1)/2 + (j-i-1)`. `Baseline` unset.

## Gotchas

- Pairs byte-equal `OVERLAY_PROP_Z_CELL` (shared `twoProportionZ`).
- Degenerate `(pooled ∈ {0,1}, se == 0)` → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per (cell, pair).
- Absent value → nil slice + `REF_ZERO` `ref_missing`; an unreadable counted n adds `n_missing` + `slot_index`.

## See

- Skills: `overlay-system`, `pairwise-n-sources`, `compose-requests`, `op-overlay-prop-z-cell`.
