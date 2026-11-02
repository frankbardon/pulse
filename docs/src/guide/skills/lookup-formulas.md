```yaml
name: lookup-formulas
description: Registered tables inside formulas and expression filters — lookup() reading a lookup table, range_label() banding a number or an epoch day into a range-table label, and the two together as rate-grid keys. Matching, null and unmatched rules, codes. Not the cohort point lookup.
type: guide
kind: design
applies_to: process, compose, predict
covers: [expression, formula, lookup, range_label, LookupTables, RangeTables, rate-grid, banding, PULSE_LOOKUP_MISS, PULSE_RANGE_TABLE_UNKNOWN, PULSE_RANGE_TABLE_KIND_MISMATCH]
```

# Lookup formulas

Part of the expression surface; entry skill [`expression-language`](expression-language.md). Two request-bound functions read the instance's registered tables per row — in the formula attribute (Materialize's included), the expression filter, a `total()` argument, a Compose optimize objective and an overlay formula. Neither reads a cohort, and neither is the sidecar-index point lookup.

## lookup

`lookup(table, keys...)` is the number a `LookupTables` table (or a `PULSE_LOOKUP_TABLES_DIR` file) holds under its keys, each stringified and joined with `|`.

- Unknown table → `PULSE_LOOKUP_TABLE_UNKNOWN`; a key combination the table lacks → `PULSE_LOOKUP_MISS` (`details.keys`). A miss fails the request — there is no default.
- A null key → `PULSE_LOOKUP_MISS` with `details.null_key` (0-based after the table name); guard a nullable key with `??`.
- Declared only when the instance registers a lookup table.

## range_label

`range_label(table, x[, unmatched])` is the label of the `RangeTables` range holding `x`:

| Table kind | A range holds `x` when | Pass `x` as |
|---|---|---|
| numeric | `start <= x < end`; an open bound is unbounded | any number — an integer past 2^53 and a decimal compare exactly |
| date | `start <= x <= end` (inclusive) | epoch DAYS: a `date` field as is, a `datetime` through `to_day(ts)` (its seconds are not days) |

- Null `x` → null. NaN, or a value in a gap → `unmatched` when given, else null.
- Unknown table → `PULSE_RANGE_TABLE_UNKNOWN`, even for a null `x`; a value the table's kind cannot match (a string, a categorical label, a bool) → `PULSE_RANGE_TABLE_KIND_MISMATCH` (`details.got`).
- Always declared and reserved: an `ExprFunctions` entry named `range_label` → `PULSE_EXTENSION_NAME_RESERVED`.
- The result is a STRING: compare it or key a `lookup` with it. A formula's own value stays a number.

## Patterns

Rate grid keyed by two bandings (the table's keys are the labels joined by `|`):

```json
{"label": "rate", "expression": "lookup(\"rate_grid\", range_label(\"score_bands\", score, \"other\"), range_label(\"spend_bands\", spend))"}
```

Keep the top bands (a filter):

```json
{"expression": "range_label(\"score_bands\", score) in [\"high\", \"top\"]"}
```

## Gotchas

- In a filter, `==` on an unmatched or null row is false (the row drops) and `!=` true (it stays) — name the gap with `unmatched` to make it explicit.
- Declare the grid's axes (`keys`, an axis naming its `range_table`): `pulse.New` then checks every row key against the bands' labels, so a typo is a startup refusal rather than a run-time `PULSE_LOOKUP_MISS`.
- A `datetime` passed without `to_day` is read as a day count and matches the wrong range silently.
- A literal call is checked before any record, by predict and the run alike: an unknown table, a `range_label` over a string / bool / list, or — every key literal, axes declared — an off-axis key or wrong key count. Computed arguments fail per row.

## See

- [`expression-language`](expression-language.md) — the environment these functions extend.
- [`tool-range-tables`](tool-range-tables.md) — the registered range tables and their kinds.
- [`op-group-numeric-ranges`](op-group-numeric-ranges.md), [`op-group-date-ranges`](op-group-date-ranges.md) — group by the same tables.
- [`calendar-formulas`](calendar-formulas.md) — `to_day` and the request time zone.
- `docs/src/internals/extension-points.md` — registering `LookupTables` and `RangeTables`.
