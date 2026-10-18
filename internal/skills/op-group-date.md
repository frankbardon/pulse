---
name: op-group-date
description: Partition date/datetime records by calendar component (hour..year); week_start picks the week day; fiscal_offset shifts year/quarter.
kind: operator
category: GROUP
operator: GROUP_DATE
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, streaming-friendly]
---

<!-- generated: use-when -->

## Params

- `component`: enum, default `month`: `hour`, `hour_of_day`, `day`, `day_of_week`, `week`, `month`, `quarter`, `year`.
- `week_start`: `week` only; `monday` (default, ISO `2026-W09`) .. `sunday` (key = first day: 2026-03-01).
- `fiscal_offset`: -11..11, `year`/`quarter`; keys `FY` (end-year).
<!-- feature: OVERLAY_YOY -->
- `frequency`: not a bucketing knob — the `OVERLAY_YOY` frequency an overlay on this grouper inherits when it sets none.
<!-- /feature -->
- Other keys (`unit`, …) → `PULSE_REQUEST_UNKNOWN_FIELD`.
- `tz` (IANA) / `tz_field` (per-row zone column): slot keys.

## Inputs

`Field`: `date`, `datetime` (in the resolved zone). `hour` / `hour_of_day`: `datetime` only.

## Output

String key (`2024-Q1`, `FY2025-Q1`, `2026-03-29T03`, `Monday`, `09`), chronological; `day_of_week` Monday first, `hour_of_day` `00`..`23`. `include` keeps listed keys, in listed order. Smart default (date-family).

## Components

Floor `{total_n, n_null}` + `granularity`, `range_start` / `range_end`, `n_buckets`, `buckets` (`{key, period_start, period_end, count}`; `hour` period = label; `day_of_week` / `hour_of_day` none). `Mergeable`; `Streamable=false`<!-- feature: GROUP_CATEGORY, ATTR_DATE_PART --> (stream: `GROUP_CATEGORY` over `ATTR_DATE_PART`)<!-- /feature -->.

## Gotchas

- An `include` key never minted (`monday`, `7`) drops silently.
- Local clock: a skipped DST hour has no bucket; a repeated one is ONE. Null `tz_field` cell → `n_null`.
- `PROCESSING_CONFIG`: zone on `date` / derived field, `hour` / `hour_of_day` off `datetime`, misplaced `week_start` / `fiscal_offset`.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `grouper-design`, `response-components`, `request-envelope` (Time zones)
