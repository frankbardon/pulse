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

- `component` — enum, default `month`. `day`, `day_of_week`, `week`, `month`, `quarter`, `year`.
- `fiscal_offset` — int, default 0. Months after Jan the FY starts; `year`/`quarter` only. Non-zero prefixes keys `FY` (end-year).

## Inputs

`Field` — `date`, `datetime`.

`datetime` truncates to the UTC calendar day (time discarded, never rounded — `23:59:59` stays on its day).

## Output

String key per row (`2024-Q1`, `FY2025-Q1`). Smart default for `date` and `datetime`.

## Components

Floor `{total_n, n_null}` + `granularity` (string, component used), `range_start` / `range_end` (ISO, earliest / latest period), `n_buckets` (int, distinct buckets), `buckets` (`[]bucket` of `{key, period_start, period_end, count}`). `Mergeable`; `Streamable=false` — hint: `GROUP_CATEGORY` on an `ATTR_DATE_PART` column for streaming.

## Gotchas

- `day_of_week` weekday names lex-sort — sort explicitly.
- `fiscal_offset` with sub-quarter components rejected.
- `Group.Include` not honoured; no sub-day `component` even for `datetime`.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `grouper-design`, `response-components`
