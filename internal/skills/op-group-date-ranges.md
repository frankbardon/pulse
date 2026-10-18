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

<!-- generated: use-when -->

## Params

One source — `ranges` XOR `table`:

- `ranges` — ordered `[]{label,start,end}`; ISO, omitted = open, inclusive.
- `table` — a DATE `RangeTable` (ISO bounds; Extensions or `PULSE_RANGE_TABLES_DIR`).
- `unmatched_label` — default `unmatched`; not a range label.
- `tz` (IANA) / `tz_field` (per-row zone column) — slot keys.

## Inputs

`Field` — `date` or `datetime`, floored to its local day in the resolved zone (UTC default). Range bounds (inline or `table`; tables carry no zone) are local days.

## Output

Range label per row, else `unmatched_label`; supplied order.

## Components

Floor `{total_n, n_null}` + `n_ranges` (int), `unmatched_label` (string), `buckets` (`{key, label, count}`, unmatched last). Mergeable, streamable.

## Gotchas

- Both/neither source → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table → `PULSE_RANGE_TABLE_UNKNOWN`; numeric table → `PULSE_RANGE_TABLE_KIND_MISMATCH`; non-date → `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Overlap / dup / bad bound → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL` / `_INVALID`.
- Zone on `date` / derived field → `PROCESSING_CONFIG`; null `tz_field` cell → `n_null`.
- `Group.Include` not honoured.

## See

- Skills: `grouper-design`, `response-components`, `request-envelope` (Time zones)
