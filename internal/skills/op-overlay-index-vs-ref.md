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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score on every slot (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = each slot's `aggregations[0]`; rows key on group columns only; a slot lacking it → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `scale` | float | `100` | Set `1` for a raw ratio. |

`scope` required — `cell` (matrix host) or `group` (series host). `reference` / `targets` required slot labels (one or more targets).

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES grouped Process on reference + target. Slots must match on schema, keys and dictionaries. Ratio twin of the reference delta.

## Output

MATRIX (cell host) or SERIES (group host) — per-coordinate `(target / reference) × scale`. Layer `summary.baseline` = `scale`. One layer per target, in `targets` order: one target keeps `name`; two or more are `<name>__<target>`, each the one-target layer. Over `options.max_panel_targets` (16) → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.

<!-- generated: contract -->

## Gotchas

- An undefined value (NaN — a mean over a group with no value, an unanswered element) on either side is MISSING, as on the request host: series — the entry keeps its key with NO `statistic` (absent, not `null`); matrix — the cell stays absent; no warning, out of `summary.count`.
- Zero reference → `null` + ONE `PULSE_OVERLAY_REF_ZERO` per coordinate.
- Missing reference coordinate → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`.
- The SERIES form is streamable (manifest `buffered: false`); the MATRIX form buffers.
- `options.dict_prefix_fast` enables the dictionary-prefix probe. A `ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Weighted slots → weighted figures (reads the slot payloads).
<!-- feature: AGG_MODE -->- A code cell (`AGG_MODE` over `categorical_*` / `packed_bool`) on either slot → `PROCESSING_CONFIG`, reason `categorical_code`; use a count cell.<!-- /feature -->

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-delta-vs-ref`, `op-overlay-panel-index-vs-ref`.
