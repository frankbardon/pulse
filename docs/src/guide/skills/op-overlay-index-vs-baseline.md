```yaml
name: op-overlay-index-vs-baseline
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_BASELINE
description: Per-point ratio index against a fixed positional baseline of an ordered SERIES host (×100).
type: reference
applies_to: process
examples_tags: [overlay, time-series, comparison]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each point of an ordered series as an index value against a chosen baseline point (point / baseline x 100), e.g. growth since launch.

Questions it answers:

- How have monthly sales grown relative to the launch month?
- How does each wave's score compare with the first wave, scaled to 100?

Use something else:

- `OVERLAY_DELTA_VS_BASELINE` when you want the gap in the value's own units.

## Params

`Scope` must be `group`. `Ref.BaselineIndex.Position` (int, required) — `>= 0`; positional anchor in host order. `Level`/`Within` must be `0`. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES — ordered grouped Process host (e.g. a date grouper). Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment. Ratio twin of the baseline delta.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `index = point / baseline × 100` on `Summary.Statistic`. Baseline ordinal itself emits `100.0` (self-vs-self). Layer `Baseline = 100`.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- Out-of-range `Position` → `PULSE_OVERLAY_REF_UNKNOWN` (predict + runtime via `ResolveBaselineIndex`).
- Zero baseline → NaN across entries + ONE `PULSE_OVERLAY_REF_ZERO` per layer, unlike the subtractive twin.
- Absent host point → `SeriesEntry` with unset `Statistic`.
- Absent baseline ordinal yields `0.0` from host → routes to zero-baseline arm.
- Buffered — `host.ValueAt(Position)` post-finalize via `ApplyOverlaysSeries`.
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-delta-vs-baseline`](op-overlay-delta-vs-baseline.md), [`op-overlay-index-vs-prior`](op-overlay-index-vs-prior.md).
