```yaml
name: op-overlay-index-vs-stage
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_STAGE
description: Whole-chain ratio index of target stage's result against the reference stage's result (×100).
type: reference
applies_to: process
examples_tags: [overlay, comparison]
```

Lives on `ChainRequest.Overlays` (dual-slot host — `ChainOverlaySpec`). Decorates `ChainResponse.Overlays`; per-stage overlays untouched. Overlays emit no `Response.Components`.

## Use when

Index value of a process-chain stage's result against an earlier stage's result at the same coordinate (target / reference x 100).

Questions it answers:

- What share of the starting figure is left after each filtering stage, scaled to 100?
- How does the refined stage's result compare with the first stage's?

Use something else:

- `OVERLAY_DELTA_VS_STAGE` when you want the gap in the value's own units.

## Params

`Scope` required, echoed verbatim — the capability declares `total` (there is no `chain` scope). `ChainOverlaySpec` has no `Level` / `Within`. `Ref` (a `StageRef`) required — `{Index: N}` or `{Name: "stage-id"}`. `Target` — `{Index}` or `{Name}`, default the latest stage.

## Host shape

CHAIN — `ProcessChain`; reference + target stages' host shape (scalar / series / matrix). Consumes the `StageRef` reference family.

## Output

Shape inherited from target stage. Per-coordinate `index = target / ref × 100`. Layer `Baseline = 100`.

## Gotchas

- Zero `ref_val_k` → NaN at that coordinate + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- `Target` defaults to the latest stage when `Index` is nil and `Name` empty; `Ref` has no default.
- Stage shape divergence → `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` (runtime only; predict cannot catch it).
- Unknown stage → `PULSE_OVERLAY_REF_UNKNOWN` (`PULSE_OVERLAY_TARGET_UNKNOWN` when it lands).
- Buffered (whole-chain barrier post stage loop).

## See

- Skills: [`overlay-system`](overlay-system.md), [`process-chain`](process-chain.md), [`op-overlay-delta-vs-stage`](op-overlay-delta-vs-stage.md).
