```yaml
name: op-overlay-delta-vs-stage
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_STAGE
description: Whole-chain additive delta of target stage's result against the reference stage's result.
type: reference
applies_to: process
examples_tags: [overlay, before-after]
```

Lives on `ChainRequest.Overlays` (dual-slot host — `ChainOverlaySpec`). Decorates `ChainResponse.Overlays`; per-stage overlays are independent. Overlays emit no `Response.Components`.

## Use when

A process-chain stage's result minus an earlier stage's result at the same coordinate, in the later stage's own units.

Questions it answers:

- How many records or how much revenue does each filtering stage remove?
- How far does the refined stage's figure move from the first stage's?

Use something else:

- `OVERLAY_INDEX_VS_STAGE` when you want a ratio rather than a gap.

## Params

`Scope` required, echoed verbatim — the capability declares `total` (there is no `chain` scope). `ChainOverlaySpec` has no `Level` / `Within`. `Ref` (a `StageRef`) required — `{Index: N}` or `{Name: "stage-id"}`. `Target` — `{Index}` or `{Name}`, default the latest stage. `params.field` (string, optional, series stages) — OUTPUT LABEL of the aggregation to score on the target and the reference stage (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = each stage's `aggregations[0]`; rows key on group columns only; a stage lacking it → `PULSE_OVERLAY_FIELD_UNKNOWN` (`details.which` / `stage_index`).

## Host shape

CHAIN — `ProcessChain` with reference + target stage's host result shape (scalar / series / matrix). Subtractive twin of the stage index.

## Output

Shape inherited from target stage. Per-coordinate `delta = target_val - ref_val`, in the target stage's own units. Layer `Baseline = 0`.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- Series host: an undefined value (NaN — a mean over a group with no value, an unanswered element) on either side is a MISSING point, as on the request host — the entry keeps its key with NO `statistic` (absent, not `null`), no warning, out of `summary.count`.
- No division — zero reference never raises `PULSE_OVERLAY_REF_ZERO`. Distinct from the ratio twin.
- Target shape ≠ ref shape → `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` WARNING on predict-chain and at runtime (chain stays valid and runs; empty layer).
- Unknown stage → `PULSE_OVERLAY_REF_UNKNOWN` (`PULSE_OVERLAY_TARGET_UNKNOWN` when it lands).
- Buffered (whole-chain barrier runs after every stage finalises by construction).

## See

- Skills: [`overlay-system`](overlay-system.md), [`process-chain`](process-chain.md), [`op-overlay-index-vs-stage`](op-overlay-index-vs-stage.md).
