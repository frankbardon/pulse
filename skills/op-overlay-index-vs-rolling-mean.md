---
name: op-overlay-index-vs-rolling-mean
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_ROLLING_MEAN
description: Per-point windowed index against the arithmetic mean of the W preceding points of an ordered SERIES host.
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, window-operator]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

`Scope` required, must be `group`. `Ref.RollingMean` empty marker tags the ref family; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.window` required, positive width `W`. `Level` / `Within` must be `0`.

## Host shape

SERIES ordered grouped Process host. Rolling-window family, mirroring the `WIN_*` window-via-Params convention.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `index = point / mean(prior W) × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- Carrier: Welford `(count, mean, M2)` trio in a W-wide per-group ring buffer; M2 is reserved so `OVERLAY_ZSCORE_VS_ROLLING` reads SD off the same carrier.
- Missing `params.window` → `PULSE_OVERLAY_PARAM_MISSING`; `window <= 0` → `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`.
- First W present ordinals → NaN, no warning (window unfilled). Absent host point → NaN, and the ring does NOT advance. Zero rolling mean → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per occurrence.
- Buffered — the ring buffer widens streaming-fold state past v1's single-state lag.

## See

- Skills: `overlay-system`, `op-overlay-zscore-vs-rolling`, `op-overlay-index-vs-prior`.
