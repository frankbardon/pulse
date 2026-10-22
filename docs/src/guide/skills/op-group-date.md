```yaml
name: op-group-date
description: Partition date/datetime records by calendar component (hour..year); week_start picks the week day; fiscal_offset shifts year/quarter.
kind: operator
category: GROUP
operator: GROUP_DATE
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, streaming-friendly]
```

## Use when

Splits the rows by calendar period of a date, such as month, quarter, ISO week or weekday, to see a measure over time.

Questions it answers:

- How many orders came in each month?
- Which day of the week gets the most complaints?

Use something else:

- `GROUP_DATE_RANGES` when your periods are custom, such as campaign windows.
- `OVERLAY_INDEX_VS_PRIOR` when you want each period against the one before it.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `component` | enum | `month` | `hour`, `hour_of_day`, `day`, `day_of_week`, `week`, `month`, `quarter`, `year`. |
| `week_start` | enum | `monday` | `week` only; `monday` (ISO `2026-W09`) .. `sunday` (key = first day: 2026-03-01). |
| `fiscal_offset` | int | `0` | -11..11, `year`/`quarter`; keys `FY` (end-year). |
| `frequency` | enum | — | Not a bucketing knob — the `OVERLAY_YOY` frequency an overlay on this grouper inherits when it sets none. |

Other keys (`unit`, …) → `PULSE_REQUEST_UNKNOWN_FIELD`. `tz` (IANA) / `tz_field` (per-row zone column): slot keys.

## Inputs

`Field`: `date`, `datetime` (in the resolved zone). `hour` / `hour_of_day`: `datetime` only.

## Output

String key (`2024-Q1`, `FY2025-Q1`, `2026-03-29T03`, `Monday`, `09`), chronological; `day_of_week` Monday first, `hour_of_day` `00`..`23`. `include` keeps listed keys, in listed order. Smart default (date-family).

## Components

- `granularity`, `range_start` / `range_end`, `n_buckets`, `buckets` (`{key, period_start, period_end, count}`; `hour` period = label; `day_of_week` / `hour_of_day` none).
- Not streamable as a grouper; stream `GROUP_CATEGORY` over `ATTR_DATE_PART` instead.

## Contract

- Types: `date`, `datetime`.
- Weight: not read; a weight in force is skipped.
- Streaming: buffered only; components `mergeable`. Use GROUP_CATEGORY on an ATTR_DATE_PART output column for a streaming-friendly substitute.
- Components: `total_n`, `n_null`, `granularity`, `range_start`, `range_end`, `n_buckets`, `buckets`.

## Gotchas

- An `include` key never minted (`monday`, `7`) drops silently.
- Local clock: a skipped DST hour has no bucket; a repeated one is ONE. Null `tz_field` cell → `n_null`.
- `PROCESSING_CONFIG`: zone on `date` / derived field, `hour` / `hour_of_day` off `datetime`, misplaced `week_start` / `fiscal_offset`.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`grouper-design`](grouper-design.md), [`response-components`](response-components.md), [`request-envelope`](request-envelope.md) (Time zones)
