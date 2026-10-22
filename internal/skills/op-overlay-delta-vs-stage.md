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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score on the target and the reference stage (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = each stage's `aggregations[0]`; rows key on group columns only; a stage lacking it → `PULSE_OVERLAY_FIELD_UNKNOWN` (`details.which` / `stage_index`). |

`Scope` required, echoed verbatim — the capability declares `total` (there is no `chain` scope). `ChainOverlaySpec` has no `Level` / `Within`. `Ref` (a `StageRef`) required — `{Index: N}` or `{Name: "stage-id"}`. `Target` — `{Index}` or `{Name}`, default the latest stage.

## Host shape

CHAIN — `ProcessChain` with reference + target stage's host result shape (scalar / series / matrix). Subtractive twin of the stage index.

## Output

Shape inherited from target stage. Per-coordinate `delta = target_val - ref_val`, in the target stage's own units. Layer `Baseline = 0`.

<!-- generated: contract -->

## Gotchas

- Series host: an undefined value (NaN — a mean over a group with no value, an unanswered element) on either side is a MISSING point, as on the request host — the entry keeps its key with NO `statistic` (absent, not `null`), no warning, out of `summary.count`.
- No division — zero reference never raises `PULSE_OVERLAY_REF_ZERO`. Distinct from the ratio twin.
- Target shape ≠ ref shape → `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` WARNING on predict-chain and at runtime (chain stays valid and runs; empty layer).
- Unknown stage → `PULSE_OVERLAY_REF_UNKNOWN` (`PULSE_OVERLAY_TARGET_UNKNOWN` when it lands).
- Buffered (whole-chain barrier runs after every stage finalises by construction).

## See

- Skills: `overlay-system`, `process-chain`, `op-overlay-index-vs-stage`.
