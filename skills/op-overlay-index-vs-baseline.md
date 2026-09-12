---
name: op-overlay-index-vs-baseline
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_BASELINE
description: Per-point ratio index against a fixed positional baseline of an ordered SERIES host (×100).
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, comparison]
---

Overlays decorate the host; no `Response.Components`.

## Params

`Scope` must be `group`. `Ref.BaselineIndex.Position` (int, required) — `>= 0`; positional anchor in host order. `Level`/`Within` must be `0`. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

SERIES — ordered grouped Process host (e.g. `GROUP_DATE`). Ratio twin of `OVERLAY_DELTA_VS_BASELINE`.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `index = point / baseline × 100` on `Summary.Statistic`. Baseline ordinal itself emits `100.0` (self-vs-self). Layer `Baseline = 100`.

## Gotchas

- Out-of-range `Position` → `PULSE_OVERLAY_REF_UNKNOWN` (predict + runtime via `ResolveBaselineIndex`).
- Zero baseline → NaN across entries + ONE `PULSE_OVERLAY_REF_ZERO` per layer, unlike `OVERLAY_DELTA_VS_BASELINE`.
- Absent host point → `SeriesEntry` with unset `Statistic`.
- Absent baseline ordinal yields `0.0` from host → routes to zero-baseline arm.
- Buffered — `host.ValueAt(Position)` post-finalize via `ApplyOverlaysSeries`.

## See

- Skills: `overlay-system`, `op-overlay-delta-vs-baseline`, `op-overlay-index-vs-prior`.
