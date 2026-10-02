---
name: op-filter-date-ranges
description: Keep records whose date/datetime field falls inside any of a validated set of labeled date ranges.
kind: operator
category: FILTER
operator: FILTER_DATE_RANGES
type: reference
applies_to: process, compose, predict, facet, sample
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

One range source in `Params` (not `Values`) — `ranges` XOR `table`:

- `ranges` — `[]{label,start,end}`, ISO; null bound = open; inclusive.
- `table` — registered `RangeTable` (`Options.Extensions.RangeTables` / `PULSE_RANGE_TABLES_DIR`).
- `tz` — slot key, not `params`; IANA zone, beats `time_zone`.

## Inputs

`Field` — `date` or `datetime`, else `PROCESSING_CONFIG`. `datetime` floors to the UTC day: a range's last day keeps `23:59:59`.

## Output

Keeps rows whose day lies in any range; `label` is unused. No column.

## Components

Floor only — `{n_in, n_out, n_null_input}`. Mergeable (additive).

## Gotchas

- Null/missing date → dropped.
- Both/neither → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table → `PULSE_RANGE_TABLE_UNKNOWN`.
- Overlap/dup → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL`; bad literal or start>end → `PULSE_RANGE_INVALID`.
- `tz` on `date`, a non-UTC zone on `datetime`, or any `tz` under `FilterToFileWithRequest` → `PROCESSING_CONFIG`.
- Row-local streamable; also `facet` and `sample`.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `op-group-date-ranges`, `facet-design`, `request-envelope` (Time zones)
