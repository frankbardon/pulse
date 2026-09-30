---
name: overlay-system
description: Overlay framework — OverlaySpec composition, six reference families, three payload shapes, host-arm wiring (SERIES / FACET / CHAIN / FORMULA), parity overlays + Welford migration. Per-kind detail lives in op-overlay-* atomics.
type: guide
kind: design
applies_to: process, compose, facet
covers: [OVERLAY, OverlaySpec, OverlayLayer]
---

# Overlays

Additive, read-only decorations. Specs ride `Request.Overlays`, layers `Response.Overlays[i]` in slot order. Overlays NEVER mutate the base — siblings keyed to host coordinates. Use for derived projections (share, index, delta, z, χ², p); base aggregations stay `AGG_*`.

```jsonc
{"overlays":[{"kind":"OVERLAY_SHARE_OF_ROW","scope":"cell","ref":{"margin":{"axis":"row"}}}]}
```

## OverlaySpec composition

`Kind`, `Scope`, `Ref`, optional `Name`/`Level`/`Within`/`Params`; Compose-only `Reference`/`Targets`. Predict and runtime reject misshape under the SAME code (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `_LEVEL_OUT_OF_RANGE`, `_SCOPE_UNSUPPORTED`): a fault raises its real `PULSE_OVERLAY_*` as `errors[0].code`, never `PROCESSING_INTERNAL` with a `details.code` echo, so `pulse errors lookup` resolves it — `PROCESSING_INTERNAL` covers caller-side invariant breaks (nil spec/host). `Scope` ∈ `cell|row|column|group|matrix|total`. `Ref` is a discriminated union over six families, exactly one populated. `Level`/`Within` mirror normalize.

## Six reference families

`Margin{Axis}` (share + margin index/delta/z), `Sibling{Field,Value}` (SERIES), `BaselineIndex{Position}` (windowed), `Population{Cohort}` + `Prior`/`RollingMean`/`YoY` (FACET + self-compare), `Stage{Index|Name}` (chain), `Reference`+`Targets` (Compose). Implicit-margin kinds (χ², Fisher) leave `Ref` empty.

## Three payload shapes

`Payload.Shape` ∈ `scalar|series|matrix`. Scalar — `Payload.Scalar` + optional `OverlaySummary{Statistic,PValue,Parameters}`. Series — `Payload.Series.Entries[i].{Key,Value,Summary}` aligned to host keys. Matrix — `Payload.Matrix.Cells[r][c]` mirrors host cells. `Baseline` is the centerpoint (100 index, 0 delta/z), absent for inferential kinds.

## Catalog

Authoritative list + count: `types.AllOverlayKinds()` / `pulse_manifest.overlays`; never hardcode; per-kind math in atomics. Families (drop `OVERLAY_`):

Share (`SHARE_OF_ROW`/`_COL`/`_TOTAL`); margin compare (`INDEX_`/`DELTA_`/`ZSCORE_VS_MARGIN`, `*_VS_TOTAL`, `RANK`); matrix inferential (`CHISQ_*`, `FISHER_EXACT_CELL`, `PROP_Z_CELL`, `{T,Z}_CELL`); intra-matrix pairwise (`PAIRWISE_PROP_Z`/`_PROBIT_T`/`_WELCH_T`/`_TWO_MEANS_Z`, shared `n_source`/`p_source` vocabulary: `pairwise-n-sources`). **Both pairwise surfaces — the MATRIX family and the Compose-host `PROP_Z_PANEL` — now share ONE distinct-key n vocabulary**: the same admitted cell aggregators (`AGG_DISTINCT_SUM` at `distinct_count`, `AGG_DISTINCT_COUNT` at `cardinality`), the same exact-identity admission, the same null rules, the same `PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`. **Where they differ is the SPELLINGS, and that is deliberate**: after the panel's rename the two hosts share NO within-prefix mode name at all. `n_within` / `n_within_distinct` are the MATRIX family's (a PAIR-axis slab at one fixed opposite index); `row_margin_value_within` / `row_margin_distinct_within` are the panel's (a slot's ROW margins, all columns). They differ by roughly the column count, silently, so each is an UNKNOWN mode on the other host. The panel also refuses a panel whose slots name DIFFERENT admitted aggregators — one host has one cell aggregator, a panel has N+1; SERIES self-compare (baseline / sibling / prior / `YOY` / rolling); FACET population (`*_VS_POP`); Compose vs-ref + panel (`*_VS_REF`, `PROP_Z_PANEL`, `PANEL_INDEX_VS_REF`); chain (`*_VS_STAGE`); `FORMULA`.

