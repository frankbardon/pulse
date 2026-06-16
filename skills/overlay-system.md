---
name: overlay-system
description: Overlay framework — OverlaySpec composition, six reference families, three payload shapes, host-arm wiring (SERIES / FACET / CHAIN / FORMULA), parity overlays + Welford migration. Per-kind detail lives in op-overlay-* atomics.
type: guide
kind: design
applies_to: process, compose, facet
covers: [OVERLAY, OverlaySpec, OverlayLayer]
---

# Overlays

Additive, read-only decorations on a primary result. Specs ride `Request.Overlays`; layers ride `Response.Overlays[i]` in slot order. Overlays NEVER mutate the base — siblings keyed to host coordinates. Use for derived projections (share, index, delta, z, χ², p); base aggregations stay `AGG_*`.

```jsonc
{"overlays":[{"kind":"OVERLAY_SHARE_OF_ROW","scope":"cell","ref":{"margin":{"axis":"row"}}}]}
```

## OverlaySpec composition

`Kind`, `Scope`, `Ref`, optional `Name`/`Level`/`Within`/`Params`; Compose-only `Reference`/`Targets`. Predict rejects misshape (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `_LEVEL_OUT_OF_RANGE`, `_SCOPE_UNSUPPORTED`). `Scope` ∈ `cell|row|column|group|matrix|total`. `Ref` is a discriminated union over six families; exactly one populated. `Level`/`Within` mirror normalize.

## Six reference families

`Margin{Axis}` (share + margin index/delta/z), `Sibling{Field,Value}` (SERIES), `BaselineIndex{Position}` (windowed), `Population{Cohort}` + `Prior`/`RollingMean`/`YoY` (FACET + self-compare), `Stage{Index|Name}` (whole-chain), `Reference`+`Targets` (Compose). Implicit-margin (χ²/Fisher) leave `Ref` empty.

## Three payload shapes

`Payload.Shape` ∈ `scalar|series|matrix`. Scalar — `Payload.Scalar` + optional `OverlaySummary{Statistic,PValue,Parameters}`. Series — `Payload.Series.Entries[i].{Key,Value,Summary}` aligned with host keys. Matrix — `Payload.Matrix.Cells[r][c]` mirrors host cells. `Baseline` is the centerpoint (100 index, 0 delta/z); absent for inferential.

## Catalog — 38 kinds

Per-kind math in `op-overlay-*` atomics. `types.AllOverlayKinds()`:

