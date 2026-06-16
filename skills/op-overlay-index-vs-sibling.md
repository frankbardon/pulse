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

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.Sibling.Field` | string | (required) | Grouper field on the host. |
| `Ref.Sibling.Value` | string | (required) | Axis-key value identifying the sibling group. |

## Host shape

SERIES — grouped Process host. Family: sibling reference (`Ref.Sibling`). Ratio twin of `OVERLAY_DELTA_VS_SIBLING`. Sibling resolved via `processing/overlay_sibling_resolver.go`.

## Output

SERIES — one `SeriesEntry` per host group, carrying `index = group / sibling × 100` on `Summary.Statistic`. Sibling group itself emits `100.0` (self-vs-self). Layer `Baseline = 100`.

## Gotchas

- Unknown `(Field, Value)` pair → ONE `PULSE_OVERLAY_REF_UNKNOWN` warning per layer + NaN entries.
- Zero sibling value → NaN entries + ONE `PULSE_OVERLAY_REF_ZERO` per layer. Distinct from `OVERLAY_DELTA_VS_SIBLING` (no warning on zero — subtraction defined).
- Absent host group → `SeriesEntry` with unset `Statistic`.
- Both `Field` and `Value` MUST be non-empty. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered — sibling resolver requires materialised per-group accumulators (`ApplyOverlaysSeries`).

## See

- Skills: `overlay-system`, `op-overlay-delta-vs-sibling`, `op-overlay-index-vs-total`.
