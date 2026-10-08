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

<!-- generated: use-when -->

## Params

One source in `Params` (not `Values`) — `ranges` XOR `table`:

- `ranges` — `[]{label,start,end}`, ISO; null bound = open; inclusive.
- `table` — registered `RangeTable` (Extensions or `PULSE_RANGE_TABLES_DIR`).
- `tz` — slot key (not `params`); IANA, beats `time_zone`.

## Inputs

`Field` — `date` or `datetime`, else `PROCESSING_CONFIG`. `datetime` floors to its local day in the resolved zone (UTC default); bounds (inline or `table`; tables carry no zone) are local days.

## Output

Keeps rows whose day is in any range; `label` unused, no column.

## Components

Floor only — `{n_in, n_out, n_null_input}`. Mergeable (additive).

## Gotchas

- Null date → dropped.
- Both/neither → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table → `PULSE_RANGE_TABLE_UNKNOWN`.
- Overlap/dup → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL`; bad literal, start>end → `_INVALID`.
- `start: 2026-03-01`, `tz: Europe/Berlin` keeps `2026-02-28T23:30Z`.
- `tz` on `date`, non-UTC on a derived field, any `tz` under `FilterToFileWithRequest` → `PROCESSING_CONFIG`.
- Row-local streamable.

## See

- Skills: `op-group-date-ranges`, `facet-design`, `request-envelope` (Time zones)
