---
name: op-overlay-delta-vs-stage
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_STAGE
description: Whole-chain additive delta of target stage's result against the reference stage's result.
type: reference
applies_to: process
examples_tags: [overlay, before-after]
---

Lives on `ChainRequest.Overlays` (dual-slot host — `ChainOverlaySpec`). Decorates `ChainResponse.Overlays`; per-stage overlays are independent. Overlays emit no `Response.Components`.

## Params

`Scope` must be `chain`; `Level` / `Within` must be zero. `Ref.Stage` required — `{Index: N}` or `{Name: "stage-id"}`. `Target.Stage` — `{Index}` or `{Name}`, default the latest stage.

## Host shape

CHAIN — `ProcessChain` with reference + target stage's host result shape (scalar / series / matrix). Subtractive twin of `OVERLAY_INDEX_VS_STAGE`.

## Output

Shape inherited from target stage. Per-coordinate `delta = target_val - ref_val`, in the target stage's own units. Layer `Baseline = 0`.

## Gotchas

- No division — zero reference never raises `PULSE_OVERLAY_REF_ZERO`. Distinct from `OVERLAY_INDEX_VS_STAGE`.
- Target shape ≠ ref shape → `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` + NaN across coordinates.
- Unknown stage → `PULSE_OVERLAY_REF_UNKNOWN` (`PULSE_OVERLAY_TARGET_UNKNOWN` when it lands).
- Buffered (whole-chain barrier runs after every stage finalises by construction).

## See

- Skills: `overlay-system`, `process-chain`, `op-overlay-index-vs-stage`.
