```yaml
name: op-overlay-delta-vs-baseline
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_BASELINE
description: Per-point additive delta against a fixed positional baseline of an ordered SERIES host.
type: reference
applies_to: process
examples_tags: [overlay, time-series, before-after]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each point of an ordered series minus a chosen baseline point, in the value's own units: how much it has moved since then.

Questions it answers:

- How many more orders a week do we get than in the launch week?
- How many points has satisfaction moved since the first wave?

Use something else:

- `OVERLAY_INDEX_VS_BASELINE` when you want a ratio rather than a gap.

## Params

`Scope` must be `group`. `Ref.BaselineIndex.Position` (int, required) — `>= 0`; positional anchor in host order. `Level`/`Within` must be `0`. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES — ordered grouped Process host (e.g. a date grouper). Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment. Subtractive twin of the baseline index.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `delta = point - baseline` on `Summary.Statistic`. Baseline ordinal itself emits `0.0` (self-vs-self). Layer `Baseline = 0` (delta family centerpoint).

## Gotchas

- Out-of-range `Position` → `PULSE_OVERLAY_REF_UNKNOWN` (predict + runtime via `ResolveBaselineIndex`).
- Zero baseline → no warning (subtraction defined for every finite value; delta becomes raw host value). The ratio twin raises `PULSE_OVERLAY_REF_ZERO` instead.
- Absent host point → `SeriesEntry` with unset `Statistic` (canonical absent-slot shape).
- Buffered — `host.ValueAt(Position)` read post-finalize via `ApplyOverlaysSeries`.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-index-vs-baseline`](op-overlay-index-vs-baseline.md).
