```yaml
name: op-overlay-delta-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_REF
description: Compose-host additive delta of target slot value against the matching reference slot value (matrix or series).
type: reference
applies_to: compose
examples_tags: [overlay, compose, before-after]
```

Compose-only dual-shape. Overlays decorate the host; no `Response.Components`.

## Use when

Each cell or group of a target Compose request minus the same coordinate in the reference request, in the target's own units.

Questions it answers:

- How many points did each segment's score move since last wave?
- How much more or less did each region sell this year than last?

Use something else:

- `OVERLAY_INDEX_VS_REF` when you want a ratio rather than a gap.
- `OVERLAY_T_CELL` when you want a test of whether the means differ.

## Params

`Scope` (enum, required) — `cell` (matrix host) or `group` (series host). `Reference` (string, required) — reference slot label. `Targets` ([]string, required) — target slot labels (one or more). Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES grouped Process on reference + target. Schema-match, key-alignment and dict-prefix gates run at the slot barrier. Subtractive twin of the reference index.

## Output

MATRIX (cell host) or SERIES (group host) — per-coordinate `delta = target - ref`. Preserves target slot's units. Layer `Baseline = 0`.

## Gotchas

- No division — zero reference never raises `PULSE_OVERLAY_REF_ZERO` (mirrors per-Request DELTA family).
- Target key absent from the reference → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`, entry NaN.
- SERIES dispatch is fold-only (one accumulator per group), streamable per `OverlayStreamability`; MATRIX is forced buffered by the slot barrier.

## See

- Skills: [`overlay-system`](overlay-system.md), [`compose-requests`](compose-requests.md), [`op-overlay-index-vs-ref`](op-overlay-index-vs-ref.md).
