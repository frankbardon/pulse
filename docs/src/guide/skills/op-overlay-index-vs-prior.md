```yaml
name: op-overlay-index-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_PRIOR
description: Per-point streamable windowed index against the immediately preceding point of an ordered SERIES host (×100).
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, trend-detection, streaming-friendly]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each point of an ordered series as an index value against the point before it (point / prior x 100): period-on-period change.

Questions it answers:

- By how much did sales grow or shrink from each month to the next?
- Is this week's volume above or below last week's, scaled to 100?

Use something else:

- `OVERLAY_DELTA_VS_PRIOR` when you want the change in the value's own units.

## Params

`Scope` must be `group`. `Ref.Prior` (object, empty) — implicit-default — empty `Ref` also accepted. `Level`/`Within` must be `0`.

## Host shape

SERIES — ordered grouped Process host. Family: windowed prior (`Ref.Prior`). First streamable windowed kind.

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

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-yoy`](op-overlay-yoy.md), [`op-overlay-index-vs-rolling-mean`](op-overlay-index-vs-rolling-mean.md).
