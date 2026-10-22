---
name: op-overlay-delta-vs-sibling
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_SIBLING
description: Per-group additive delta against a sibling group named by (Field, Value) on the SERIES host.
type: reference
applies_to: process
examples_tags: [overlay, comparison, before-after]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |

`Scope` must be `group`. `Ref.Sibling.Field` (string, required) — grouper field on the host. `Ref.Sibling.Value` (string, required) — axis-key value identifying the sibling group. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

SERIES — grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment; the sibling resolves inside each partition. Subtractive twin of the sibling index.

## Output

SERIES — one `SeriesEntry` per host group, carrying `delta = group - sibling` on `Summary.Statistic`. Sibling group itself emits `0.0` (self-vs-self). Layer `Baseline = 0`.

<!-- generated: contract -->

## Gotchas

- Unknown `(Field, Value)` pair → ONE `PULSE_OVERLAY_REF_UNKNOWN` per layer + NaN across entries.
- Zero sibling → no warning (subtraction is defined; delta becomes the raw group value), unlike the ratio twin.
- Absent host group → `SeriesEntry` with unset `Statistic`.
- Buffered — the sibling resolver needs materialised per-group accumulators (`ApplyOverlaysSeries`).

## See

- Skills: `overlay-system`, `op-overlay-index-vs-sibling`, `op-overlay-delta-vs-margin`.
