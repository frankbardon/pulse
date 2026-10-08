```yaml
name: op-feat-date-features
description: Expand a date or datetime field into year / month / day / day-of-week / quarter (+ hour) columns.
kind: operator
category: FEAT
operator: FEAT_DATE_FEATURES
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, time-series, pre-filter]
```

Feature operators emit derived columns; no `Response.Components`.

## Use when

Splits a date into year, month, day, day of week and quarter columns, so each part can be filtered, grouped or modelled.

Questions it answers:

- Do orders differ by day of the week?
- Is there a seasonal pattern by month across several years?

Use something else:

- `ATTR_DATE_PART` when you need just one part of the date.
- `GROUP_DATE` when you want results grouped by day, month or year.

## Params

None. `Field` (required) — `params` block is unused.

## Inputs

`Field` — `date` or `datetime`; any other type is refused.

## Output

Columns prefixed by `Label` (default the field name):

| Column | Type | Range |
|---|---|---|
| `<prefix>_year` | f64 | year |
| `<prefix>_month` | f64 | `1..12` |
| `<prefix>_day` | f64 | `1..31` |
| `<prefix>_dow` | f64 | `0..6`, `0` = Sunday |
| `<prefix>_quarter` | f64 | `1..4` |
| `<prefix>_hour` | f64 | `0..23` — `datetime` only |

## Gotchas

- Null source → every column `null`.
- `dow` `0` = Sunday, NOT ISO; ignores `week_start`.
- Suffixes are `dow` / `quarter` (not `day_of_week` / `is_weekend`).
- Zone-capable: a `datetime` reads the LOCAL clock (slot `tz` → `time_zone` → default → UTC). A `date` ignores zones: explicit `tz` refused, inherited not applied.
- Streamable per-row.

## See

- `pulse_examples_search tags=[time-series]`, `tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-attr-date-part`](op-attr-date-part.md), [`request-envelope`](request-envelope.md) (Time zones)
