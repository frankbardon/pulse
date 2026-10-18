---
name: op-group-numeric-ranges
description: Bucket numeric records by labeled half-open ranges [start, end); the range label becomes the bucket key.
kind: operator
category: GROUP
operator: GROUP_NUMERIC_RANGES
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

One source — `ranges` XOR `table`:

- `ranges` — ordered `[]{label,start,end}`; JSON numbers, omitted/null = open, half-open `[start, end)`.
- `table` — a NUMERIC `RangeTable` (number bounds; Extensions or `PULSE_RANGE_TABLES_DIR`).
- `unmatched_label` — default `unmatched`; not a range label.

Integer split idiom — 0..90 train, 91..103 holdout, ends meet:

```json
{"ranges":[{"label":"train","start":0,"end":91},{"label":"holdout","start":91,"end":104}]}
```

## Inputs

`Field` — `u4`, `u8`..`u64`, `f32` / `f64`. Date, `decimal128`, `packed_bool`, categorical or `set_*` → `PULSE_FIELD_TYPE_UNSUPPORTED` (predict and runtime); dates take <!-- feature: GROUP_DATE_RANGES -->`GROUP_DATE_RANGES`<!-- /feature -->.

## Output

Range label per row, else `unmatched_label`. Rows emit in supplied range order, unmatched last, on every arm (streaming, shards, fused crosstab); an explicit `sort` or axis `include` wins. A weight never moves a key.

## Components

Floor `{total_n, n_null}` + `n_ranges` (int), `unmatched_label` (string), `buckets` (`{key, label, count}`, supplied order, unmatched last). Mergeable, streamable.

## Gotchas

- `[0,91)` + `[91,104)` adjacent, not overlap; `start >= end` → `PULSE_RANGE_INVALID`.
- Overlap / dup label → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL`.
- Both/neither source → `PULSE_RANGE_SOURCE_AMBIGUOUS`; unknown table → `PULSE_RANGE_TABLE_UNKNOWN`; a date table → `PULSE_RANGE_TABLE_KIND_MISMATCH`.
- `Group.Include` not honoured.

## See

- Skills: `grouper-design`, `op-group-range`, `op-group-date-ranges`, `tool-range-tables`
