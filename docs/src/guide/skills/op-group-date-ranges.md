```yaml
name: op-group-date-ranges
description: Bucket date records by inline labeled date ranges; the range label becomes the bucket key.
kind: operator
category: GROUP
operator: GROUP_DATE_RANGES
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, cohort-analysis]
```

## Use when

Labels each row with the named date range its date falls in, such as custom fiscal quarters or before and after a launch.

Questions it answers:

- How did sales compare before, during and after the campaign?
- What are the totals for each of our custom fiscal quarters?

Use something else:

- `GROUP_DATE` when plain calendar months, quarters or weeks are enough.

## Params

One source — `ranges` XOR `table`:

- `ranges` — ordered `[]{label,start,end}`; ISO, omitted = open, inclusive.
- `table` — a DATE `RangeTable` (ISO bounds; Extensions or `PULSE_RANGE_TABLES_DIR`).
- `unmatched_label` — default `unmatched`; not a range label.
- `tz` (IANA) / `tz_field` (per-row zone column) — slot keys.

## Inputs

`Field` — `date` or `datetime`, floored to its local day in the resolved zone (UTC default). Range bounds (inline or `table`; tables carry no zone) are local days.

## Output

Range label per row, else `unmatched_label`; supplied order, unmatched last. `include` keeps listed labels, in listed order.

## Components

- `n_ranges` (int), `unmatched_label` (string), `buckets` (`{key, label, count}`, same order; zero-count ranges kept).

## Contract

- Types (`field`): `date`, `datetime`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `total_n`, `n_null`, `n_ranges`, `unmatched_label`, `buckets`.

## Gotchas

- Both/neither source → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table → `PULSE_RANGE_TABLE_UNKNOWN`; numeric table → `PULSE_RANGE_TABLE_KIND_MISMATCH`.
- Overlap / dup / bad bound → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL` / `_INVALID`.
- Zone on `date` / derived field → `PROCESSING_CONFIG`; null `tz_field` cell → `n_null`.
- An `include` label matching no range drops silently.

## See

- Skills: [`grouper-design`](grouper-design.md), [`response-components`](response-components.md), [`request-envelope`](request-envelope.md) (Time zones)
