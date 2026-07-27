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

| Name | Type | Default | Description |
|---|---|---|---|
| `ranges` | array | (required) | Ordered `{label, start, end}` list. ISO dates; omitted bound = open. Inclusive; non-overlapping, distinct labels. |
| `unmatched_label` | string | `unmatched` | Bucket for out-of-range rows; must not equal a range label. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `date` |

## Output

The matching range's label per row (or the unmatched label). Buckets emit in supplied range order.

## Components

Universal floor `{total_n, n_null}` plus:

| Key | Type | Notes |
|---|---|---|
| `n_ranges` | int | Number of configured ranges |
| `unmatched_label` | string | Out-of-range bucket label |
| `buckets` | []bucket | `{key, label, count}`, supplied order, unmatched last |

Mergeability `Mergeable`; `Streamable=true`.

## Gotchas

- Inline `ranges` only (named `table:` is later). Absent/empty → `PULSE_RANGE_EMPTY`.
- Non-`date` field → `PROCESSING_CONFIG`.
- Overlap / dup label / bad boundary → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL` / `_INVALID`.
- `Group.Include` not honoured.

## See

- Skills: `grouper-design`, `response-components`
