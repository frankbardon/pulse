---
name: op-overlay-delta-vs-sibling
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_SIBLING
description: Per-group additive delta against a sibling group named by (Field, Value) on the SERIES host.
type: reference
applies_to: process, compose
examples_tags: [overlay, comparison, before-after]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.Sibling.Field` | string | (required) | Grouper field on the host. |
| `Ref.Sibling.Value` | string | (required) | Axis-key value identifying the sibling group. |

## Host shape

SERIES — grouped Process host. Family: sibling reference (`Ref.Sibling`). Subtractive twin of `OVERLAY_INDEX_VS_SIBLING`. Sibling resolved via `processing/overlay_sibling_resolver.go`.

## Output

SERIES — one `SeriesEntry` per host group, carrying `delta = group - sibling` on `Summary.Statistic`. Sibling group itself emits `0.0` (self-vs-self). Layer `Baseline = 0`.

## Gotchas

- Unknown `(Field, Value)` pair → ONE `PULSE_OVERLAY_REF_UNKNOWN` warning per layer + NaN across entries.
- Zero sibling value → no warning (subtraction defined; delta becomes raw group value). Distinct from `OVERLAY_INDEX_VS_SIBLING` which raises `PULSE_OVERLAY_REF_ZERO`.
- Absent host group → `SeriesEntry` with unset `Statistic`.
- Both `Field` and `Value` MUST be non-empty. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered — sibling resolver requires materialised per-group accumulators (`ApplyOverlaysSeries`).

## See

- Skills: `overlay-system`, `op-overlay-index-vs-sibling`, `op-overlay-delta-vs-margin`.
