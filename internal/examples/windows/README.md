# Window Examples

Runnable JSON requests demonstrating each `WIN_*` operator. Windows run
after aggregation (or instead of it, when no groups + aggs are set) and
emit one output column per window. When a group, aggregation or crosstab
reads a window label, the windows run first over the records instead
(windows-first, examples 15 and 16; `pulse skills show windows-first`). Every window requires `order_by`,
except a non-cumulative `WIN_SHARE` / `WIN_LIFT`.

Setup is documented in [`../README.md`](../README.md). Run all:

```
./internal/examples/windows/run-all.sh
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
| `11_delta.json` | `WIN_DELTA` | Point difference vs `params.periods` rows ago (same units as the field) |
| `12_ewma_for_each.json` | `WIN_EWMA` | One item fanned out by `for_each` (zip of a label axis and a `decay` axis) into three EWMA columns |
| `13_share_lift_gains.json` | `WIN_SHARE`, `WIN_LIFT` | Gains table on revenue quartiles: share of total, cumulative share and cumulative lift over the subscriber count (`order_by` only for the cumulative forms) |
| `14_distributed_lag.json` | `WIN_DISTRIBUTED_LAG` | Distributed lag (media adstock): geometric (`decay`) and Weibull PDF (`shape`, `scale`) lag kernels over `max_lag` rows, normalised |
| `15_window_column_aggregate.json` | `WIN_DELTA`, `AGG_AVERAGE`, `AGG_COUNT` | Windows-first, ungrouped: aggregate a window column (the mean order-to-order revenue change; the first order's null change is skipped) |
| `16_window_column_by_quarter.json` | `WIN_RUNNING_SUM`, `GROUP_DATE`, `AGG_SUM`, `AGG_MAX` | Windows-first, grouped: the running revenue total reached by each quarter's end beside the quarter's own sum |

## Frame requirements

| Operator | Frame |
|---|---|
| `LAG`, `LEAD`, `PCT_CHANGE`, `DELTA`, `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `SHARE`, `LIFT`, `DISTRIBUTED_LAG` | forbidden (`DISTRIBUTED_LAG` reaches `params.max_lag` rows) |
| `RUNNING_SUM`, `RUNNING_AVG` | required (`preceding: null` = unbounded) |
| `MOVING_AVG` | required, both bounds set |
| `EWMA` | required (typically unbounded preceding to current) |

`order_by` field must be numeric or `date` — categorical, bool, and
packed_bool order keys are rejected by predict. `partition_by` resets
the operator's state per partition; empty means a single global
partition.

See `pulse skills show window-design` for the full per-operator
contract, frame semantics, and validation gates.
