---
name: op-group-date-ranges
description: Bucket date records by inline labeled date ranges; the range label becomes the bucket key.
kind: operator
category: GROUP
operator: GROUP_DATE_RANGES
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, cohort-analysis]
---

## Params

Exactly one range source — `ranges` XOR `table`:

- `ranges` — ordered `[]{label, start, end}`; ISO, omitted bound = open, inclusive.
- `table` — registered `RangeTable` (`Options.Extensions.RangeTables` / `PULSE_RANGE_TABLES_DIR`).
- `unmatched_label` — default `unmatched`; must not equal a range label.
- `tz` — slot key, not `params`; IANA zone, beats `time_zone`.

## Inputs

`Field` — `date`, `datetime` (floored to the UTC day).

## Output

Matching range label per row, else the unmatched label; supplied range order.

## Components

Floor `{total_n, n_null}` + `n_ranges` (int), `unmatched_label` (string), `buckets` (`{key, label, count}`, unmatched last). `Mergeable`, `Streamable=true`.

## Gotchas

- Both/neither source → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table → `PULSE_RANGE_TABLE_UNKNOWN`; non-date field → `PROCESSING_CONFIG`.
- Overlap / dup label / bad boundary → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL` / `_INVALID`.
- `tz` on `date`, or a non-UTC zone on `datetime` → `PROCESSING_CONFIG` (not yet applied).
- `Group.Include` not honoured.

## See

- Skills: `grouper-design`, `response-components`, `request-envelope` (Time zones)
