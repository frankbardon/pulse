---
name: op-overlay-yoy
kind: operator
category: OVERLAY
operator: OVERLAY_YOY
description: Per-point year-over-year ratio against the same period one year prior; requires one GROUP_DATE grouper, partitions by the other keys.
type: reference
applies_to: process
examples_tags: [overlay, time-series, comparison, trend-detection]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `group`. `Ref.YoY` (empty marker) — tags ref family. `params.frequency` (string, conditional) — `annual`/`quarterly`/`monthly`/`weekly`/`daily`/`hourly`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`.

## Host shape

SERIES Process host with EXACTLY ONE `GROUP_DATE` grouper, anywhere in `groups`. Over N ≥ 2 `groups` it runs once per combination of every OTHER key; each entry keeps its full key. `params.partition_by`, if set, must name exactly the non-date keys (`PULSE_OVERLAY_PARTITION_INVALID`, `reason: not_non_date_keys`). Zero or 2+ `GROUP_DATE` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`details.date_groupers`), predict and runtime. `frequency` from `spec.Params`, else the `GROUP_DATE` grouper's `Params`. No `tz` — follows the host grouper's zone (`zone: following`): keys are the host's local labels.

## Output

SERIES — one `SeriesEntry` per host key, `yoy = point / prior × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

<!-- generated: contract -->

## Gotchas

- Strides: annual `-1`, quarterly `-4`, monthly `-12`, weekly `-52`; daily/hourly exact-key lookup one year back.
- Feb 29 with a non-leap prior year → NaN (no realignment).
- Missing `frequency` → `PULSE_OVERLAY_YOY_FREQUENCY_MISSING`; unsupported → `PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY`.
- First year of data → NaN, no warning. Zero prior value → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-prior`, `op-overlay-index-vs-rolling-mean`.
