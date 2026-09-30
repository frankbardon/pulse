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

Compose-only multi-reference; no `Response.Components`.

## Params

`Scope` required, must be `cell`. `Reference` = panel index 0; `Targets` = panel indices 1..N. `OverlayOptions.MaxPanelTargets` int, default `16`, caps `len(Targets)` — an `Options` knob, never a param.

`Params` decodes to `PanelOverlayParams`: no fields yet, so absent and `{}` are identical. Malformed → `PULSE_OVERLAY_PARAM_MISSING` at `ValidateCompose`.

## Host shape

COMPOSE — MATRIX crosstab on every slot. Panel order `{Reference, Targets[0..N-1]}`.

## Output

MATRIX — `Cells[r][c].Value` is `[]float64`: upper-triangular pairwise p-values (row-major, no diagonal), length `M(M-1)/2`, `M = N+1`. Pair index `i*(2*M-i-1)/2 + (j-i-1)`. `Baseline` unset.

## Gotchas

- Pairwise byte-equal to `OVERLAY_PROP_Z_CELL` (shared `twoProportionZ`).
- `len(Targets) > MaxPanelTargets` → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- Missing row margin → cell value as n. Degenerate `(pooled ∈ {0,1}, se == 0)` → NaN at the pair + ONE `PULSE_OVERLAY_REF_ZERO` per (cell, pair).
- Reference value absent → nil slice + ONE `PULSE_OVERLAY_REF_ZERO`, `ref_missing=true`.
- Buffered.

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-prop-z-cell`.
