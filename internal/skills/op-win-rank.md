---
name: op-win-rank
description: Sparse rank with gaps after ties (1, 2, 2, 4, ...) within the ordered partition.
kind: operator
category: WIN
operator: WIN_RANK
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
| `Field` | must be empty / omitted — `WIN_RANK` reads ranking from `order_by` keys only |

## Output

One `int64` per row written to `Label` (default `WIN_RANK`). Ties (rows equal on every `order_by` key) share a rank; next distinct row advances by the tie count — `(1, 2, 2, 4, 5)`. Rank resets per partition.

## Gotchas

- Use `WIN_DENSE_RANK` if you want `(1, 2, 2, 3, 4)` — no gaps.
- `order_by` defines BOTH scan order AND tie key — same field choice changes the answer.
- Multiple `order_by` keys: tie only when ALL keys equal.
- Result rows are NOT reordered — use `Request.Sort` for response order.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[top-n]`
- Skills: `window-design`, `op-win-dense-rank`, `op-win-row-number`
