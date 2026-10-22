---
name: op-feat-date-features
description: Expand a date or datetime field into year / month / day / day-of-week / quarter (+ hour) columns.
kind: operator
category: FEAT
operator: FEAT_DATE_FEATURES
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, time-series, pre-filter]
---

Feature operators emit derived columns; no `Response.Components`.

<!-- generated: use-when -->

## Params

None. `Field` (required) — `params` block is unused.

## Inputs

`Field` — `date` or `datetime`.

## Output

Columns prefixed by `Label` (default the field name):

| Column | Type | Range |
|---|---|---|
| `<prefix>_year` | f64 | year |
| `<prefix>_month` | f64 | `1..12` |
| `<prefix>_day` | f64 | `1..31` |
| `<prefix>_dow` | f64 | ISO `1..7`, `1` = Monday, `7` = Sunday |
| `<prefix>_quarter` | f64 | `1..4` |
| `<prefix>_hour` | f64 | `0..23` — `datetime` only |

<!-- generated: contract -->

## Gotchas

- Null source or `tz_field` cell → every column `null`.
- `dow` is ISO (`6` = Saturday, `7` = Sunday; formerly `0` = Sunday) of the LOCAL day; ignores `week_start`.
<!-- feature: FILTER_EXCLUDE -->
- Local Saturdays out: `FILTER_EXCLUDE` on `<prefix>_dow` `["6"]`; with `tz_field` each row reads its own zone.
<!-- /feature -->
- Suffixes are `dow` / `quarter` (not `day_of_week` / `is_weekend`).
- Zone-capable: a `datetime` reads the LOCAL clock (slot `tz` / `tz_field` → `time_zone_field` / `time_zone` → default → UTC). A `date` ignores zones (explicit refused).

## See

- `pulse_examples_search tags=[time-series]`, `tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-attr-date-part`, `request-envelope` (Time zones)
