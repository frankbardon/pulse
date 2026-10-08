```yaml
name: op-win-rank
description: Sparse rank with gaps after ties (1, 2, 2, 4, ...) within the ordered partition.
kind: operator
category: WIN
operator: WIN_RANK
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, top-n, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next skips: 1, 2, 2, 4.

Questions it answers:

- Where does each store rank on revenue within its region?
- Which product came first in each month by units sold?

Use something else:

- `WIN_DENSE_RANK` when equal values should share a rank with no gap after them.

## Params

None. `partition_by` (carve), `order_by` (≥1, required, numeric / `date` — tie comparison reads these keys), `frame` (forbidden), `field` (forbidden — no value read).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | must be empty / omitted — `WIN_RANK` reads ranking from `order_by` keys only |

## Output

One `int64` per row written to `Label` (default `WIN_RANK`). Ties (rows equal on every `order_by` key) share a rank; next distinct row advances by the tie count — `(1, 2, 2, 4, 5)`. Rank resets per partition.

## Reading the output

- `value`: The row's position in order_by order within its partition, starting at 1. Rows equal on every order_by key share a rank, and the next rank skips past them: 1, 2, 2, 4. A row's rank is 1 plus the number of rows strictly before it.
  - Caveat: Rank 1 is the first row in order_by order: the smallest value with an ascending key, the largest with desc. Say which when reporting a 'top' rank.
  - Caveat: Rows tie only when equal on every order_by key; rows with a missing order_by value sort last and tie with each other, so they share the last rank.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- `order_by` defines BOTH scan order AND tie key — same field choice changes the answer.
- Multiple `order_by` keys: tie only when ALL keys equal.
- Result rows are NOT reordered — use `Request.Sort` for response order.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[top-n]`
- Skills: [`window-design`](window-design.md), [`op-win-dense-rank`](op-win-dense-rank.md), [`op-win-row-number`](op-win-row-number.md)
