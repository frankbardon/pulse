---
name: op-overlay-index-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_PRIOR
description: Per-point streamable windowed index against the immediately preceding point of an ordered SERIES host (×100).
type: reference
applies_to: process
examples_tags: [overlay, time-series, trend-detection, streaming-friendly]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `group`. `Ref.Prior` (object, empty) — implicit-default — empty `Ref` also accepted. `Level`/`Within` must be `0`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES — ordered grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment. Family: windowed prior (`Ref.Prior`).

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `index = point / prior × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- Single-state lag carrier (one `float64`) — streamable inside the streaming Process fold.
- NaN entries are `statistic: null` in JSON.
- First ordinal → NaN, no warning ("no comparison available" ≠ "denominator zero").
- Absent host point → no `statistic` key + carrier does NOT advance (next present point still divides by last present value).
- Zero prior → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- `Ref.Prior.Lag` reserved for future window-N priors; v1 ships lag-1 only.
- Empty `Ref` and populated `Ref.Prior` both spell lag-1.
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: `overlay-system`, `op-overlay-yoy`, `op-overlay-index-vs-rolling-mean`.
