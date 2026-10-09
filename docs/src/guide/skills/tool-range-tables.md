```yaml
name: tool-range-tables
kind: tool
description: List registered range tables — named, reusable sets of labeled date ranges.
type: reference
applies_to: mcp
```

## When to use

CALL TO FIND WHICH NAMED DATE-RANGE SETS EXIST BEFORE GROUPING OR FILTERING BY ONE.
Fiscal quarters, marketing campaigns, product-launch windows. This is the INPUT direction: turning a table name into the usable `{label, start, end}` range set an operator resolves.

## Input

No arguments.

## Output

`descriptor.Envelope` wrapping `{tables: []RangeTableInfo}`, each `RangeTableInfo`: `name`, `range_count`, and `ranges` (the ordered `{label, start, end}` entries; `start`/`end` are ISO date literals, omitted for an open bound). Empty array when none registered (never null). Tables are supplied programmatically via `Options.Extensions.RangeTables` or loaded from `$PULSE_RANGE_TABLES_DIR/*.json` at `pulse.New` time.

## Gotchas

- Empty registry returns an empty list, NOT an error.
- Ranges are validated at `pulse.New` time (non-overlapping, unique labels, non-empty, parseable bounds) — a listed table is always safe to reference.
- Both bounds are inclusive; an empty/absent `start` or `end` is an open bound.
- Entries are calendar days with no zone of their own: over a `datetime` they match the local day in the referencing slot's resolved zone, exactly like inline ranges.
- Reference a table by name from the grouper's `table` field or the filter's `Params.table` — do not re-inline the ranges.

## See

- [`op-group-date-ranges`](op-group-date-ranges.md) — bucket a date field into labeled ranges.
- [`op-filter-date-ranges`](op-filter-date-ranges.md) — keep/drop rows by labeled date range.
- [`tool-label-tables`](tool-label-tables.md) — the parallel discovery tool for categorical label tables.
