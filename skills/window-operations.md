---
name: window-operations
description: WIN_* operators — partitioning, ordering, frames, and per-operator semantics
type: guide
applies_to: process, compose, predict
---

# Window Operations

<skill_overview>
Window operators (`WIN_*`) compute a value per row that depends on other rows in the same partition, in a defined order. They are the table-stakes vocabulary for time-series and ranked queries — `LAG`, `LEAD`, `ROW_NUMBER`, `RANK`, cumulative sums, moving averages, exponential smoothing, period-over-period changes.

Pulse evaluates windows AFTER aggregation, on the post-aggregate `[]map[string]any` row set. When a request has no group and no aggregation, windows operate on one row per filtered record.
</skill_overview>

<rule severity="critical" topic="streaming">
## Streaming-incompatible

Any request with a non-empty `windows` array forces the buffered execution path. Windows require a sort over the row set, which is fundamentally incompatible with the single-pass streaming aggregator path. For very large cohorts, document this loudly to your callers — pre-partition the import or split into smaller cohorts.
</rule>

<reference>
## Window spec shape

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

| Field | Required | Notes |
|---|---|---|
| `type` | yes | One of the ten `WIN_*` constants. |
| `field` | when applicable | Source field. Required for value-bearing operators (LAG, LEAD, RUNNING_*, MOVING_AVG, EWMA, PCT_CHANGE). Forbidden for ROW_NUMBER / RANK / DENSE_RANK. |
| `label` | no | Output column name. Defaults to `<TYPE>_<field>` or `<TYPE>` when no field. |
| `partition_by` | no | Field names. Empty means a single global partition. |
| `order_by` | yes (≥1) | Each key is `{field, desc}`. Field must be a numeric or `date` type — categorical, bool, and packed_bool order keys are rejected by predict. |
| `frame` | conditional | Required for RUNNING_*, MOVING_AVG, EWMA. Forbidden for LAG, LEAD, ROW_NUMBER, RANK, DENSE_RANK, PCT_CHANGE. Mode is always `"rows"`. |
| `params` | per operator | Operator-specific overrides. |
</reference>

<reference>
## Frame semantics

Frame mode is `"rows"` — only mode supported in v1. The frame defines the inclusive window of rows in the partition that the operator can read.

- `preceding: null` → UNBOUNDED PRECEDING (start of partition).
- `preceding: N` → up to `N` rows before the current row.
- `following: null` → UNBOUNDED FOLLOWING (end of partition).
- `following: N` → up to `N` rows after the current row.
- `preceding: 0, following: 0` → current row only.
- `MOVING_AVG` requires both bounded; an unbounded frame degenerates to `RUNNING_AVG` and is rejected by predict.
</reference>

<reference>
## Operator catalog

### `WIN_LAG`

Look back `params.offset` (default 1) rows in the partition. Returns the value of `field` at that earlier row. Output is `params.default` (or `null` if not set) for the first `offset` rows.

### `WIN_LEAD`

Look ahead `params.offset` (default 1) rows in the partition. Output is `params.default` (or `null`) for the last `offset` rows.

### `WIN_ROW_NUMBER`

1-based per-partition counter. Output is `int64`. Never null.

### `WIN_RANK`

Standard rank with gaps (1, 2, 2, 4, …). Ties share the same rank; the next distinct value skips. `int64`. Never null.

### `WIN_DENSE_RANK`

Rank without gaps (1, 2, 2, 3, …). `int64`. Never null.

### `WIN_RUNNING_SUM`

Sum of `field` over the frame. Skips nulls. Output is `null` only if every row in the frame is null.

### `WIN_RUNNING_AVG`

Mean of `field` over the frame, skipping nulls. Same null behavior as RUNNING_SUM.

### `WIN_MOVING_AVG`

Mean over a bounded frame. Requires `preceding` AND `following` to be set. Same null behavior.

### `WIN_EWMA`

