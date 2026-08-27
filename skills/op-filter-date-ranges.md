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

Exactly one range source in `Params json.RawMessage` (not `Values []string`) — inline `ranges` XOR named `table`:
- `ranges` (`[]{label,start,end}`): inline labeled ranges. `start`/`end` are ISO literals (`YYYY-MM-DD`); omit/null either for an open bound. Bounds inclusive. Fully validated.
- `table` (string): name of a registered `RangeTable` (`Options.Extensions.RangeTables` / `PULSE_RANGE_TABLES_DIR`); resolves to the same range set.

## Inputs

`Field` — `date` or `datetime`; anything else → `PROCESSING_CONFIG`. `datetime` truncates to the UTC calendar day, so a range's last day keeps rows through `23:59:59`.

## Output

Row-level predicate. Keep when the record's day-integer lies in any range; drop otherwise. The `label` is validated but plays no role in keep/drop. No emitted column.

## Components

Floor only. Universal `{n_in, n_out, n_null_input}` per `response-components`. Mergeable; counters fold by addition.

## Gotchas

- Null/missing date → dropped.
- Exactly one of `ranges` / `table`; both or neither → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table name → `PULSE_RANGE_TABLE_UNKNOWN`.
- Overlap/dup → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL`; bad literal or start>end → `PULSE_RANGE_INVALID`.
- Row-local streamable — auto-available to `facet` (`FacetRequest.Filterers`) and `sample`, single-pass.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `op-group-date-ranges`, `response-components`, `facet-design`
