---
name: overlay-system
description: Overlay framework — OverlaySpec composition, six reference families, three payload shapes, host-arm wiring (SERIES / FACET / CHAIN / FORMULA), parity overlays + Welford migration. Per-kind detail lives in op-overlay-* atomics.
type: guide
kind: design
applies_to: process, compose, facet
covers: [OVERLAY, OverlaySpec, OverlayLayer]
---

# Overlays

Additive, read-only decorations on a primary result. Specs ride `Request.Overlays`; layers ride `Response.Overlays[i]` in slot order. Overlays NEVER mutate the base — siblings keyed to host coordinates. Use for derived projections (share, index, delta, z-score, χ², p-value); base aggregations stay `AGG_*`.

```jsonc
{"overlays":[{"kind":"OVERLAY_SHARE_OF_ROW","scope":"cell","ref":{"margin":{"axis":"row"}}}]}
```

## OverlaySpec composition

`Kind`, `Scope`, `Ref`, optional `Name`/`Level`/`Within`/`Params`; Compose-only `Reference`/`Targets`. Runtime dispatches off `Kind`; predict rejects misshape (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `_LEVEL_OUT_OF_RANGE`, `_SCOPE_UNSUPPORTED`). `Scope` ∈ `cell|row|column|group|matrix|total`. `Ref` is a discriminated union over six families; exactly one populated. `Level`/`Within` mirror `normalize_level`/`normalize_within`; non-applicable kinds reject non-zero.

## Six reference families

`Margin{Axis}` (share + margin index/delta/zscore), `Sibling{Field,Value}` (SERIES siblings), `BaselineIndex{Position}` (windowed anchors), `Population{Cohort}` plus `Prior`/`RollingMean`/`YoY` (FACET + self-comparison), `Stage{Index|Name}` on `ChainOverlaySpec` (whole-chain), `Reference`+`Targets` (Compose). Implicit-margin kinds (χ²/Fisher) leave `Ref` empty; supplying any fires `_REF_INCOMPATIBLE_WITH_SHAPE`.

## Three payload shapes

`OverlayLayer.Payload.Shape` ∈ `scalar|series|matrix`. Scalar — `Payload.Scalar` + optional `OverlaySummary{Statistic,PValue,Parameters}`. Series — `Payload.Series.Entries[i].{Key,Value,Summary}` element-aligned with host keys (parallel-slice). Matrix — `Payload.Matrix.Cells[r][c]` mirrors `Response.Crosstab.Matrix.Cells`. Layer `Baseline` is the centerpoint (100 index, 0 delta/z-score); absent for inferential.

## Catalog — 38 kinds

Per-kind math in `op-overlay-*` atomics (E4-S10). Every `types.AllOverlayKinds()` constant listed:

`OVERLAY_SHARE_OF_ROW`, `OVERLAY_SHARE_OF_COL`, `OVERLAY_SHARE_OF_TOTAL`, `OVERLAY_INDEX_VS_MARGIN`, `OVERLAY_DELTA_VS_MARGIN`, `OVERLAY_ZSCORE_VS_MARGIN`, `OVERLAY_CHISQ_MATRIX`, `OVERLAY_CHISQ_ROW`, `OVERLAY_CHISQ_COL`, `OVERLAY_FISHER_EXACT_CELL`, `OVERLAY_T_CELL`, `OVERLAY_Z_CELL`, `OVERLAY_INDEX_VS_BASELINE`, `OVERLAY_DELTA_VS_BASELINE`, `OVERLAY_INDEX_VS_SIBLING`, `OVERLAY_DELTA_VS_SIBLING`, `OVERLAY_INDEX_VS_TOTAL`, `OVERLAY_ZSCORE_VS_TOTAL`, `OVERLAY_INDEX_VS_PRIOR`, `OVERLAY_YOY`, `OVERLAY_INDEX_VS_ROLLING_MEAN`, `OVERLAY_ZSCORE_VS_ROLLING`, `OVERLAY_RANK`, `OVERLAY_INDEX_VS_POP`, `OVERLAY_ZSCORE_VS_POP`, `OVERLAY_CHISQ_VS_POP`, `OVERLAY_KS_VS_POP`, `OVERLAY_INDEX_VS_STAGE`, `OVERLAY_DELTA_VS_STAGE`, `OVERLAY_INDEX_VS_REF`, `OVERLAY_DELTA_VS_REF`, `OVERLAY_CHISQ_VS_REF`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF`, `OVERLAY_PROP_Z_CELL`, `OVERLAY_PROP_Z_PANEL`, `OVERLAY_PANEL_INDEX_VS_REF`, `OVERLAY_FORMULA`.

## Host-arm wiring

Each host has a finalize hook walking `Request.Overlays`.

- **MATRIX (Crosstab)** — `processing/crosstab.go applyOverlaysToResponse`. v1 host: share triad + margin comparison + matrix inferential + parity + Compose vs-ref.
- **SERIES (windowed Process)** — `service/series_overlay.go` per-group fold; baseline/sibling/total/prior/YoY/rolling/rank (E3-S6).
- **FACET** — `service.applyFacetOverlays` at the buffered exit (E5-S6). `Ref.Population` recursion produces the comparison FacetResult.
- **CHAIN** — `service.applyChainOverlays` at post-stage barrier (E6-S3). Per-stage `Stages[i].Overlays` untouched; whole-chain layers on `ChainResponse.Overlays`. Shape divergence ⇒ `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT`.
- **FORMULA** — `OVERLAY_FORMULA` (E8-S2) evaluates expr-lang against earlier layers; refs resolve by `Name`.

Compose post-slot fold (`service/compose_overlay.go`) gates slot-label resolution, key alignment, schema match, dict-prefix drift before dispatch. `OverlayOptions.DictPrefixFast` ⇒ byte-equal prefix probe.

## Streamability

`types.OverlayStreamability` carries one row per kind. Descriptive SERIES kinds stream; inferential (χ²/KS/Fisher/parity/Welch) buffer. All Crosstab overlays buffered today. `pulse predict --json` reports per-spec classification.

## Parity overlays — Welford migration (v0.20.0)

The four parity kinds (`OVERLAY_T_CELL`, `OVERLAY_Z_CELL`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF`) read `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` populated by `AGG_WELFORD` via the `MetaAggregator` path. The legacy smuggle through `MatrixCell.Value` (`processing.WelfordTriple`) is **removed in v0.20.0** (E3-S7/S8); `MatrixCell.Value` now carries the scalar mean per `Aggregate()`. Additive contract preserved — without the triple, the handler falls back to `Params`-supplied `{mean, variance, n}`. P-values are byte-equal to `TEST_WELCH`/`TEST_Z_TWO_SAMPLE` row tests (shared recurrence + survival helpers). Canonical exact per-cell parametric test: `AGG_WELFORD` cell + one parity kind.

## Adding a new kind

Declare constant + `AllOverlayKinds()` (`types/overlay.go`); add `types/overlay_streamability.go` row; implement handler under `processing/overlay_*.go`; register in host dispatch; add predict validator (`descriptor/overlay_*.go`); surface constant in catalog above; ship `op-overlay-*` atomic.

## See

- `skills/response-components.md` — `CellComponents` (parity source).
- `skills/crosstab-guide.md` — MATRIX host.
- `skills/facet-design.md` — FACET host + `*_VS_POP`.
- `docs/src/internals/` — Internals chapter: CHAIN host wiring.
- `docs/src/internals/extension-points.md` — overlay extensions.
