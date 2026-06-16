---
name: op-overlay-delta-vs-baseline
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_BASELINE
description: Per-point additive delta against a fixed positional baseline of an ordered SERIES host.
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, before-after]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.BaselineIndex.Position` | int | (required) | `>= 0`; positional anchor in host order. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

SERIES — ordered grouped Process host (e.g. `GROUP_DATE`). Family: positional baseline (`Ref.BaselineIndex`). Subtractive twin of `OVERLAY_INDEX_VS_BASELINE`.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `delta = point - baseline` on `Summary.Statistic`. Baseline ordinal itself emits `0.0` (self-vs-self). Layer `Baseline = 0` (delta family centerpoint).

## Gotchas

- Out-of-range `Position` → `PULSE_OVERLAY_REF_UNKNOWN` (predict + runtime via `ResolveBaselineIndex`).
- Zero baseline → no warning (subtraction defined for every finite value; delta becomes raw host value). Distinct from `OVERLAY_INDEX_VS_BASELINE` which raises `PULSE_OVERLAY_REF_ZERO`.
- Absent host point → `SeriesEntry` with unset `Statistic` (canonical absent-slot shape).
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered — `host.ValueAt(Position)` consulted post-finalize via `ApplyOverlaysSeries`.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-baseline`, `op-overlay-delta-vs-margin`.
