---
name: op-win-row-number
description: 1-based row index within the ordered partition; never ties.
kind: operator
category: WIN
operator: WIN_ROW_NUMBER
type: reference
applies_to: process, compose, predict
examples_tags: [top-n, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

None. `partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (forbidden), `field` (refused `PULSE_WINDOW_INVALID` — no value read; manifest `forbids_field`, `accepts_types: []`).

## Inputs

- `Field` — must be empty / omitted — no value field is read

## Output

One `int64` per row written to `Label` (default `WIN_ROW_NUMBER`). Counts `1, 2, 3, ...` in scan order, resets per partition. NEVER ties — row order is stable (nulls last) on the `order_by` comparator.

<!-- generated: contract -->

## Gotchas

- Ties on `order_by`: ROW_NUMBER picks a deterministic but arbitrary order.
- Common top-N idiom: `WIN_ROW_NUMBER` partitioned by group, then a range filter on the label `[1, N]`. Filter runs BEFORE windows in the pipeline — stage via Compose / ProcessChain.
- Result rows are NOT reordered — use `Request.Sort` for response order.

## See

- `pulse_examples_search tags=[top-n]`
- Skills: `window-design`, `op-win-rank`, `op-win-dense-rank`
