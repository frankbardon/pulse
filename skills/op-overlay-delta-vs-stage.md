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

Lives on `ChainRequest.Overlays` (dual-slot host — `ChainOverlaySpec`). Decorates `ChainResponse.Overlays`; per-stage overlays are independent. Overlays do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `chain`. |
| `Ref.Stage` | object | (required) | `{Index: N}` or `{Name: "stage-id"}`. |
| `Target.Stage` | object | latest stage | `{Index}` or `{Name}`. |

## Host shape

CHAIN — `ProcessChain` with reference + target stage's host result shape (scalar / series / matrix). Subtractive twin of `OVERLAY_INDEX_VS_STAGE`.

## Output

Shape inherited from target stage. Per-coordinate `delta = target_val - ref_val`. Preserves target stage's units — a $-valued aggregator yields a $-valued delta in the same currency. Layer `Baseline = 0`.

## Gotchas

- No division — zero reference never raises `PULSE_OVERLAY_REF_ZERO`. Distinct from `OVERLAY_INDEX_VS_STAGE`.
- Stage shape divergence (target shape ≠ ref shape) → `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` + NaN across coordinates.
- Unknown stage → `PULSE_OVERLAY_REF_UNKNOWN` (and `PULSE_OVERLAY_TARGET_UNKNOWN` when those codes land).
- Scope MUST be `chain`. `Level` / `Within` MUST be zero.
- Buffered (whole-chain barrier runs after every stage finalises by construction).

## See

- Skills: `overlay-system`, `contributor-workflow`, `op-overlay-index-vs-stage`.
