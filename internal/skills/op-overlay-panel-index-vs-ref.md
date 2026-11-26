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

`scope` required — `cell` (matrix host) or `group` (SERIES host). `reference` = shared reference slot. `targets` — one layer per target. `options.max_panel_targets` (default `16`) caps the target count.

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES on reference and every target. Every (reference, target) pair must match on schema, keys and dictionaries. Sibling to the proportion-z panel.

## Output

ONE layer per target, named `<name>__<target>` (no `name`: `OVERLAY_PANEL_INDEX_VS_REF__<target>`), in `targets` order. Payload shape mirrors the reference slot's; each layer equals the reference index on that `(reference, target)` pair.

<!-- generated: contract -->

## Gotchas

- An undefined value (NaN — a mean over a group with no value, an unanswered element) on either side is MISSING, as on the request host: series — the entry keeps its key with NO `statistic` (absent, not `null`); matrix — the cell stays absent; no warning, out of `summary.count`.
- More targets than `options.max_panel_targets` → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- The SERIES form is streamable (manifest `buffered: false`); the MATRIX form buffers.
- Weighted slots → weighted figures (reads the slot payloads).
<!-- feature: AGG_MODE -->- A code cell (`AGG_MODE` over `categorical_*` / `packed_bool`) on either slot → `PROCESSING_CONFIG`, reason `categorical_code`; use a count cell.<!-- /feature -->

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-index-vs-ref`, `op-overlay-prop-z-panel`.
