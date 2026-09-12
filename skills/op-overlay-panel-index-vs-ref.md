---
name: op-overlay-panel-index-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_PANEL_INDEX_VS_REF
description: Compose-host multi-reference index — indexes every target slot against a shared reference; emits one layer per target.
type: reference
applies_to: compose
examples_tags: [overlay, compose, comparison]
---

Compose-only multi-reference. Overlays decorate the host; no `Response.Components`.

## Params

`Scope` required — `cell` (matrix host) or `group` (series host). `Reference` = shared reference slot. `Targets` — one layer per target. `OverlayOptions.MaxPanelTargets` int, default `16`, caps `len(Targets)`.

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES on reference and every target. Schema-match + key-alignment + dict-prefix gates per (ref, target). Sibling to `OVERLAY_PROP_Z_PANEL`.

## Output

ONE layer per target, `layers[i].Name = "<spec.Name>__<spec.Targets[i]>"`. Payload shape mirrors the reference slot's. Byte-equal per layer to `OVERLAY_INDEX_VS_REF` on each `(reference, target[i])`.

## Gotchas

- `len(Targets) > MaxPanelTargets` → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- Empty `spec.Name` → label becomes `OVERLAY_PANEL_INDEX_VS_REF__<target_label>`.
- Streamable: SERIES fold-only; MATRIX forced buffered by the slot barrier.
- Shared coord space is enforced before dispatch.
- Layer slice order matches `spec.Targets`, stable across re-runs.

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-index-vs-ref`, `op-overlay-prop-z-panel`.