## Host-arm wiring

- **MATRIX (Crosstab)** — `applyOverlaysToResponse` (`processing/crosstab.go`), from BOTH the buffered and fused exits. The distinct-key slab partition refusal sits here, twinned with predict's. A direct `processing.ApplyOverlaysWithExtensions` caller bypasses BOTH — accepted; that entry is for embedders who own their host (`pairwise-n-sources`).
- **SERIES (windowed Process)** — `service/series_overlay.go` per-group fold.
- **FACET** — `service.applyFacetOverlays` at the buffered exit; `Ref.Population` recursion builds the comparison FacetResult.
- **CHAIN** — `service.applyChainOverlays` post-stage; per-stage `Stages[i].Overlays` untouched, whole-chain on `ChainResponse.Overlays`. Divergent shape ⇒ `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT`.
- **FORMULA** — expr-lang over earlier layers; refs resolve by `Name`.
- **COMPOSE** — post-slot fold (`service/compose_overlay.go`) gates slot-label, key alignment, schema and dict drift; `DictPrefixFast` ⇒ prefix probe. Handlers may ALSO read a slot's `Components.Crosstab` via `processing.ComposeHostView`/`ComposeSlotView` — opt-in, four-valued `State()` (slot-absent / disabled / non-crosstab / present) so a misconfiguration is not reported as absent data. Only `OVERLAY_PROP_Z_PANEL` reads it today (`n_source: cell_n_unweighted`, `row_margin_distinct_within`). The panel's slab-partition and distinct-key gates are twinned in `descriptor.ValidateCompose`; its cell-aggregator ADMISSION is runtime-only, like the MATRIX arm's.

## Per-layer warnings (`OverlayLayer.Warnings`)

Additive `[]OverlayWarning` slot (`omitempty`); empty/nil elides the key, so overlay-free responses stay byte-identical (`Test*_OverlayFreeByteIdentical`). Each entry carries `Code`, `Message`, `Details map[string]any`; canonical `PULSE_OVERLAY_REF_ZERO` + siblings.

Routing is dispatcher-stamped, service-distributed: the chain / Compose dispatchers (`processing/overlay_*_dispatch.go`) stamp `Details["overlay_index"] = i`; `service.applyChainOverlays` / `applyComposeOverlays` route each to `out.Overlays[idx].Warnings` (none ⇒ `nil`; missing key ⇒ layer 0). The Compose-host barrier rides the same slot on `ComposedResponse.Overlays[i]`.

## Streamability

`types.OverlayStreamability` — one row per kind. Descriptive SERIES stream, inferential (χ²/KS/Fisher/parity/Welch) buffer. Every MATRIX-host kind is `false`: the crosstab fold runs AFTER the matrix is finalised, not in-pass.

That flag does NOT pick the crosstab's execution path. **`Request.Overlays` no longer forces buffered** — `CanFuseCrosstab` ignores the slot; `RunCrosstabFused` folds at its exit through the same hook. The only reason such a crosstab buffers is the CELL AGGREGATOR: `AGG_WELFORD` is non-mergeable, so the two kinds reading its triple stay buffered. `OVERLAY_PAIRWISE_PROP_Z` over `AGG_WEIGHTED_MEAN` fuses.

## Parity overlays — Welford migration

The four parity kinds (`OVERLAY_{T,Z}_CELL`, `OVERLAY_{T,Z}_VS_REF`) read `{n, mean, variance}` from `CellComponents[r][c]` (`AGG_WELFORD`), falling back to `Params` when absent. The legacy `WelfordTriple` smuggle through `MatrixCell.Value` is **removed in v0.20.0**; that slot carries the mean. P-values byte-equal `TEST_WELCH`.

## Adding a new kind

Declare the constant + `AllOverlayKinds()`; add the `overlay_streamability.go` row; handler in `processing/overlay_*.go`; register in host dispatch; predict validator (`descriptor/overlay_*.go`); raise faults under the kind's own code; ship its atomic.

## See

- `response-components`, `crosstab-guide` (MATRIX host), `facet-design`, `pairwise-n-sources`, `docs/src/internals/extension-points.md`.
