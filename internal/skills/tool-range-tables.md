---
name: tool-range-tables
kind: tool
description: List registered range tables — named, reusable sets of labeled date ranges.
type: reference
applies_to: mcp
---

## When to use

Discover which named labeled-date-range sets exist (e.g. fiscal quarters, marketing campaigns, product-launch windows) before authoring a `GROUP_DATE_RANGES` grouper or `FILTER_DATE_RANGES` filter that references a table by name. This is the INPUT direction: turning a table name into the usable `{label, start, end}` range set an operator resolves.

## Input

No arguments.

## Output

`descriptor.Envelope` wrapping `{tables: []RangeTableInfo}`, each `RangeTableInfo`: `name`, `range_count`, and `ranges` (the ordered `{label, start, end}` entries; `start`/`end` are ISO date literals, omitted for an open bound). Empty array when none registered (never null). Tables are supplied programmatically via `Options.Extensions.RangeTables` or loaded from `$PULSE_RANGE_TABLES_DIR/*.json` at `pulse.New` time.

## Gotchas

- Empty registry returns an empty list, NOT an error.
- Ranges are validated at `pulse.New` time (non-overlapping, unique labels, non-empty, parseable bounds) — a listed table is always safe to reference.
- Both bounds are inclusive; an empty/absent `start` or `end` is an open bound.
- Reference a table by name from the grouper's `table` field or the filter's `Params.table` — do not re-inline the ranges.

## See

- `op-group-date-ranges` — bucket a date field into labeled ranges.
- `op-filter-date-ranges` — keep/drop rows by labeled date range.
- `tool-label-tables` — the parallel discovery tool for categorical label tables.