`OVERLAY_SHARE_OF_ROW`, `OVERLAY_SHARE_OF_COL`, `OVERLAY_SHARE_OF_TOTAL`, `OVERLAY_INDEX_VS_MARGIN`, `OVERLAY_DELTA_VS_MARGIN`, `OVERLAY_ZSCORE_VS_MARGIN`, `OVERLAY_CHISQ_MATRIX`, `OVERLAY_CHISQ_ROW`, `OVERLAY_CHISQ_COL`, `OVERLAY_FISHER_EXACT_CELL`, `OVERLAY_T_CELL`, `OVERLAY_Z_CELL`, `OVERLAY_INDEX_VS_BASELINE`, `OVERLAY_DELTA_VS_BASELINE`, `OVERLAY_INDEX_VS_SIBLING`, `OVERLAY_DELTA_VS_SIBLING`, `OVERLAY_INDEX_VS_TOTAL`, `OVERLAY_ZSCORE_VS_TOTAL`, `OVERLAY_INDEX_VS_PRIOR`, `OVERLAY_YOY`, `OVERLAY_INDEX_VS_ROLLING_MEAN`, `OVERLAY_ZSCORE_VS_ROLLING`, `OVERLAY_RANK`, `OVERLAY_INDEX_VS_POP`, `OVERLAY_ZSCORE_VS_POP`, `OVERLAY_CHISQ_VS_POP`, `OVERLAY_KS_VS_POP`, `OVERLAY_INDEX_VS_STAGE`, `OVERLAY_DELTA_VS_STAGE`, `OVERLAY_INDEX_VS_REF`, `OVERLAY_DELTA_VS_REF`, `OVERLAY_CHISQ_VS_REF`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF`, `OVERLAY_PROP_Z_CELL`, `OVERLAY_PROP_Z_PANEL`, `OVERLAY_PANEL_INDEX_VS_REF`, `OVERLAY_FORMULA`.

## Host-arm wiring

Each host has a finalize hook walking `Request.Overlays`.

- **MATRIX (Crosstab)** — `processing/crosstab.go applyOverlaysToResponse`. Share triad + margin compare + matrix inferential + parity + vs-ref.
- **SERIES (windowed Process)** — `service/series_overlay.go` per-group fold (E3-S6).
- **FACET** — `service.applyFacetOverlays` at buffered exit (E5-S6); `Ref.Population` recursion produces the comparison FacetResult.
- **CHAIN** — `service.applyChainOverlays` post-stage (E6-S3); per-stage `Stages[i].Overlays` untouched, whole-chain on `ChainResponse.Overlays`. Shape divergence ⇒ `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT`.
- **FORMULA** — `OVERLAY_FORMULA` (E8-S2) evaluates expr-lang against earlier layers; refs resolve by `Name`.

Compose post-slot fold (`service/compose_overlay.go`) gates slot-label, key alignment, schema, dict-prefix drift; `OverlayOptions.DictPrefixFast` ⇒ prefix probe.


## Per-layer warnings (`OverlayLayer.Warnings`)

Additive `[]OverlayWarning` slot (`omitempty`); empty/nil elides the key, so overlay-free responses stay byte-identical (`TestOverlayLayer_WarningsFreeByteIdentical`, mirrored by `TestComposedResponse_OverlayFreeByteIdentical`). Each entry carries `Code`, `Message`, `Details map[string]any`; canonical codes `PULSE_OVERLAY_REF_ZERO` + siblings (`pulse errors lookup`).

Routing is dispatcher-stamped, service-distributed. `processing/overlay_chain_dispatch.go` and `overlay_compose_dispatch.go` stamp `Details["overlay_index"] = i` on every warning a handler appends; handlers receive only target / reference indices, so the dispatcher owns spec position. `service.applyChainOverlays` and `service.applyComposeOverlays` consume the flat slice and route each warning to `out.Overlays[idx].Warnings` by index; layers with no warnings keep `Warnings == nil`. Missing key falls back to layer 0. Compose-host barrier rides the same `OverlayLayer.Warnings` slot exposed on `ComposedResponse.Overlays[i]` by the v0.21.0 Compose facade lift. Per-kind catalogs in `op-overlay-*.md`.

## Streamability

`types.OverlayStreamability` — one row per kind. Descriptive SERIES stream; inferential (χ²/KS/Fisher/parity/Welch) buffer. Crosstab overlays buffer. `pulse predict --json` classifies per spec.

## Parity overlays — Welford migration (v0.20.0)

The four parity kinds (`OVERLAY_T_CELL`, `OVERLAY_Z_CELL`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF`) read `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (populated by `AGG_WELFORD` via `MetaAggregator`). Legacy `processing.WelfordTriple` smuggle through `MatrixCell.Value` is **removed in v0.20.0** (E3-S7/S8); `MatrixCell.Value` carries the scalar mean. Absent the triple, handlers fall back to `Params`-supplied `{mean, variance, n}`. P-values byte-equal to `TEST_WELCH` / `TEST_Z_TWO_SAMPLE`. Canonical per-cell parametric: `AGG_WELFORD` cell + parity kind.

## Adding a new kind

Declare constant + `AllOverlayKinds()`; add `overlay_streamability.go` row; handler in `processing/overlay_*.go`; register in host dispatch; predict validator (`descriptor/overlay_*.go`); add to catalog; ship `op-overlay-*` atomic.

## See

- `skills/response-components.md` — `CellComponents` (parity source).
- `skills/crosstab-guide.md` — MATRIX host.
- `skills/facet-design.md` — FACET host + `*_VS_POP`.
- `docs/src/internals/extension-points.md` — overlay extensions.
