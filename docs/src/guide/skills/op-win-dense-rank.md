```yaml
name: op-win-dense-rank
description: Dense rank with no gaps after ties (1, 2, 2, 3, ...) within the ordered partition.
kind: operator
category: WIN
operator: WIN_DENSE_RANK
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, top-n, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next follows on: 1, 2, 2, 3.

Questions it answers:

- Which price tier is each product in, counting equal prices as one tier?
- With scores ordered desc, how many distinct scores sit above each respondent's (the dense rank minus 1)?

Use something else:

- `WIN_RANK` when the rank should skip past equal values, as in sports standings.

## Params

None. `partition_by` (carve), `order_by` (≥1, required, numeric / `date` — tie comparison reads these keys), `frame` (forbidden), `field` (forbidden — no value read).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | must be empty / omitted — reads ranking from `order_by` keys only |

## Output

One `int64` per row written to `Label` (default `WIN_DENSE_RANK`). Ties share a rank; next distinct row advances by exactly 1 — `(1, 2, 2, 3, 4)`. Rank resets per partition.

## Reading the output

- `value`: The row's position among the distinct order_by values within its partition, starting at 1. Rows equal on every order_by key share a rank, and the next rank follows on with no gap: 1, 2, 2, 3. The largest rank is the number of distinct values, not the number of rows.
  - Caveat: Rank 1 is the first row in order_by order: the smallest value with an ascending key, the largest with desc. Say which when reporting a 'top' rank.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- Tie comparison uses every `order_by` key; tie only when ALL keys equal.
- `WIN_DENSE_RANK` does NOT distinguish row count from rank count.
- Result rows are NOT reordered — use `Request.Sort` for response order.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[top-n]`
- Skills: [`window-design`](window-design.md), [`op-win-rank`](op-win-rank.md), [`op-win-row-number`](op-win-row-number.md)
