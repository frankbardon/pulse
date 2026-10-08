```yaml
name: op-overlay-index-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_REF
description: Compose-host ratio index of target slot value against the matching reference slot value (matrix or series).
type: reference
applies_to: compose
examples_tags: [overlay, compose, comparison]
```

Compose-only dual-shape. Overlays decorate the host; no `Response.Components`.

## Use when

Index value of each cell or group of a target Compose request against the same spot in the reference request (target / reference x 100).

Questions it answers:

- How does this wave's result per segment compare with last wave's, scaled to 100?
- How does each region's revenue this year compare with last year's?

Use something else:

- `OVERLAY_DELTA_VS_REF` when you want the gap in the value's own units.

## Params

`Scope` required — `cell` (matrix host) or `group` (series host). `Reference` / `Targets` required slot labels (one or more targets). `params.scale` (float, default `100`) — set `1` for a raw ratio.

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES grouped Process on reference + target. Schema-match + key-alignment + dict-prefix gates at the slot barrier. Ratio twin of the reference delta.

## Output

MATRIX (cell host) or SERIES (group host) — per-coordinate `(target / ref) × scale`. Layer `Baseline = scale` (default `100`).

## Gotchas

- Zero reference → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per coord.
- Missing reference coordinate → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`.
- SERIES dispatch is fold-only (streamable per `OverlayStreamability`); MATRIX is forced buffered by the slot barrier.
- `OverlayOptions.DictPrefixFast` enables the byte-equal dictionary prefix probe. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Weighted slots → weighted figures (reads the slot payloads).

## See

- Skills: [`overlay-system`](overlay-system.md), [`compose-requests`](compose-requests.md), [`op-overlay-delta-vs-ref`](op-overlay-delta-vs-ref.md), [`op-overlay-panel-index-vs-ref`](op-overlay-panel-index-vs-ref.md).
