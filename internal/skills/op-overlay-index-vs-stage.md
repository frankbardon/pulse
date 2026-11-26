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

Lives on a chain request's `overlays`; layers land on the chain response's `overlays`, per-stage overlays untouched. No `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score on the target and the reference stage (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = each stage's `aggregations[0]`; rows key on group columns only; a stage lacking it → `PULSE_OVERLAY_FIELD_UNKNOWN` (`details.which` / `stage_index`). |

`scope` required, echoed verbatim — the capability declares `total` (there is no `chain` scope). A chain overlay has no `level` / `within`. `ref` required — `{"index": N}` or `{"name": "stage-id"}`. `target` — same form, default the latest stage.

## Host shape

CHAIN — a process chain; the reference and target stages' result shape (scalar / series / matrix).

## Output

Shape inherited from target stage. Per-coordinate `target / reference × 100`. Layer `summary.baseline` = `100`.

<!-- generated: contract -->

## Gotchas

- SERIES host: an undefined value (NaN — a mean over a group with no value, an unanswered element) on either side is a MISSING point, as on the request host — the entry keeps its key with NO `statistic` (absent, not `null`), no warning, out of `summary.count`.
- Zero reference value → `null` at that coordinate + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- Stage shape divergence → `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` WARNING on predict-chain and at runtime (chain stays valid and runs; empty layer).
- An empty or unknown `ref` (no default) → `PULSE_OVERLAY_REFERENCE_UNKNOWN`; unknown `target` → `PULSE_OVERLAY_TARGET_UNKNOWN`.
- Buffered (runs after every stage finalizes).

## See

- Skills: `overlay-system`, `process-chain`, `op-overlay-delta-vs-stage`.
