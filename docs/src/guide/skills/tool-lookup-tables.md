```yaml
name: tool-lookup-tables
kind: tool
description: List registered lookup tables — named numeric tables lookup() reads — with their key axes and cell counts, never their cells.
type: reference
applies_to: mcp
```

## When to use

CALL TO FIND WHICH LOOKUP TABLES EXIST AND THEIR KEY AXES BEFORE WRITING A LOOKUP() CALL.
Rate grids, multipliers, per-segment constants: each table answers `lookup("table", k1, k2, …)` with a number. This is the INPUT direction — which tables exist and which keys each accepts, in argument order.

## Input

No arguments.

## Output

`{tables: []LookupTableInfo}`, in name order. Each entry:

- `name`, `description` (absent when none).
- `has_rows_data` — `true` for a static table, `false` for a function-driven one.
- `cell_count` — the static table's number of cells; ABSENT on a function-driven table, whose cells cannot be counted.
- `keys` — the declared axes in `lookup()` argument order, each `{name, values}` (inline) or `{name, range_table}` (the range table's labels are the values). `[]` when none are declared: the keys are then free-form.
- `complete` — every combination of the axes' values has a cell.

Never a cell value. Empty array when none registered (never null). Tables come from `Options.Extensions.LookupTables` or `$PULSE_LOOKUP_TABLES_DIR/*.json` at `pulse.New` time.

## Gotchas

- Empty registry returns an empty list, NOT an error.
- Keys are stringified and joined with `|`: the number `1` is the axis value `"1"`.
- A combination the table lacks — an off-axis key, or a gap in a table that is not `complete` — fails the request with `PULSE_LOOKUP_MISS`; there is no default. A literal call with an off-axis key is refused before any record.
- A `range_table` axis is fed by `range_label(table, x)`.
- `pulse_range_tables` lists that table's labels.
- Unrelated to the cohort point lookup ([`tool-lookup`](tool-lookup.md)).

## See

- [`lookup-formulas`](lookup-formulas.md) — `lookup` and `range_label` semantics and the rate-grid pattern.
- [`tool-range-tables`](tool-range-tables.md) — the range tables a `range_table` axis names.
- [`expression-language`](expression-language.md) — where `lookup()` is callable.
