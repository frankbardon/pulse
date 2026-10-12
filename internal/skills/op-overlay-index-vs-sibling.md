---
name: op-overlay-index-vs-sibling
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_SIBLING
description: Per-group ratio index against a sibling group named by (Field, Value) on the SERIES host (×100).
type: reference
applies_to: process, compose
examples_tags: [overlay, comparison]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `group`. `Ref.Sibling.Field` (string, required) — grouper field on the host. `Ref.Sibling.Value` (string, required) — axis-key value identifying the sibling group. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES — grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment; the sibling resolves inside each partition. Ratio twin of the sibling delta.

## Output

SERIES — one `SeriesEntry` per host group, carrying `index = group / sibling × 100` on `Summary.Statistic`. Sibling group itself emits `100.0` (self-vs-self). Layer `Baseline = 100`.

## Gotchas

- Unknown `(Field, Value)` pair → ONE `PULSE_OVERLAY_REF_UNKNOWN` per layer + NaN entries.
- Zero sibling value → NaN entries + ONE `PULSE_OVERLAY_REF_ZERO` per layer. The subtractive twin does not warn on zero.
- Absent host group → `SeriesEntry` with unset `Statistic`.
- Buffered — the resolver needs materialised per-group accumulators (`ApplyOverlaysSeries`).
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: `overlay-system`, `op-overlay-delta-vs-sibling`, `op-overlay-index-vs-total`.
