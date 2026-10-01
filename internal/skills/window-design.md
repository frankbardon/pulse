---
name: window-design
description: Window slot semantics — partition / order / frame, what WIN_* operators share conceptually, streamability per window. Topical design; per-WIN detail lives in atomic op-win-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [WIN, windows]
---

# Window design

`windows` adds per-row values that depend on OTHER rows in the same partition under a defined order. Table-stakes vocabulary for time-series and ranked queries (lag / lead, the rank family, running and moving aggregates, EWMA, pct-change and delta). Design contract here; per-WIN detail lives in atomic `op-win-*` skills.

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
| `type` | yes | One of the registered `WIN_*` constants (`pulse_manifest`). |
| `field` | conditional | Required for value-bearing ops; forbidden for `WIN_ROW_NUMBER` / `WIN_RANK` / `WIN_DENSE_RANK`. |
| `label` | no | Output column name; default `<TYPE>_<field>`. |
| `partition_by` | no | Field names. Empty → single global partition. |
| `order_by` | yes (≥1) | `{field, desc}`. Field must be numeric or `date` — categorical / bool keys are rejected at predict. |
| `frame` | conditional | Required for `RUNNING_*` / `MOVING_AVG` / `EWMA`. Forbidden for `LAG` / `LEAD` / `ROW_NUMBER` / `RANK` / `DENSE_RANK` / `PCT_CHANGE` / `DELTA`. Mode always `"rows"` in v1. |
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

Windows sharing a `(partition_by, order_by)` tuple share the sort — O(n log n) per distinct tuple.

## Difference vs. ratio

`WIN_DELTA` and `WIN_PCT_CHANGE` share every slot — same `periods`, no frame, same null rules — and answer different questions. `WIN_DELTA` subtracts, so it reads in the FIELD'S OWN UNITS; `WIN_PCT_CHANGE` divides, so it reads as a ratio of the prior.

For a percentage-valued metric (a 0–100 score, a share), 97.9 against 90.0 is a gap of 7.9 points (`WIN_DELTA`) or 8.8% (`WIN_PCT_CHANGE`) — both render as "the gap" and they are different numbers. Points are honest when the metric already IS a percentage; the ratio when the base moves. A zero prior is a real delta and a null pct-change.

## Streamability per window

Any non-empty `windows` slate forces the BUFFERED path. Windows require a sort over the row set, incompatible with the single-pass streaming aggregator. Predict marks the request `Streamable=false` whenever `len(windows) > 0`.

The per-op answer today is uniformly "no" — every registered `WIN_*` operator needs scan order, and `WindowType.Streamable()` (`types/streamability.go`) returns `false` unconditionally. The method exists so a future exception stays declarable.

For very large cohorts, pre-partition by `partition_by` or push the math into the import — sort cost dominates.

## Components

**Window operators emit row-level values; they do not produce `Response.Components`.** Components covers aggregations, groupers, filterers, crosstab and run — not windowed columns. To audit a windowed column, read it from `Response.Data` or wrap it in an aggregation (`AGG_MEAN` over the windowed label).

The windowed-Process `OVERLAY_*` family (`OVERLAY_INDEX_VS_PRIOR`, `OVERLAY_YOY`, `OVERLAY_DELTA_VS_BASELINE`, …) is the sibling surface for windowed analytics that DOES produce typed payloads — use overlays when you want renderer-visible comparison values keyed to host coordinates; use `WIN_*` when you want the comparison rolled into a per-record column. See `overlay-system`.

## Gotchas

- Ordering on `day_of_week` (`ATTR_DATE_PART` raw or `GROUP_DATE` string) sorts lexicographically, not Sun→Sat. Order on epoch days or a year-bearing key.
- Partitioning by the raw `date` field collapses every row into its own partition. Partition by a coarser key (region, product) and order by the date.
- `WIN_EWMA` requires `params.alpha ∈ (0, 1]`; leading nulls emit null until the first non-null seed row.
- `WIN_PCT_CHANGE` emits null when the denominator is zero (never a panic); `WIN_DELTA` does not — a zero prior is a real difference (see above).

## See

- Recipes: `pulse_examples_search tags=["time-series"]`, `tags=["ranking"]`, `tags=["moving-average"]`, `tags=["lag-lead"]` plus atomic `op-win-<name>`.
- `aggregation-design` — what the window-input rows come from.
- `overlay-system` — windowed-Process overlays vs `WIN_*` choice.
- `request-envelope` — slot keys, sort + streamability.
- `streaming-and-watching` — buffered execution mode + sort cost.
