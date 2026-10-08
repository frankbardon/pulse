```yaml
name: op-win-row-number
description: 1-based row index within the ordered partition; never ties.
kind: operator
category: WIN
operator: WIN_ROW_NUMBER
type: reference
applies_to: process, compose, predict
examples_tags: [top-n, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Numbers the rows 1, 2, 3 within each partition in the chosen order, for picking the first few rows of every group.

Questions it answers:

- Which are the top three products by revenue in each region?
- What is each customer's first order, by date?

Use something else:

- `WIN_RANK` when rows with equal values should share a position.
- `WIN_DENSE_RANK` when you want ranks without gaps after equal values.

## Params

None. `partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (forbidden), `field` (forbidden — no value read).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | must be empty / omitted — no value field is read |

## Output

One `int64` per row written to `Label` (default `WIN_ROW_NUMBER`). Counts `1, 2, 3, ...` in scan order, resets per partition. NEVER ties — row order is stable (nulls last) on the `order_by` comparator.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- Ties on `order_by`: ROW_NUMBER picks a deterministic but arbitrary order.
- Common top-N idiom: `WIN_ROW_NUMBER` partitioned by group, then a range filter on the label `[1, N]`. Filter runs BEFORE windows in the pipeline — stage via Compose / ProcessChain.
- Result rows are NOT reordered — use `Request.Sort` for response order.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[top-n]`
- Skills: [`window-design`](window-design.md), [`op-win-rank`](op-win-rank.md), [`op-win-dense-rank`](op-win-dense-rank.md)
