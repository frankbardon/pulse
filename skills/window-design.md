---
name: window-design
description: Window slot semantics — partition / order / frame, what WIN_* operators share conceptually, streamability per window. Topical design; per-WIN detail lives in atomic op-win-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [WIN, windows]
---

# Window design

`windows` adds per-row values that depend on OTHER rows in the same partition under a defined order. Table-stakes vocabulary for time-series and ranked queries (`WIN_LAG`, `WIN_LEAD`, `WIN_ROW_NUMBER`, `WIN_RANK`, `WIN_DENSE_RANK`, `WIN_RUNNING_SUM`, `WIN_RUNNING_AVG`, `WIN_MOVING_AVG`, `WIN_EWMA`, `WIN_PCT_CHANGE`). Design contract here; per-WIN detail lives in atomic `op-win-*` skills.

## Slot position

Pipeline order: `features → filterers → attributes → groups → aggregations → windows → sort`. Windows run AFTER aggregation on the post-aggregate `[]map[string]any` row set. Empty `groups` and empty `aggregations` → windows operate on one row per filtered record.

Entry shape:

```json
{
  "type": "WIN_LAG",
  "field": "revenue",
  "label": "revenue_lag",
  "partition_by": ["region"],
  "order_by": [{"field": "ts"}],
  "frame": null,
  "params": {"offset": 1}
}
```

| Key | Required | Notes |
|---|---|---|
| `type` | yes | One of the ten `WIN_*` constants. |
| `field` | conditional | Required for value-bearing ops; forbidden for `WIN_ROW_NUMBER` / `WIN_RANK` / `WIN_DENSE_RANK`. |
| `label` | no | Output column name; default `<TYPE>_<field>`. |
| `partition_by` | no | Field names. Empty → single global partition. |
| `order_by` | yes (≥1) | `{field, desc}`. Field must be numeric or `date` — categorical / bool keys are rejected at predict. |
| `frame` | conditional | Required for `RUNNING_*` / `MOVING_AVG` / `EWMA`. Forbidden for `LAG` / `LEAD` / `ROW_NUMBER` / `RANK` / `DENSE_RANK` / `PCT_CHANGE`. Mode always `"rows"` in v1. |
| `params` | per op | Operator-specific overrides (`offset`, `alpha`, `periods`, `default`, ...). |

## Partition / order / frame

The three axes every window shares:

- **Partition** — `partition_by` carves the row set into independent slices. The math computes inside one slice and never crosses. Empty → one global slice.
- **Order** — `order_by` defines the scan direction inside the partition. Stable, nulls-last comparator. Date fields sort numerically (epoch days).
- **Frame** — bounds the rows the operator can read relative to the current row.

Frame (mode `"rows"`): `preceding: null` → UNBOUNDED PRECEDING; `preceding: N` → up to N rows before; `following: null` → UNBOUNDED FOLLOWING; `following: N` → up to N rows after; `preceding: 0, following: 0` → current row only.

`WIN_MOVING_AVG` requires BOTH bounded; an unbounded frame degenerates to `WIN_RUNNING_AVG` and is rejected by predict.

## Shared conceptual shape

Every `WIN_*` op follows the same contract:

1. Compute the partition map from `partition_by`.
2. Sort each partition once by `order_by`.
3. For each row in scan order, read the framed slice and emit one value into `label`.
4. Result rows are NOT reordered — `order_by` defines scan order, not response order. Use `Request.Sort` to order the response (matches SQL semantics: Postgres, DuckDB, BigQuery).

Multiple windows that share the same `(partition_by, order_by)` tuple share the sort — cost is O(n log n) per distinct tuple.

## Streamability per window

Any non-empty `windows` slate forces the BUFFERED path. Windows require a sort over the row set, incompatible with the single-pass streaming aggregator. Predict marks the request `Streamable=false` whenever `len(windows) > 0`.

The per-op answer today is uniformly "no" — `WIN_LAG`, `WIN_LEAD`, `WIN_ROW_NUMBER`, `WIN_RANK`, `WIN_DENSE_RANK`, `WIN_RUNNING_SUM`, `WIN_RUNNING_AVG`, `WIN_MOVING_AVG`, `WIN_EWMA`, `WIN_PCT_CHANGE` all need scan order. The per-op `Streamable()` method lives in `types/streamability.go` so future exceptions stay declarable.

For very large cohorts, pre-partition into shards by `partition_by` or push the math into the import — sort cost is the bottleneck.

## Components

**Window operators emit row-level values; they do not produce `Response.Components`.** The Components family covers aggregations, groupers, filterers, crosstab, and run — not per-record windowed columns. To audit a windowed column, read it from `Response.Data` or wrap it in an aggregation (`AGG_MEAN` over the windowed label).

The `OVERLAY_*` family (windowed-Process overlays — `OVERLAY_INDEX_VS_PRIOR`, `OVERLAY_INDEX_VS_ROLLING_MEAN`, `OVERLAY_ZSCORE_VS_ROLLING`, `OVERLAY_YOY`, `OVERLAY_DELTA_VS_BASELINE`, `OVERLAY_INDEX_VS_BASELINE`) is the sibling surface for windowed analytics that DOES produce typed payloads — use overlays when you want renderer-visible comparison values keyed to host coordinates; use `WIN_*` when you want the comparison rolled into a per-record column. See `overlay-system`.

## Gotchas

- `WIN_*` does NOT reorder response rows. Use `Request.Sort` to order the response.
- Ordering on `day_of_week` (`ATTR_DATE_PART` raw or `GROUP_DATE` string) sorts lexicographically, not Sun→Sat. Order on epoch days or a year-bearing key.
- Partitioning by the raw `date` field collapses every row into its own partition. Partition by a coarser key (region, product) and order by the date.
- Any `windows` slate forces buffered execution — predict surfaces `Streamable=false`.
- `WIN_EWMA` requires `params.alpha ∈ (0, 1]`; leading nulls emit null until the first non-null seed row.
- `WIN_PCT_CHANGE` emits null when the denominator is zero (never a panic).

## See

- Recipes: `pulse_examples_search tags=["time-series"]`, `tags=["ranking"]`, `tags=["moving-average"]`, `tags=["lag-lead"]` plus atomic `op-win-<name>`.
- `aggregation-design` — what the window-input rows come from.
- `overlay-system` — windowed-Process overlays vs `WIN_*` choice.
- `request-envelope` — slot keys, sort + streamability.
- `streaming-and-watching` — buffered execution mode + sort cost.
