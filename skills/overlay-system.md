---
name: overlay-system
description: Overlay framework — OverlaySpec composition, six reference families, three payload shapes, host-arm wiring (SERIES / FACET / CHAIN / FORMULA), parity overlays + Welford migration. Per-kind detail lives in op-overlay-* atomics.
type: guide
kind: design
applies_to: process, compose, facet
covers: [OVERLAY, OverlaySpec, OverlayLayer]
---

# Overlays

Additive, read-only decorations on a primary result. Specs ride `Request.Overlays`, layers `Response.Overlays[i]` in slot order. Overlays NEVER mutate the base — they are siblings keyed to host coordinates. Use for derived projections (share, index, delta, z, χ², p); base aggregations stay `AGG_*`.

```jsonc
{"overlays":[{"kind":"OVERLAY_SHARE_OF_ROW","scope":"cell","ref":{"margin":{"axis":"row"}}}]}
```

## OverlaySpec composition

`Kind`, `Scope`, `Ref`, optional `Name`/`Level`/`Within`/`Params`; Compose-only `Reference`/`Targets`. Predict rejects misshape (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `_LEVEL_OUT_OF_RANGE`, `_SCOPE_UNSUPPORTED`). `Scope` ∈ `cell|row|column|group|matrix|total`. `Ref` is a discriminated union over six families, exactly one populated. `Level`/`Within` mirror normalize.

## Six reference families

`Margin{Axis}` (share + margin index/delta/z), `Sibling{Field,Value}` (SERIES), `BaselineIndex{Position}` (windowed), `Population{Cohort}` + `Prior`/`RollingMean`/`YoY` (FACET + self-compare), `Stage{Index|Name}` (chain), `Reference`+`Targets` (Compose). Implicit-margin kinds (χ², Fisher) leave `Ref` empty.

## Three payload shapes

`Payload.Shape` ∈ `scalar|series|matrix`. Scalar — `Payload.Scalar` + optional `OverlaySummary{Statistic,PValue,Parameters}`. Series — `Payload.Series.Entries[i].{Key,Value,Summary}` aligned to host keys. Matrix — `Payload.Matrix.Cells[r][c]` mirrors host cells. `Baseline` is the centerpoint (100 index, 0 delta/z), absent for inferential kinds.

## Catalog

Authoritative list + count: `types.AllOverlayKinds()` / `pulse_manifest.overlays`; never hardcode. Per-kind math in `op-overlay-*` atomics. Families (drop the `OVERLAY_` prefix):

Share (`SHARE_OF_ROW`/`_COL`/`_TOTAL`); margin compare (`INDEX_`/`DELTA_`/`ZSCORE_VS_MARGIN`, `*_VS_TOTAL`, `RANK`); matrix inferential (`CHISQ_*`, `FISHER_EXACT_CELL`, `PROP_Z_CELL`, `T_CELL`, `Z_CELL`); intra-matrix pairwise (`PAIRWISE_PROP_Z`/`_PROBIT_T`/`_WELCH_T`/`_TWO_MEANS_Z`); SERIES self-compare (baseline / sibling / prior / `YOY` / rolling); FACET population (`*_VS_POP`); Compose vs-ref + panel (`*_VS_REF`, `PROP_Z_PANEL`, `PANEL_INDEX_VS_REF`); chain (`*_VS_STAGE`); `FORMULA`.

## Host-arm wiring

- **MATRIX (Crosstab)** — `applyOverlaysToResponse` (`processing/crosstab.go`), called from BOTH the buffered and the fused exit. Share triad + margin compare + matrix inferential + pairwise + parity + vs-ref.
- **SERIES (windowed Process)** — `service/series_overlay.go` per-group fold.
- **FACET** — `service.applyFacetOverlays` at the buffered exit; `Ref.Population` recursion produces the comparison FacetResult.
- **CHAIN** — `service.applyChainOverlays` post-stage; per-stage `Stages[i].Overlays` untouched, whole-chain on `ChainResponse.Overlays`. Divergent shape ⇒ `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT`.
- **FORMULA** — evaluates expr-lang against earlier layers; refs resolve by `Name`.

Compose post-slot fold (`service/compose_overlay.go`) gates slot-label, key alignment, schema and dict-prefix drift; `DictPrefixFast` ⇒ prefix probe.

## Per-layer warnings (`OverlayLayer.Warnings`)

Additive `[]OverlayWarning` slot (`omitempty`); empty/nil elides the key, so overlay-free responses stay byte-identical (`Test*_OverlayFreeByteIdentical`). Each entry carries `Code`, `Message`, `Details map[string]any`; canonical code `PULSE_OVERLAY_REF_ZERO` + siblings (`pulse errors lookup`).

Routing is dispatcher-stamped, service-distributed. `processing/overlay_chain_dispatch.go` and `overlay_compose_dispatch.go` stamp `Details["overlay_index"] = i` on every warning a handler appends (handlers see only target / reference indices, so the dispatcher owns spec position). `service.applyChainOverlays` / `applyComposeOverlays` route each warning to `out.Overlays[idx].Warnings`; layers with none keep `Warnings == nil`, a missing key falls back to layer 0. The Compose-host barrier rides the same slot on `ComposedResponse.Overlays[i]`.

## Streamability

`types.OverlayStreamability` — one row per kind. Descriptive SERIES stream; inferential (χ²/KS/Fisher/parity/Welch) buffer. Every MATRIX-host kind is `false` — the crosstab fold runs AFTER the matrix is finalised, so it is not an in-pass computation.

That flag does NOT pick the crosstab's execution path. **`Request.Overlays` no longer forces buffered** — `CanFuseCrosstab` ignores the slot; `RunCrosstabFused` folds layers at its exit through the same hook, byte-identical to buffered. The one surviving reason an overlay-carrying crosstab still buffers is the CELL-AGGREGATOR arm: `AGG_WELFORD` is non-mergeable, so the two kinds reading its `{n, mean, variance}` triple (`OVERLAY_PAIRWISE_WELCH_T`, `OVERLAY_PAIRWISE_TWO_MEANS_Z`) stay buffered — expected, pinned by `TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays`. `OVERLAY_PAIRWISE_PROP_Z` over `AGG_WEIGHTED_MEAN` fuses, even over a `GROUP_SET_PER_ELEMENT` axis. `pulse predict --json` classifies per spec.

## Parity overlays — Welford migration (v0.20.0)

The four parity kinds (`OVERLAY_T_CELL`, `OVERLAY_Z_CELL`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF`) read `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (populated by `AGG_WELFORD` via `MetaAggregator`); the legacy `WelfordTriple` smuggle through `MatrixCell.Value` is **removed in v0.20.0** — that slot carries the scalar mean. Absent the triple, handlers fall back to `Params`-supplied `{mean, variance, n}`. P-values byte-equal `TEST_WELCH` / `TEST_Z_TWO_SAMPLE`.

## Adding a new kind

Declare the constant + `AllOverlayKinds()`; add the `overlay_streamability.go` row; handler in `processing/overlay_*.go`; register in host dispatch; predict validator (`descriptor/overlay_*.go`); ship the `op-overlay-*` atomic.

## See

- `response-components` (`CellComponents`), `crosstab-guide` (MATRIX host + fused path), `facet-design` (`*_VS_POP`), `docs/src/internals/extension-points.md`.
