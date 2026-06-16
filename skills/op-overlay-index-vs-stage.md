---
name: op-overlay-index-vs-stage
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_STAGE
description: Whole-chain ratio index of target stage's result against the reference stage's result (×100).
type: reference
applies_to: process
examples_tags: [overlay, comparison]
---

Lives on `ChainRequest.Overlays` (dual-slot host — `ChainOverlaySpec`). Decorates `ChainResponse.Overlays`; per-stage overlays untouched. Overlays do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `chain`. |
| `Ref.Stage` | object | (required) | `{Index: N}` or `{Name: "stage-id"}`. |
| `Target.Stage` | object | latest stage | `{Index}` or `{Name}`. |

## Host shape

CHAIN — `ProcessChain` with reference + target stages' host result shape (scalar / series / matrix). First whole-chain (E6) kind; first consumer of `StageRef` discriminated reference family.

## Output

Shape inherited from target stage. Per-coordinate `index = target / ref × 100`. Layer `Baseline = 100`.

## Gotchas

- Zero `ref_val_k` → NaN at that coordinate + ONE `PULSE_OVERLAY_REF_ZERO` warning per layer.
- Default `Target` = latest stage when both `Index` nil + `Name` empty (the "anchor against final result" authoring shape). `Ref` has no default.
- Stage shape divergence → `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` (runtime only — predict can't catch it).
- Unknown stage → `PULSE_OVERLAY_REF_UNKNOWN` (and `PULSE_OVERLAY_TARGET_UNKNOWN` when those codes land).
- Scope MUST be `chain`. `Level` / `Within` MUST be zero.
- Buffered (whole-chain barrier post stage loop).

## See

- Skills: `overlay-system`, `contributor-workflow`, `op-overlay-delta-vs-stage`.
