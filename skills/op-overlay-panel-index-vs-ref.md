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

Compose-only multi-reference. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | `cell` (matrix host) or `group` (series host). |
| `Reference` | string | (required) | Shared reference slot label. |
| `Targets` | []string | (required) | One layer emitted per target. |
| `OverlayOptions.MaxPanelTargets` | int | `16` | Cap on `len(Targets)`. |

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES on reference + every target slot. Schema-match + key-alignment + dict-prefix gates per (ref, target). Sibling to `OVERLAY_PROP_Z_PANEL` (inferential).

## Output

ONE layer per target: `layers[i].Name = "<spec.Name>__<spec.Targets[i]>"`. Payload shape mirrors reference slot's host shape (MATRIX or SERIES). Mathematically equivalent to `OVERLAY_INDEX_VS_REF` for each `(reference, target[i])` pair — byte-equal per layer.

## Gotchas

- `len(Targets) > MaxPanelTargets` → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- Empty `spec.Name` → label degenerates to `OVERLAY_PANEL_INDEX_VS_REF__<target_label>`.
- Streamable — SERIES fold-only; MATRIX forced buffered by the slot barrier.
- Shared coord space enforced by schema-match + key-alignment gates before dispatch.
- Layer slice order matches `spec.Targets` order; stable across re-runs.

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-index-vs-ref`, `op-overlay-prop-z-panel`.
