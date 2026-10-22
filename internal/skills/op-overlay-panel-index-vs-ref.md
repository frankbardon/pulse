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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score on every slot (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = each slot's `aggregations[0]`; rows key on group columns only; a slot lacking it → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `scale` | float | `100` | Multiplier on target / reference; 1 gives a raw ratio. |

`Scope` required — `cell` (matrix host) or `group` (series host). `Reference` = shared reference slot. `Targets` — one layer per target. `OverlayOptions.MaxPanelTargets` int, default `16`, caps `len(Targets)`.

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES on reference and every target. Schema-match + key-alignment + dict-prefix gates per (ref, target). Sibling to the proportion-z panel.

## Output

ONE layer per target, `layers[i].Name = "<spec.Name>__<spec.Targets[i]>"`. Payload shape mirrors the reference slot's. Byte-equal per layer to the two-slot reference index on each `(reference, target[i])`.

<!-- generated: contract -->

## Gotchas

- An undefined value (NaN — a mean over a group with no value, an unanswered element) on either side is MISSING, as on the request host: series — the entry keeps its key with NO `statistic` (absent, not `null`); matrix — the cell stays absent; no warning, out of `summary.count`.
- `len(Targets) > MaxPanelTargets` → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- Empty `spec.Name` → label becomes `OVERLAY_PANEL_INDEX_VS_REF__<target_label>`.
- Streamable: SERIES fold-only; MATRIX forced buffered by the slot barrier.
- Shared coord space is enforced before dispatch.
- Layer slice order matches `spec.Targets`, stable across re-runs.
- Weighted slots → weighted figures (reads the slot payloads).

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-index-vs-ref`, `op-overlay-prop-z-panel`.
