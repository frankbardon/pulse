---
name: op-overlay-index-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_PRIOR
description: Per-point streamable windowed index against the immediately preceding point of an ordered SERIES host (×100).
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, trend-detection, streaming-friendly]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.Prior` | object | (empty) | Implicit-default — empty `Ref` also accepted. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

SERIES — ordered grouped Process host. Family: windowed prior (`Ref.Prior`). First streamable windowed kind.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `index = point / prior × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- Single-state lag carrier (one `float64`) — streamable inside the streaming Process fold.
- First ordinal → NaN, no warning ("no comparison available" ≠ "denominator zero").
- Absent host point → NaN + carrier does NOT advance (next present point still divides by last present value).
- Zero prior → NaN + ONE `PULSE_OVERLAY_REF_ZERO` warning per layer.
- `Ref.Prior.Lag` reserved for future window-N priors; v1 ships lag-1 only.
- Empty `Ref` and populated `Ref.Prior` both spell lag-1.
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## See

- Skills: `overlay-system`, `op-overlay-yoy`, `op-overlay-index-vs-rolling-mean`.
