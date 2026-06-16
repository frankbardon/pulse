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

## Params

None. `partition_by` (carve), `order_by` (≥1, required, numeric / `date` — tie comparison reads these keys), `frame` (forbidden), `field` (forbidden — no value read).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | must be empty / omitted — reads ranking from `order_by` keys only |

## Output

One `int64` per row written to `Label` (default `WIN_DENSE_RANK`). Ties share a rank; next distinct row advances by exactly 1 — `(1, 2, 2, 3, 4)`. Rank resets per partition.

## Gotchas

- Use `WIN_RANK` if you want gaps after ties — `(1, 2, 2, 4, 5)`.
- Tie comparison uses every `order_by` key; tie only when ALL keys equal.
- `WIN_DENSE_RANK` does NOT distinguish row count from rank count — pair with `WIN_ROW_NUMBER` if you need both.
- Result rows are NOT reordered — use `Request.Sort` for response order.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[top-n]`
- Skills: `window-design`, `op-win-rank`, `op-win-row-number`
