---
name: op-group-date
description: Partition date/datetime records by calendar component; optional fiscal_offset shifts year/quarter to a fiscal calendar.
kind: operator
category: GROUP
operator: GROUP_DATE
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `component` | enum | `month` | `day`, `day_of_week`, `week`, `month`, `quarter`, `year`. |
| `fiscal_offset` | int | 0 | Months after Jan when FY starts; `year`/`quarter` only. Non-zero prefixes keys `FY` (end-year). |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `date`, `datetime` |

`datetime` truncates to the UTC calendar day (time of day discarded, never rounded — `23:59:59` stays on its day).

## Output

String key per row (e.g. `2024-Q1`, `FY2025-Q1`). Smart default for `date` and `datetime`.

## Components

Universal floor `{total_n, n_null}` plus:

| Key | Type | Notes |
|---|---|---|
| `granularity` | string | Component used |
| `range_start` | string | ISO, earliest period |
| `range_end` | string | ISO, latest period |
| `n_buckets` | int | Distinct buckets |
| `buckets` | []bucket | `{key, period_start, period_end, count}` |

- Mergeability: `Mergeable`
- Streaming: `Streamable=false`. Hint — `GROUP_CATEGORY` on `ATTR_DATE_PART` for streaming.

## Gotchas

- `day_of_week` weekday names lex-sort — sort explicitly.
- `fiscal_offset` with sub-quarter components rejected.
- `Group.Include` not honoured; no sub-day `component` even for `datetime`.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `grouper-design`, `response-components`
