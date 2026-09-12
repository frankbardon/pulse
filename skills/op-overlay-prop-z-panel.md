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

Compose-only multi-reference. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

`Scope` required, must be `cell`. `Reference` = panel index 0; `Targets` = panel indices 1..N. `OverlayOptions.MaxPanelTargets` int, default `16`, caps `len(Targets)`.

## Host shape

COMPOSE — MATRIX crosstab on every slot. Panel order `{Reference, Targets[0..N-1]}`. First kind to emit a per-cell vector payload via `MatrixCell.Value` as `[]float64`.

## Output

MATRIX — `Cells[r][c].Value` is `[]float64` of upper-triangular pairwise p-values (row-major, no diagonal), length `M(M-1)/2` for `M = N+1`. Pair index `i*(2*M-i-1)/2 + (j-i-1)`. Layer `Baseline` unset.

## Gotchas

- Reuses `twoProportionZ` + `standardNormalCDF`; pairwise byte-equal to `OVERLAY_PROP_Z_CELL`.
- `len(Targets) > MaxPanelTargets` → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- Missing row margins → cell value as sample size. Degenerate `(pooled ∈ {0,1}, se == 0)` → NaN at the pair + ONE `PULSE_OVERLAY_REF_ZERO` per (cell, pair).
- Any reference value absent → nil slice + ONE `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`.
- Buffered (inferential family).

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-prop-z-cell`, `op-overlay-panel-index-vs-ref`.
