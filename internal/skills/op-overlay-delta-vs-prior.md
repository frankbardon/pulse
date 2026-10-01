---
name: op-overlay-delta-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_PRIOR
description: Per-point streamable windowed additive delta against the immediately preceding point of an ordered SERIES host.
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, trend-detection, streaming-friendly]
---

Overlays decorate the host; no `Response.Components`.

## Params

`Scope` must be `group`. `Ref.Prior` (object, empty) — implicit-default — empty `Ref` also accepted. `Level`/`Within` must be `0`.

## Host shape

SERIES — ordered grouped Process host. Family: windowed prior (`Ref.Prior`).

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `delta = point − prior` on `Summary.Statistic`, in the metric's own units. Layer `Baseline = 0`.

## Choosing between this and `OVERLAY_INDEX_VS_PRIOR`

Absolute-difference twin of `OVERLAY_INDEX_VS_PRIOR`, standing to it as `OVERLAY_DELTA_VS_BASELINE` stands to `OVERLAY_INDEX_VS_BASELINE`. Same host, same ref arm, same carrier — subtraction instead of division.

Pick this one for "how much did it change"; pick the index for "what proportion of the prior is this". An index of `101.6` and a delta of `+1.5` answer different questions and are not interchangeable in a report.

## Gotchas

- Single-state lag carrier (one `float64`) — streamable inside the streaming Process fold.
- First ordinal → NaN, no warning. A delta of `0` would CLAIM nothing changed about a comparison that was never made.
- Absent host point → NaN + carrier does NOT advance (next present point still subtracts the last present value).
- **NEVER emits `PULSE_OVERLAY_REF_ZERO`** — unlike the index twin. A prior of exactly zero is a valid subtrahend (`value − 0 = value`); there is no degenerate-denominator branch to hit. Its absence is deliberate, not an omission.
- `Ref.Prior.Lag` reserved for future window-N priors; v1 ships lag-1 only.
- Empty `Ref` and populated `Ref.Prior` both spell lag-1.
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-prior`, `op-overlay-delta-vs-baseline`.
