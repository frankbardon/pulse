---
name: op-win-dense-rank
description: Dense rank with no gaps after ties (1, 2, 2, 3, ...) within the ordered partition.
kind: operator
category: WIN
operator: WIN_DENSE_RANK
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, top-n, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

None. `partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`) — tie comparison reads these keys), `frame` (forbidden), `field` (refused `PULSE_WINDOW_INVALID` — no value read; manifest `forbids_field`, `accepts_types: []`).

## Inputs

- `Field` — must be empty / omitted — reads ranking from `order_by` keys only

## Output

One `int64` per row written to `Label` (default `WIN_DENSE_RANK`). Ties share a rank; next distinct row advances by exactly 1 — `(1, 2, 2, 3, 4)`. Rank resets per partition.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Tie comparison uses every `order_by` key; tie only when ALL keys equal.
- `WIN_DENSE_RANK` does NOT distinguish row count from rank count.
- Result rows are NOT reordered — use `Request.Sort` for response order.

## See

- `pulse_examples_search tags=[top-n]`
- Skills: `window-design`, `op-win-rank`, `op-win-row-number`
