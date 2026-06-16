---
name: op-overlay-delta-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_REF
description: Compose-host additive delta of target slot value against the matching reference slot value (matrix or series).
type: reference
applies_to: compose
examples_tags: [overlay, compose, before-after]
---

Compose-only dual-shape. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | `cell` (matrix host) or `group` (series host). |
| `Reference` | string | (required) | Reference slot label. |
| `Targets` | []string | (required) | Target slot labels (one or more). |

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES grouped Process on both reference + target. Schema-match, key-alignment, and dict-prefix gates run at the slot barrier. Subtractive twin of `OVERLAY_INDEX_VS_REF`.

## Output

MATRIX (cell host) or SERIES (group host) — per-coordinate `delta = target - ref`. Preserves target slot's units. Layer `Baseline = 0`.

## Gotchas

- No division — zero reference never raises `PULSE_OVERLAY_REF_ZERO` (mirrors per-Request DELTA family).
- Missing reference coordinates (target key not in reference) → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true` Detail flag; affected entry NaN.
- SERIES dispatch is fold-only (single accumulator per group) — streamable per `OverlayStreamability`.
- MATRIX dispatch forced buffered by the slot barrier.
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-index-vs-ref`, `op-overlay-delta-vs-margin`.
