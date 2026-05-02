# Window Examples

Runnable JSON requests demonstrating each `WIN_*` operator. Windows run
after aggregation (or instead of it, when no groups + aggs are set) and
emit one output column per window. Every window requires `order_by`.

Setup is documented in [`../README.md`](../README.md). Run all:

```
./examples/windows/run-all.sh
```

## Catalog

| File | Operator | Demonstrates |
|---|---|---|
| `01_lag.json` | `WIN_LAG` | Look back `params.offset` rows (default 1) |
| `02_lead.json` | `WIN_LEAD` | Look ahead `params.offset` rows |
| `03_row_number.json` | `WIN_ROW_NUMBER` | 1-based per-partition counter (no `field`) |
| `04_rank.json` | `WIN_RANK` | Rank with gaps on ties, partitioned by region |
| `05_dense_rank.json` | `WIN_DENSE_RANK` | Rank without gaps on ties |
| `06_running_sum.json` | `WIN_RUNNING_SUM` | Cumulative total over a frame |
| `07_running_avg.json` | `WIN_RUNNING_AVG` | Running mean over a frame |
| `08_moving_avg.json` | `WIN_MOVING_AVG` | Bounded centered moving average |
| `09_ewma.json` | `WIN_EWMA` | Exponentially weighted moving average (`alpha=0.3`) |
| `10_pct_change.json` | `WIN_PCT_CHANGE` | Percent change vs `params.periods` rows ago |

## Frame requirements

| Operator | Frame |
|---|---|
| `LAG`, `LEAD`, `PCT_CHANGE`, `ROW_NUMBER`, `RANK`, `DENSE_RANK` | forbidden |
| `RUNNING_SUM`, `RUNNING_AVG` | required (`preceding: null` = unbounded) |
| `MOVING_AVG` | required, both bounds set |
| `EWMA` | required (typically unbounded preceding to current) |

`order_by` field must be numeric or `date` — categorical, bool, and
packed_bool order keys are rejected by predict. `partition_by` resets
the operator's state per partition; empty means a single global
partition.

See `pulse skills show window-operations` for the full per-operator
contract, frame semantics, and validation gates.
