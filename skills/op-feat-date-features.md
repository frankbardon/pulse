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

Feature operators emit row-level/derived columns; they do not produce `Response.Components`.

## Params

None. `Field` (required, `date`) — `params` block is unused.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `date` only — rejects every other type including categorical. |

## Output

FIVE columns prefixed by `Label` (default the field name):

| Column | Type | Range |
|---|---|---|
| `<prefix>_year` | f64 | calendar year |
| `<prefix>_month` | f64 | `1..12` |
| `<prefix>_day` | f64 | `1..31` |
| `<prefix>_dow` | f64 | `0..6` — `time.Weekday`, `0` = Sunday |
| `<prefix>_quarter` | f64 | `1..4` |

Date storage convention: days since the Unix epoch, decoded as UTC (mirrors `ATTR_DATE_PART`).

## Gotchas

- Non-`date` source → `PROCESSING_CONFIG` at construction.
- Null date → ALL FIVE columns emit `null` for that row.
- DOW is `0` = Sunday (Go `time.Weekday` convention), NOT ISO `1` = Monday.
- Capabilities metadata historically listed `day_of_week` / `is_weekend`; actual emitted columns are `dow` / `quarter`. Code is authoritative — reference the suffixes above.
- Streamable per-row.

## See

- `pulse_examples_search tags=[time-series]`, `tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-attr-date-part`, `op-group-date`
