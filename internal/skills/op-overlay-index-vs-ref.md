---
name: op-overlay-index-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_REF
description: Compose-host ratio index of target slot value against the matching reference slot value (matrix or series).
type: reference
applies_to: compose
examples_tags: [overlay, compose, comparison]
---

Compose-only dual-shape. Overlays decorate the host; no `Response.Components`.

## Params

`Scope` required — `cell` (matrix host) or `group` (series host). `Reference` / `Targets` required slot labels (one or more targets). `params.scale` (float, default `100`) — set `1` for a raw ratio.

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES grouped Process on reference + target. Schema-match + key-alignment + dict-prefix gates at the slot barrier. Ratio twin of `OVERLAY_DELTA_VS_REF`.

## Output

MATRIX (cell host) or SERIES (group host) — per-coordinate `(target / ref) × scale`. Layer `Baseline = scale` (default `100`).

## Gotchas

- Zero reference → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per coord.
- Missing reference coordinate → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`.
- SERIES dispatch is fold-only (streamable per `OverlayStreamability`); MATRIX is forced buffered by the slot barrier.
- `OverlayOptions.DictPrefixFast` enables the byte-equal dictionary prefix probe. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-delta-vs-ref`, `op-overlay-panel-index-vs-ref`.
