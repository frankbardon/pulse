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

Exactly one range source in `Params json.RawMessage` (not `Values []string`) — `ranges` XOR `table`:

- `ranges` (`[]{label,start,end}`) — ISO `YYYY-MM-DD`; omit/null a bound for an open one. Inclusive, validated.
- `table` (string) — registered `RangeTable` (`Options.Extensions.RangeTables` / `PULSE_RANGE_TABLES_DIR`).

## Inputs

`Field` — `date` or `datetime`, else `PROCESSING_CONFIG`. `datetime` truncates to the UTC calendar day, so a range's last day keeps rows through `23:59:59`.

## Output

Row-level predicate: keep when the day-integer lies in any range. `label` is validated but plays no part in keep/drop. No emitted column.

## Components

Floor only — `{n_in, n_out, n_null_input}`. Mergeable; counters fold by addition.

## Gotchas

- Null/missing date → dropped.
- Both or neither → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table → `PULSE_RANGE_TABLE_UNKNOWN`.
- Overlap/dup → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL`; bad literal or start>end → `PULSE_RANGE_INVALID`.
- Row-local streamable — auto-available single-pass to `facet` (`FacetRequest.Filterers`) and `sample`.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `op-group-date-ranges`, `response-components`, `facet-design`
