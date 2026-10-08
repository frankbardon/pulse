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

- `component`: enum, default `month`: `hour`, `day`, `day_of_week`, `week`, `month`, `quarter`, `year`.
- `week_start`: `week` only; `monday` (default, ISO `2026-W09`) .. `sunday` (key = first day: 2026-03-01).
- `fiscal_offset`: int -11..11; `year`/`quarter` only; keys `FY` (end-year).
- `tz`: slot key (IANA), beats `time_zone`.

## Inputs

`Field`: `date`, `datetime` (in the resolved zone). `hour`: `datetime` only.

## Output

String key (`2024-Q1`, `FY2025-Q1`, `2026-03-29T03`). Smart default (date-family).

## Components

Floor `{total_n, n_null}` + `granularity`, `range_start` / `range_end`, `n_buckets`, `buckets` (`{key, period_start, period_end, count}`; `hour` period = label). `Mergeable`; `Streamable=false`<!-- feature: GROUP_CATEGORY, ATTR_DATE_PART --> (stream: `GROUP_CATEGORY` over `ATTR_DATE_PART`)<!-- /feature -->.

## Gotchas

- `day_of_week` lex-sorts; `Group.Include` ignored.
- Local wall clock: a skipped DST hour has no bucket; a repeated one is ONE.
- `PROCESSING_CONFIG`: `tz` on `date`, zone on a derived field, `hour` off `datetime`, misplaced `week_start` / `fiscal_offset`.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `grouper-design`, `response-components`, `request-envelope` (Time zones)
