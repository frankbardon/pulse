---
name: op-overlay-yoy
kind: operator
category: OVERLAY
operator: OVERLAY_YOY
description: Per-point year-over-year ratio against the same period one year prior; requires GROUP_DATE host.
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, comparison, trend-detection]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.YoY` | object | (empty marker) | Tags ref family. |
| `params.frequency` | string | conditional | `annual`/`quarterly`/`monthly`/`weekly`/`daily`/`hourly`. |

## Host shape

SERIES — grouped Process host whose single grouper is `GROUP_DATE`. Other grouper kinds → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. Reads `frequency` from `spec.Params` first, falls back to `req.Groups[0].Params["frequency"]`.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `yoy = point / prior × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- Strides: annual `-1`, quarterly `-4`, monthly `-12`, weekly `-52`. Daily/hourly: exact-key lookup via `Key(i).AddDate(-1,0,0)` / `Add(-365*24*time.Hour)`.
- Feb 29 in non-leap prior year → NaN (no exact-key match; non-goal: no leap-year realignment v1).
- Missing `frequency` → `PULSE_OVERLAY_YOY_FREQUENCY_MISSING`; unsupported → `PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY`.
- First year of data → NaN, no warning.
- Zero prior value → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- Buffered — per-frequency lookup over materialised host series.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-prior`, `op-overlay-index-vs-rolling-mean`.
