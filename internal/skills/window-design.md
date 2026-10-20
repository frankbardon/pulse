---
name: window-design
description: Window slot semantics — partition / order / frame, what window operators share conceptually, streamability per window. Topical design; per-WIN detail lives in atomic op-win-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [WIN, windows]
---

# Window design

`windows` adds per-row values that depend on OTHER rows in the same partition under a defined order — time-series and ranked queries. Per-operator detail: atomic `op-win-*` skills.

## Slot position

Pipeline order: `features → filterers → attributes → groups → aggregations → windows → sort`. Windows run AFTER aggregation on the post-aggregate row set; with empty `groups` and `aggregations` they see one row per filtered record: the full record plus window columns.

Entry shape:

<!-- feature: WIN_LAG -->
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
<!-- /feature -->

| Key | Required | Notes |
|---|---|---|
| `type` | yes | A window constant from `pulse_manifest` `components.windows`. |
| `field` | conditional | Required for value-bearing ops; forbidden for the row-number / rank family (they read only order). |
| `label` | no | Output column; default `<TYPE>_<field>`. Naming an existing output column or earlier window label is `SERVICE_VALIDATION` (shadow). |
| `partition_by` | no | Field names. Empty → single global partition. |
| `order_by` | yes (≥1; a non-cumulative share / lift needs none) | `{field, desc}`. Any type but `set_*` (`PULSE_WINDOW_INVALID`, predict and runtime): numbers / `decimal128` by value, `date` / `datetime` by signed epoch (pre-1970 first), `packed_bool` false first, categorical by LABEL byte-wise. A grouper key follows its grouper's order (Monday first, ranges as supplied, buckets by value). Nulls last in BOTH directions — `desc` reverses non-null values only. A NaN (an undefined figure, e.g. an expanded vector element no row answered) is a null. |
| `frame` | conditional | Required for running, moving and exponentially weighted aggregates; forbidden for offset, rank, change and share / lift ops. Mode `"rows"`. Every slot refusal is `PULSE_WINDOW_INVALID`, predict and runtime alike. |
| `params` | per op | Operator-specific overrides (`offset`, `alpha`, `periods`, `default`, ...). |

## Partition / order / frame

The three axes every window shares:

- **Partition** — `partition_by` carves the row set into independent slices. The math computes inside one slice and never crosses. Empty → one global slice.
- **Order** — `order_by` defines the scan direction inside the partition; the stable nulls-last comparator is shared with `Request.Sort` and post-test `order_by`.
- **Frame** — bounds the rows the operator can read relative to the current row.

Frame (mode `"rows"`): `preceding: null` → UNBOUNDED PRECEDING; `preceding: N` → up to N rows before; `following: null` → UNBOUNDED FOLLOWING; `following: N` → up to N rows after; `preceding: 0, following: 0` → current row only.

A moving average requires BOTH bounds; an unbounded frame degenerates to the running average and is refused.

## Choosing a window

`pulse_skills_get intents` → `change_over_time` (offsets, changes, running and smoothed aggregates), `benchmark` (ranks, lift), `composition` (shares), `prepare` (row numbering); keep the manifest `components.windows` entries carrying it and read `op-win-<name>` — each one's guidance names its siblings. The deciding questions:

- **Compare with a neighbour** — its raw value (an offset), or the change from it (difference or ratio, below)?
- **Accumulate** — everything so far (running), a fixed recent span (moving), or recent rows weighted more (exponential)?
- **Position** — do ties share a rank, and does the rank skip past them?
- **Share** — of the partition's total, running down an order (a capture curve), or over another column's share (lift)? A zero total reads `null`.
- **A column or a decoration?** A window adds a per-record column; a renderer-keyed comparison against the prior period is an overlay (Components, below).

## Shared conceptual shape

Every window op:

1. Compute the partition map from `partition_by`.
2. Sort each partition once by `order_by`.
3. For each row in scan order, read the framed slice and emit one value into `label`.
4. Result rows are NOT reordered — `order_by` is scan order, not response order; use `Request.Sort`.

Windows sharing a `(partition_by, order_by)` tuple share one sort.

## Difference vs. ratio

Delta and pct-change share every slot (`periods`, no frame, same null rules). The delta reads in the FIELD'S OWN UNITS; the pct-change as a ratio of the prior.

For a percentage metric, 97.9 vs 90.0 is 7.9 points (delta) or 8.8% (pct-change). Points are honest when the metric IS a percentage; the ratio when the base moves. A zero prior is a real delta and a null pct-change (never a panic).

## Streamability per window

Any `windows` entry forces the BUFFERED path (a sort over the row set); predict marks the request `Streamable=false` (`WindowType.Streamable()` is `false` for every window).

## Components

**Windows emit row-level values, not `Response.Components`.**

Windowed-Process overlays (index vs prior, year-over-year, …) DO produce typed payloads keyed to host coordinates — `overlay-system`.

## Lag autocorrelation

<!-- feature: WIN_LAG, TEST_PEARSON_R -->
`WIN_LAG` (no `default`) into post-test `TEST_PEARSON_R` (`field` lag, `field2` value): the leading `null` drops listwise; `default: 0` biases r.
<!-- /feature -->

## Gotchas

- A weekday STRING column (not a grouper key) sorts byte-wise. Order on epoch days or a year-bearing key.
- Partitioning by the raw `date` field collapses every row into its own partition. Partition by a coarser key (region, product) and order by the date.

## See

- Recipes: `pulse_examples_search tags=["time-series"]`, `tags=["ranking"]`, `tags=["moving-average"]`, `tags=["lag-lead"]` plus atomic `op-win-<name>`.
- `aggregation-design` — what the window-input rows come from.
- `overlay-system` — windowed-Process overlays vs window columns.
- `request-envelope` — slot keys, sort + streamability.
- `streaming-and-watching` — buffered execution mode + sort cost.
