```yaml
name: calendar-formulas
description: Calendar helpers and log1p inside formulas and expression filters — weekday, month_index and date_trunc over epoch days, to_day for a datetime in the request time zone, the bare-datetime compile guard and the null rules. Use when an expression reads a date or a datetime.
type: guide
kind: design
applies_to: process, compose, predict
covers: [expression, formula, date, datetime, time_zone, weekday, month_index, date_trunc, to_day, log1p, PULSE_FORMULA_COMPILE_ERROR]
```

# Calendar formulas

Part of the expression surface; entry skill [`expression-language`](expression-language.md). In the formula attribute and the expression filter a `date` field binds as epoch DAYS and a `datetime` as epoch SECONDS — both plain numbers, never interchangeable — so the day helpers read days only and `to_day` is the one bridge from an instant.

## Functions

| Function | Result |
|---|---|
| `weekday(day)` | ISO weekday: 1 = Monday … 7 = Sunday |
| `month_index(day)` | `year * 12 + month - 1`; a difference counts calendar months, across year ends |
| `date_trunc(unit, day)` | the epoch day the period starts: unit `"week"` (Monday start), `"month"`, `"quarter"` or `"year"`, case-sensitive |
| `to_day(ts)` | the local epoch day of an instant, in the request zone |
| `log1p(x)` | `ln(1 + x)`, accurate near 0; an overlay formula admits it too |

A fractional day is floored; a non-finite day or instant → NaN (`null` on the wire). A null argument → a null result for all five: a formula row stays null, a filter drops the row (guard with `??`).

## Time zone

`to_day` reads the wall clock in the request `time_zone`, else `Options.DefaultTimeZone`, else UTC — the zone a date-part attribute inherits ([`time-zones`](time-zones.md)). An instant near midnight moves day with the zone: `2024-03-10T03:30Z` is the 10th in UTC, the 9th in `America/New_York`. DST days are handled by the embedded tz database. A per-slot `tz` is refused on the formula and the expression filter (not zone-capable); set the request `time_zone`.

## The datetime guard

A `datetime` field passed bare as the day of `weekday`, `month_index` or `date_trunc` would read seconds as days, so the compile refuses it: `PULSE_FORMULA_COMPILE_ERROR`, the reason naming `to_day()`, at predict and at the run alike. Wrap it — `weekday(to_day(created_at))`. A literal unit outside the four (`"weeks"`, `"Month"`) is refused at compile the same way; a unit read from a field that is not one of them is `PROCESSING_CONFIG` on the row. A string or decimal day fails the compile's type check; a wrong argument count is `PROCESSING_CONFIG` (build + predict).

## Patterns

```json
{"label": "months_since_signup", "expression": "month_index(order_day) - month_index(signup_day)"}
{"label": "weekend", "expression": "weekday(to_day(created_at)) >= 6"}
{"label": "order_week", "expression": "date_trunc(\"week\", order_day)"}
{"label": "log_spend", "expression": "log1p(spend ?? 0)"}
```

Retention: `month_index(order_day) - month_index(signup_day) == 1` flags a next-month return without hand-coded day ranges.

## See

- [`expression-language`](expression-language.md) — the environment, operators and null handling.
- [`time-zones`](time-zones.md) — which zone a request uses, local-day semantics, DST.
