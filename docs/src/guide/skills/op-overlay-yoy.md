```yaml
name: op-overlay-yoy
kind: operator
category: OVERLAY
operator: OVERLAY_YOY
description: Per-point year-over-year ratio against the same period one year prior; requires GROUP_DATE host.
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, comparison, trend-detection]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each period of a date-grouped series as an index value against the same period one year earlier (x 100): year-over-year change.

Questions it answers:

- How do this year's monthly sales compare with the same months last year?
- Is this quarter's volume up or down on the same quarter last year?

Use something else:

- `OVERLAY_INDEX_VS_PRIOR` when you want each period against the one before.

## Params

`Scope` must be `group`. `Ref.YoY` (empty marker) — tags ref family. `params.frequency` (string, conditional) — `annual`/`quarterly`/`monthly`/`weekly`/`daily`/`hourly`.

## Host shape

SERIES Process host whose single grouper is `GROUP_DATE`, else `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `frequency` from `spec.Params`, else `req.Groups[0].Params`. No `tz` — follows the host grouper's zone (`zone: following`): keys are the host's local labels.

## Output

SERIES — one `SeriesEntry` per host key, `yoy = point / prior × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- Strides: annual `-1`, quarterly `-4`, monthly `-12`, weekly `-52`; daily/hourly exact-key lookup one year back.
- Feb 29 with a non-leap prior year → NaN (no realignment).
- Missing `frequency` → `PULSE_OVERLAY_YOY_FREQUENCY_MISSING`; unsupported → `PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY`.
- First year of data → NaN, no warning. Zero prior value → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- Buffered.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-index-vs-prior`](op-overlay-index-vs-prior.md), [`op-overlay-index-vs-rolling-mean`](op-overlay-index-vs-rolling-mean.md).
