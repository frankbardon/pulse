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

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | Over two or more groupers, the leading group key columns each partition shares (default every key but the last; [] = one walk over every row; OVERLAY_YOY: exactly the non-date keys). |
| `frequency` | enum | — | `annual`/`quarterly`/`monthly`/`weekly`/`daily`/`hourly`. |

`scope` must be `group`. `ref.yoy` (empty object) required; an empty `ref` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

SERIES Process host with EXACTLY ONE `GROUP_DATE` grouper, anywhere in `groups`. Over N ≥ 2 `groups` it runs once per combination of every OTHER key; each entry keeps its full key. `params.partition_by`, if set, must name exactly the non-date keys (`PULSE_OVERLAY_PARTITION_INVALID`, `reason: not_non_date_keys`). Zero or 2+ `GROUP_DATE` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`details.date_groupers`), predict and runtime. `frequency` from the overlay's `params`, else the `GROUP_DATE` grouper's `params.frequency`. No `tz` — follows the host grouper's zone (`zone: following`): keys are the host's local labels.

## Output

SERIES — one entry per host key, `summary.statistic` = `point / prior-year point × 100`. Layer `summary.baseline` = `100`.

<!-- generated: contract -->

## Gotchas

- Strides: annual `-1`, quarterly `-4`, monthly `-12`, weekly `-52`; daily/hourly exact-key lookup one year back.
- Feb 29 with a non-leap prior year → `null` (no realignment).
- Missing `frequency` → `PULSE_OVERLAY_YOY_FREQUENCY_MISSING`; unsupported → `PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY`.
- First year of data → `null`, no warning. Zero prior value → `null` + ONE `PULSE_OVERLAY_REF_ZERO` per layer.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-prior`, `op-overlay-index-vs-rolling-mean`.
