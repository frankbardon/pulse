---
name: op-group-date-ranges
description: Bucket date records by inline labeled date ranges; the range label becomes the bucket key.
kind: operator
category: GROUP
operator: GROUP_DATE_RANGES
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, cohort-analysis]
---

## Params

Exactly one range source — inline `ranges` XOR named `table`.

| Name | Type | Default | Description |
|---|---|---|---|
| `ranges` | array | (one source) | Ordered `{label, start, end}`. ISO dates, omitted bound = open, inclusive, non-overlapping, distinct labels. |
| `table` | string | (one source) | Registered `RangeTable` name (`Options.Extensions.RangeTables` / `PULSE_RANGE_TABLES_DIR`). |
| `unmatched_label` | string | `unmatched` | Out-of-range bucket; must not equal a range label. |

## Inputs

`Field` — `date`, `datetime`. `datetime` truncates to the UTC calendar day first; ranges compile in whole days.

## Output

The matching range's label per row, else the unmatched label. Buckets emit in supplied range order.

## Components

Floor `{total_n, n_null}` + `n_ranges` (int), `unmatched_label` (string), `buckets` (`[]bucket` of `{key, label, count}`, supplied order, unmatched last). `Mergeable`; `Streamable=true`.

## Gotchas

- Both or neither of `ranges` / `table` → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table → `PULSE_RANGE_TABLE_UNKNOWN`. Field neither `date` nor `datetime` → `PROCESSING_CONFIG`.
- Overlap / dup label / bad boundary → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL` / `_INVALID`.
- `Group.Include` not honoured.

## See

- Skills: `grouper-design`, `response-components`
