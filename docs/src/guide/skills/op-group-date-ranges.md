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
- `table` — registered `RangeTable` (Extensions or `PULSE_RANGE_TABLES_DIR`).
- `unmatched_label` — default `unmatched`; not a range label.
- `tz` — slot key (not `params`); IANA, beats `time_zone`.

## Inputs

`Field` — `date` or `datetime` (floored to its local day in the resolved zone, UTC default). Range bounds (inline or `table`; tables carry no zone) are local days.

## Output

Range label per row, else `unmatched_label`; supplied order.

## Components

Floor `{total_n, n_null}` + `n_ranges` (int), `unmatched_label` (string), `buckets` (`{key, label, count}`, unmatched last). Mergeable, streamable.

## Gotchas

- Both/neither source → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table → `PULSE_RANGE_TABLE_UNKNOWN`; non-date → `PROCESSING_CONFIG`.
- Overlap / dup label / bad bound → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL` / `_INVALID`.
- `tz` on `date`, or non-UTC on a derived field → `PROCESSING_CONFIG`.
- `Group.Include` not honoured.

## See

- Skills: [`grouper-design`](grouper-design.md), [`response-components`](response-components.md), [`request-envelope`](request-envelope.md) (Time zones)
