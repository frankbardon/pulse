---
name: op-feat-date-features
description: Expand a date field into year / month / day / day-of-week / quarter columns.
kind: operator
category: FEAT
operator: FEAT_DATE_FEATURES
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, time-series, pre-filter]
---

Feature operators emit derived columns; no `Response.Components`.

## Params

None. `Field` (required, `date`) — `params` block is unused.

## Inputs

`Field` — `date` only — rejects every other type including categorical.

## Output

FIVE columns prefixed by `Label` (default the field name):

| Column | Type | Range |
|---|---|---|
| `<prefix>_year` | f64 | calendar year |
| `<prefix>_month` | f64 | `1..12` |
| `<prefix>_day` | f64 | `1..31` |
| `<prefix>_dow` | f64 | `0..6` — `time.Weekday`, `0` = Sunday |
| `<prefix>_quarter` | f64 | `1..4` |

Epoch days decoded as UTC.

## Gotchas

- Non-`date` source → `PROCESSING_CONFIG` at construction.
- Null date → all five columns `null`.
- `dow` `0` = Sunday (Go), NOT ISO `1` = Monday.
- Emitted suffixes are `dow` / `quarter` (not the historical `day_of_week` / `is_weekend`).
- Zone-capable, but `date`-only: explicit slot `tz` → `PROCESSING_CONFIG`; inherited `time_zone` not applied.
- Streamable per-row.

## See

- `pulse_examples_search tags=[time-series]`, `tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-attr-date-part`, `request-envelope` (Time zones)
