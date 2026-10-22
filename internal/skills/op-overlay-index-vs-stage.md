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

Lives on `ChainRequest.Overlays` (dual-slot host — `ChainOverlaySpec`). Decorates `ChainResponse.Overlays`; per-stage overlays untouched. Overlays emit no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score on the target and the reference stage (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = each stage's `aggregations[0]`; rows key on group columns only; a stage lacking it → `PULSE_OVERLAY_FIELD_UNKNOWN` (`details.which` / `stage_index`). |

`Scope` required, echoed verbatim — the capability declares `total` (there is no `chain` scope). `ChainOverlaySpec` has no `Level` / `Within`. `Ref` (a `StageRef`) required — `{Index: N}` or `{Name: "stage-id"}`. `Target` — `{Index}` or `{Name}`, default the latest stage.

## Host shape

CHAIN — `ProcessChain`; reference + target stages' host shape (scalar / series / matrix). Consumes the `StageRef` reference family.

## Output

Shape inherited from target stage. Per-coordinate `index = target / ref × 100`. Layer `Baseline = 100`.

<!-- generated: contract -->

## Gotchas

- Series host: an undefined value (NaN — a mean over a group with no value, an unanswered element) on either side is a MISSING point, as on the request host — the entry keeps its key with NO `statistic` (absent, not `null`), no warning, out of `summary.count`.
- Zero `ref_val_k` → NaN at that coordinate + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- `Target` defaults to the latest stage when `Index` is nil and `Name` empty; `Ref` has no default.
- Stage shape divergence → `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` WARNING on predict-chain and at runtime (chain stays valid and runs; empty layer).
- Unknown stage → `PULSE_OVERLAY_REF_UNKNOWN` (`PULSE_OVERLAY_TARGET_UNKNOWN` when it lands).
- Buffered (whole-chain barrier post stage loop).

## See

- Skills: `overlay-system`, `process-chain`, `op-overlay-delta-vs-stage`.
