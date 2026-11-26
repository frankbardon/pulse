---
name: op-overlay-index-vs-sibling
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_SIBLING
description: Per-group ratio index against a sibling group named by (field, value) on the SERIES host (×100).
type: reference
applies_to: process
examples_tags: [overlay, comparison]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |

`scope` must be `group`. `ref.sibling.field` (string, required) — grouper field on the host. `ref.sibling.value` (string, required) — axis-key value identifying the sibling group. Other `ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

SERIES — grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment; the sibling resolves inside each partition. Ratio twin of the sibling delta.

## Output

SERIES — one entry per host group, `summary.statistic` = `group / sibling × 100` (the sibling itself: `100`). Layer `summary.baseline` = `100`.

<!-- generated: contract -->

## Gotchas

- Unknown `(field, value)` pair → ONE `PULSE_OVERLAY_REF_UNKNOWN` per layer, `null` entries.
- Zero sibling value → `null` entries + ONE `PULSE_OVERLAY_REF_ZERO` per layer. The subtractive twin does not warn on zero.
- Absent host group → its entry keeps the key, no `statistic`.
- Buffered — the sibling resolves after every group finalizes.
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: `overlay-system`, `op-overlay-delta-vs-sibling`, `op-overlay-index-vs-total`.