Exponentially weighted moving average. Recurrence: `s_i = α·x_i + (1-α)·s_{i-1}`. Seed `s_0` is the first non-null value in the partition. `params.alpha` is required and must lie in `(0, 1]`. Leading nulls (before the seed row) emit `null`.

### `WIN_PCT_CHANGE`

`(x_i - x_{i-prev}) / x_{i-prev}`, where `prev = params.periods` (default 1). Emits `null` for the first `periods` rows, when either operand is null, or when the denominator is zero.
</reference>

<rule severity="critical" topic="errors">
## Validation errors

Predict raises `PULSE_WINDOW_INVALID` for every structural violation listed in `error-code-reference.md`. Run `pulse predict --json` against your file to surface the exact rule and offending window index before execution.

`SERVICE_VALIDATION` is reused for the consistency case of a missing or unknown `field` (mirrors aggregation/filter validators).
</rule>

<reference>
## Migrating from `ATTR_RANK`

`ATTR_RANK` was removed when `WIN_RANK` shipped. The window form has proper tie semantics; the old attribute assigned distinct sort-position-dependent ranks to tied values.

**Before** (`ATTR_RANK`, removed):
```json
{
  "attributes": [
    {"type": "ATTR_RANK", "field": "score", "label": "score_rank"}
  ]
}
```

**After** (`WIN_RANK`):
```json
{
  "windows": [
    {
      "type": "WIN_RANK",
      "label": "score_rank",
      "order_by": [{"field": "score"}]
    }
  ]
}
```

Behavior delta: tied values now share a rank (gap rank), where the legacy `ATTR_RANK` produced arbitrary distinct ranks for ties. Any caller depending on the old behavior must reconcile against the new contract.
</reference>

<rule severity="caveat" topic="date-ordering">
## Ordering by dates and date parts

Pulse stores `date` fields as days-since-epoch (numeric), so a window ordered directly on a `date` column sorts correctly without any encoding work.

When you order by a derived date component, mind which producer you use:

- **`ATTR_DATE_PART`** emits `float64`. Encodings that include the year preserve calendar order via arithmetic packing (no zero-padding needed):
  - `year_month` → `year*100 + month` → `202401 < 202411 < 202501` ✓
  - `year_month_day` → `year*10000 + month*100 + day` → `20240115 < 20240301` ✓
  - `month_day` → `month*100 + day` → `315 < 1201` ✓ within a year
- **`GROUP_DATE`** emits zero-padded strings (`"2024-01"`, `"2024-01-15"`, `"2024-W03"`), which lex-sort correctly. The padding is handled by Go's `time.Format`; you do not have to add it.

**Pitfalls:**

- `ATTR_DATE_PART` with bare `"month"` (1..12) or `"day"` (1..31) strips the year. A window ordered on those will mix calendar years and reset on every January — almost never what you want for time-series.
- `GROUP_DATE` with `"day_of_week"` produces weekday names (`"Monday"`, ...) that sort lexicographically, not Sunday→Saturday. Do not order windows on day-of-week.
- Mixing a date `order_by` with a `partition_by` on the raw `date` field collapses every row into its own partition. Partition by a coarser key (region, product, brand) and order by the date.

**Recommended:** for time-series windows over a `date` field, order directly on the `date` column. Use `ATTR_DATE_PART` only when you actually need the bucket value as a column in the response.
</rule>

<rule severity="caveat" topic="cost">
## Cost notes

- One sort per distinct `(partition_by, order_by)` tuple. Multiple windows that share the tuple share the sort.
- Sort is `O(n log n)` over the post-aggregate row set. For a million-row aggregate, expect millisecond costs; for tens of millions, push the partition into the import or split the cohort.
- Use `WIN_ROW_NUMBER` over `WIN_RANK` when ties are not meaningful — both costs are dominated by the sort, but rank also touches the order-key fields per row to detect ties.
</rule>
