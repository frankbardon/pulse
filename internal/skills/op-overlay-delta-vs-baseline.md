---
name: op-overlay-delta-vs-baseline
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_BASELINE
description: Per-point additive delta against a fixed positional baseline of an ordered SERIES host.
type: reference
applies_to: process
examples_tags: [overlay, time-series, before-after]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |

`scope` must be `group`. `ref.baseline_index.position` (int, required) — `>= 0`; positional anchor in host order. `level`/`within` must be `0`. Other `ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

Ordered SERIES host (e.g. a date grouper). Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment. Subtractive twin of the baseline index.

## Output

SERIES — one entry per host group key in host order, `summary.statistic` = `point − baseline` (the baseline point itself: `0`). Layer `summary.baseline` = `0`.

<!-- generated: contract -->

## Gotchas

- Out-of-range `position` → `PULSE_OVERLAY_REF_UNKNOWN`. Predict refuses it when the series groups categorical fields by label (their dictionary sizes bound each partition); over any other grouper, or a partition missing a key, only the run knows the length.
- Zero baseline → no warning (subtraction defined for every finite value; delta becomes raw host value). The ratio twin raises `PULSE_OVERLAY_REF_ZERO` instead.
- Absent host point → its entry keeps the key, no `statistic`.
- Buffered: the baseline is read after the host finalizes.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-baseline`.
