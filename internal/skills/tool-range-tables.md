---
name: tool-range-tables
kind: tool
description: List registered range tables — named, reusable sets of labeled date or numeric ranges.
type: reference
applies_to: mcp
---

## When to use

CALL TO FIND WHICH NAMED RANGE SETS EXIST BEFORE GROUPING OR FILTERING BY ONE.
Fiscal quarters, campaign windows (date tables); train/holdout splits, score bands (numeric tables). This is the INPUT direction: turning a table name into the usable `{label, start, end}` range set an operator resolves.

## Input

No arguments.

## Output

`descriptor.Envelope` wrapping `{tables: []RangeTableInfo}`, each `RangeTableInfo`: `name`, `kind` (`date` | `numeric`), `range_count`, and the ordered `{label, start, end}` entries — `ranges` on a date table (ISO date literals), `numeric_ranges` on a numeric one (numbers); a bound is omitted when open. Empty array when none registered (never null). Tables are supplied programmatically via `Options.Extensions.RangeTables` or loaded from `$PULSE_RANGE_TABLES_DIR/*.json` at `pulse.New` time.

## Gotchas

- Empty registry returns an empty list, NOT an error.
- Ranges are validated at `pulse.New` time (non-overlapping, unique labels, non-empty, parseable bounds) — a listed table is always safe to reference.
- A table is typed by its bounds. Date bounds are inclusive; numeric bounds half-open `[start, end)`; an empty/absent bound is open.
- A table of the other kind is refused `PULSE_RANGE_TABLE_KIND_MISMATCH`: date tables feed the date-range operators, numeric tables the numeric-ranges grouper.
- Entries are calendar days with no zone of their own: over a `datetime` they match the local day in the referencing slot's resolved zone, exactly like inline ranges.
- Reference a table by name from the grouper's `table` field or the filter's `Params.table` — do not re-inline the ranges.
<!-- feature: capability:materialize_output -->
- A materialize output column's `bucket.range_table` names one too, writing the labels as a categorical (`materialize-recode`).
<!-- /feature -->

## See

- `op-group-date-ranges` — bucket a date field into labeled ranges.
- `op-filter-date-ranges` — keep/drop rows by labeled date range.
- `op-group-numeric-ranges` — bucket a number into labeled half-open ranges.
- `tool-label-tables` — the parallel discovery tool for categorical label tables.
