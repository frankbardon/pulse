---
name: op-attr-date-part
description: Extract a calendar component (year, month, day, year_month, ...) from a date field.
kind: operator
category: ATTR
operator: ATTR_DATE_PART
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, feature-engineering, streaming-friendly]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `part` | enum | (required) | One of `day`, `month`, `month_day`, `year`, `year_month`, `year_month_day`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `date` (or `nullable_date`) |
| `Label` | required — new column name |

## Output

One `float64` per record carrying an encoded integer: `year` = YYYY, `month` = 1..12, `day` = 1..31, `year_month` = YYYYMM, `year_month_day` = YYYYMMDD, `month_day` = MMDD. Null source → null output.

## Gotchas

- Row-local one-pass — streams cleanly.
- Output is `f64` (uniform ATTR scalar coercion); cast to int downstream if needed.
- Useful as a `groups` field for `GROUP_CATEGORY` (e.g. `month_day` for seasonality) or as a `FEAT` substitute when post-filter visibility is needed.
- Unknown `part` value → `PROCESSING_CONFIG`.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `attribute-composition`, `op-attr-formula`
