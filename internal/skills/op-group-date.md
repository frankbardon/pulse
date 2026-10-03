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

- `component` — enum, default `month`: `day`, `day_of_week`, `week`, `month`, `quarter`, `year`.
- `fiscal_offset` — int, default 0. Months after Jan the FY starts; `year`/`quarter` only. Non-zero prefixes keys `FY` (end-year).
- `tz` — slot key, not `params`; IANA zone, beats `time_zone`.

## Inputs

`Field` — `date`, `datetime` (floored to the UTC day).

## Output

String key per row (`2024-Q1`, `FY2025-Q1`). Smart default for `date` and `datetime`.

## Components

Floor `{total_n, n_null}` + `granularity` (component used), `range_start` / `range_end` (ISO), `n_buckets` (int), `buckets` (`[]bucket` of `{key, period_start, period_end, count}`). `Mergeable`; `Streamable=false`<!-- feature: GROUP_CATEGORY, ATTR_DATE_PART --> (to stream: `GROUP_CATEGORY` over `ATTR_DATE_PART`)<!-- /feature -->.

## Gotchas

- `day_of_week` names lex-sort — sort explicitly.
- `fiscal_offset` with sub-quarter components rejected.
- `Group.Include` not honoured; no sub-day `component`.
- `tz` on a `date` field, or a non-UTC zone reaching a `datetime`, → `PROCESSING_CONFIG` (not yet applied).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `grouper-design`, `response-components`, `request-envelope` (Time zones)
