---
name: window-operations
description: Apply WIN_LAG, WIN_LEAD, WIN_RANK, WIN_DENSE_RANK, WIN_ROW_NUMBER, WIN_MOVING_AVG, WIN_RUNNING_SUM, WIN_RUNNING_AVG, WIN_EWMA, WIN_PCT_CHANGE — partitioning, ordering, and frame semantics. Use when computing row-relative metrics, time-series transforms, or top-N within partition.
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

<rule severity="critical" topic="output-order">
## Windows do not reorder output rows

A window's `order_by` defines the **scan order** for the window math, not the order of result rows. This matches SQL semantics (Postgres, DuckDB, BigQuery): a `LAG()` ordered by `ts` computes the lag against the ts-sorted partition, but the outer query result rows arrive in whatever order upstream produced.

In Pulse: with no aggregation/group, response rows arrive in record-iteration order; with grouping, in map-iteration order (non-deterministic per Go map semantics).

To order the response, use `Request.Sort`:

```json
{
  "cohort": {"filename": "data.pulse"},
  "windows": [
    {"type": "WIN_RUNNING_SUM", "field": "x", "label": "x_cum",
     "order_by": [{"field": "ts"}],
     "frame": {"mode": "rows", "following": 0}}
  ],
  "sort": [{"field": "ts"}]
}
```

`Request.Sort` runs last in the pipeline (after windows), accepts any column the pipeline produces (schema fields, aggregation/attribute/group/window output labels), and uses the same nulls-last comparator as window operators. Predict rejects sort keys that don't match a produced column.
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

Predict raises `PULSE_WINDOW_INVALID` for every structural violation listed in `error-code-reference.md`. Call `pulse_predict` against the request to surface the exact rule and offending window index before execution.

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

## Windowed-Process overlays

These six overlay kinds attach to a grouped Process result where the host axis is ordered (typically `GROUP_DATE`). They sit alongside `WIN_*` operators conceptually but live in `Request.Overlays`, not in `Request.Windows` — overlays are additive post-result decorations whereas `WIN_*` operators rewrite attributes per record. Use overlays when you want renderer-visible comparison values without affecting the base aggregate; use `WIN_*` when you want the comparison rolled into a per-record value.

All six kinds are GROUP-scoped over a SERIES-host (grouped Process) result and produce a SERIES payload — one `SeriesEntry` per host group key in host order, each carrying the overlay value on `Summary.Statistic`. The full catalog (per-kind shape, ref family, error code matrix) lives at `skills/overlay-system.md`; these subsections layer the windowed-specific recipe and gotchas on top.

`Level` and `Within` MUST be zero across this entire family — windowed kinds fold across the ordered axis without a prefix-bucket denominator. Non-zero values fire `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` at both predict and runtime.

### `OVERLAY_DELTA_VS_BASELINE`

Per-point additive delta `point[i] - baseline` against a fixed positional anchor of the host series. Requires `Ref.BaselineIndex.Position` (zero-based ordinal into the host's group key list). The anchored ordinal emits exactly `0.0` (self-vs-self under subtraction). **No `PULSE_OVERLAY_REF_ZERO` arm** — subtraction by zero is well-defined (a zero baseline yields the raw host value verbatim, distinct from the `OVERLAY_INDEX_VS_BASELINE` twin which divides and rejects zero). Output preserves the host cell's units — a `$`-valued `AGG_SUM` point minus a `$`-valued baseline yields a `$`-valued deviation in the same currency.

```json
{
  "cohort": {"filename": "sales.pulse"},
  "groups":       [{"type": "GROUP_DATE", "field": "order_date", "params": {"frequency": "month"}}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "monthly_revenue"}],
  "overlays": [
    {
      "name": "delta_vs_jan",
      "kind": "OVERLAY_DELTA_VS_BASELINE",
      "scope": "group",
      "ref": {"baseline_index": {"position": 0}}
    }
  ]
}
```

**Errors:** `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (missing `Ref.BaselineIndex`, any other ref-family pointer, or non-SERIES host), `PULSE_OVERLAY_REF_UNKNOWN` (negative or out-of-range `Position`), `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` (non-zero `Level` / `Within`).

### `OVERLAY_INDEX_VS_BASELINE`

Per-point ratio index `point[i] / baseline * 100` against a fixed positional anchor. Requires `Ref.BaselineIndex.Position`. The anchored ordinal emits exactly `100.0`. **Zero-baseline path:** when the resolved baseline value is `0` the handler emits ONE `PULSE_OVERLAY_REF_ZERO` warning per layer and surfaces NaN across every emitted entry. An absent host ordinal at the baseline position (resolver reports `(0, false)`) is treated as `baseline_value = 0` and triggers the same warning.

```json
{
  "cohort": {"filename": "sales.pulse"},
  "groups":       [{"type": "GROUP_DATE", "field": "order_date", "params": {"frequency": "month"}}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "monthly_revenue"}],
  "overlays": [
    {
      "name": "index_vs_jan",
      "kind": "OVERLAY_INDEX_VS_BASELINE",
      "scope": "group",
      "ref": {"baseline_index": {"position": 0}}
    }
  ]
}
```

**Errors:** `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (missing `Ref.BaselineIndex`, any other ref-family pointer, or non-SERIES host), `PULSE_OVERLAY_REF_UNKNOWN` (negative or out-of-range `Position`), `PULSE_OVERLAY_REF_ZERO` (warning — zero baseline), `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` (non-zero `Level` / `Within`).

### `OVERLAY_INDEX_VS_PRIOR`

Per-point windowed index `point[i] / point[i-1] * 100` against the immediately preceding present point. **Streamable — the only streamable kind in the windowed family.** The single-state lag carrier is one `f64` per group, advanced on every emit during the streaming Process fold; the post-host finalize is the divide step. The streaming-Process hot path stays untouched — see `skills/streaming-and-watching.md` for the full streamable-vs-buffered cross-reference and the mixed-mode downgrade rule.

The `Ref` accepts either `Ref.Prior` (empty marker — v1 ships lag-1 only; non-zero `Lag` is reserved) OR an entirely empty `Ref` (the implicit-default authoring shape — both spell "lag-1 prior").

**First-present-point semantics:** the first present ordinal emits NaN because no prior is available — this is "no comparison available" and does NOT raise `PULSE_OVERLAY_REF_ZERO`. **Zero-prior path:** when the lag carrier is `0` at the divide step (the most recent present point had a value of zero) the handler emits ONE `PULSE_OVERLAY_REF_ZERO` warning per layer and surfaces NaN on the affected entry. **Absent-point policy:** an absent host ordinal does NOT advance the carrier — the next present ordinal compares against the most recent PRESENT value, not the absent slot.

```json
{
  "cohort": {"filename": "sales.pulse"},
  "groups":       [{"type": "GROUP_DATE", "field": "order_date", "params": {"frequency": "month"}}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "monthly_revenue"}],
  "overlays": [
    {
      "name": "mom_index",
      "kind": "OVERLAY_INDEX_VS_PRIOR",
      "scope": "group"
    }
  ]
}
```

**Errors:** `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (non-`Ref.Prior` ref family or non-SERIES host), `PULSE_OVERLAY_REF_ZERO` (warning — zero prior value), `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` (non-zero `Level` / `Within`).

### `OVERLAY_INDEX_VS_ROLLING_MEAN`

Per-point windowed index `point[i] / mean(point[i-W:i]) * 100` against the arithmetic mean of the W immediately preceding present points. The point itself is EXCLUDED from the window — `mean()` is computed over the W priors only. Window width on `Params["window"]` (positive integer). Ref family is `Ref.RollingMean` (empty marker — the v1 window value lives entirely on `Params`).

**Shared Welford carrier with `OVERLAY_ZSCORE_VS_ROLLING`:** the handler maintains a per-group ring buffer of the W most recently observed PRESENT values plus a Welford-Pébaÿ (count, mean, M2) trio. `OVERLAY_INDEX_VS_ROLLING_MEAN` reads only `mean`; the sibling `OVERLAY_ZSCORE_VS_ROLLING` reads both `mean` and `M2`. The carrier was sized for this reuse at E4-S5 precisely so a Request carrying BOTH kinds folds the trio ONCE per group.

**Window-fill semantics:** the first W present ordinals emit NaN without warning — "window not yet filled" is distinct from "denominator was zero". **Absent-point policy:** absent ordinals do NOT advance the ring buffer (mirrors `OVERLAY_INDEX_VS_PRIOR`). **Zero rolling-mean path:** when the window mean is exactly `0` (every prior in the window was zero) the handler emits ONE `PULSE_OVERLAY_REF_ZERO` warning per layer and surfaces NaN on the affected entries.

```json
{
  "cohort": {"filename": "sales.pulse"},
  "groups":       [{"type": "GROUP_DATE", "field": "order_date", "params": {"frequency": "month"}}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "monthly_revenue"}],
  "overlays": [
    {
      "name": "vs_rolling_3",
      "kind": "OVERLAY_INDEX_VS_ROLLING_MEAN",
      "scope": "group",
      "ref": {"rolling_mean": {}},
      "params": {"window": 3}
    }
  ]
}
```

**Errors:** `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (non-`Ref.RollingMean` ref family or non-SERIES host), `PULSE_OVERLAY_PARAM_MISSING` (missing `Params["window"]`), `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` (non-integer or non-positive `window`; non-zero `Level` / `Within`), `PULSE_OVERLAY_REF_ZERO` (warning — zero rolling mean).

### `OVERLAY_ZSCORE_VS_ROLLING`

Per-point windowed standardized z-score `(point[i] - rolling_mean) / rolling_sample_sd` against the rolling-window mean + **SAMPLE** standard deviation (`sqrt(M2 / (count - 1))`, n-1 denominator) of the W immediately preceding present points. Window width on `Params["window"]`. Ref family is `Ref.RollingMean` (the same empty marker arm as `OVERLAY_INDEX_VS_ROLLING_MEAN`).

**Variance choice (SAMPLE, NOT population) — key contrast with `OVERLAY_ZSCORE_VS_TOTAL`:** the rolling z-score uses sample SD (divide by `count - 1`) because a rolling window of W observations IS a sample of the wider time series. By contrast `OVERLAY_ZSCORE_VS_TOTAL` uses population SD (`sqrt(M2 / N)`) because the per-group aggregation set IS the whole population being standardised. The two surfaces are intentionally orthogonal: rolling = local sample; total = global population.

**Shared Welford carrier with `OVERLAY_INDEX_VS_ROLLING_MEAN`:** the per-group ring buffer + Welford trio is shared with the sibling kind (see the `OVERLAY_INDEX_VS_ROLLING_MEAN` subsection). `ZSCORE_VS_ROLLING` reads BOTH `mean` and `M2`.

**Window-fill semantics:** when the carrier `count < 2` (the Welford recurrence requires at least two observations to define a sample variance) the handler emits NaN without warning. **Zero rolling-SD path:** when the rolling SD is exactly `0` (every prior in the window has the same value — constant series) the handler emits ONE `PULSE_OVERLAY_REF_ZERO` warning per layer and surfaces NaN on the affected entries.

```json
{
  "cohort": {"filename": "sales.pulse"},
  "groups":       [{"type": "GROUP_DATE", "field": "order_date", "params": {"frequency": "month"}}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "monthly_revenue"}],
  "overlays": [
    {
      "name": "z_rolling_3",
      "kind": "OVERLAY_ZSCORE_VS_ROLLING",
      "scope": "group",
      "ref": {"rolling_mean": {}},
      "params": {"window": 3}
    }
  ]
}
```

**Errors:** `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (non-`Ref.RollingMean` ref family or non-SERIES host), `PULSE_OVERLAY_PARAM_MISSING` (missing `Params["window"]`), `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` (non-integer or non-positive `window`; non-zero `Level` / `Within`), `PULSE_OVERLAY_REF_ZERO` (warning — zero rolling SD).

### `OVERLAY_YOY`

Per-point year-over-year ratio `point[i] / prior_year_value * 100`. **Required `GROUP_DATE` host grouper** — other grouper kinds cannot resolve "same period one year prior" semantics. Frequency resolution order:

1. `spec.Params["frequency"]` (the YoY's own override).
2. `req.Groups[0].Params["frequency"]` (the canonical `GROUP_DATE` authoring slot — orchestrator-promoted onto the spec before handler dispatch).
3. Missing both fires `PULSE_OVERLAY_YOY_FREQUENCY_MISSING`.

**Six supported frequencies — per-frequency lag:**

| Frequency | Prior index | Semantics |
|---|---|---|
| `annual` | `i - 1` | one ordinal back |
| `quarterly` | `i - 4` | four ordinals back |
| `monthly` | `i - 12` | twelve ordinals back |
| `weekly` | `i - 52` | calendar-week aligned, **no day-of-week realignment in v1** |
| `daily` | exact-key match for `host.Key(i).AddDate(-1, 0, 0)` | 365-day exact-key lookup — Feb 29 in a non-leap prior year emits NaN with no warning |
| `hourly` | exact-key match for `host.Key(i).Add(-365*24*time.Hour)` | 8760-hour exact-key lookup — same exact-key rule |

**v1 non-goals (explicit):** calendar-week / day-of-week realignment is NOT performed — weekly uses pure `i - 52` arithmetic (no day-of-week shift); daily uses exact-key lookup (no leap-year fill or Feb 28 / Mar 1 realignment for Feb 29). Documented explicitly so callers do not silently receive realigned results.

**First-year-no-comparison:** every ordinal whose prior-year index lands at `< 0` (coarse-frequency arms) OR whose daily / hourly prior-year date does not match an exact host key emits NaN without warning. **Zero-prior path:** when the resolved prior value is `0` the handler emits ONE `PULSE_OVERLAY_REF_ZERO` warning per layer and surfaces NaN on the affected entry.

```json
{
  "cohort": {"filename": "sales.pulse"},
  "groups":       [{"type": "GROUP_DATE", "field": "order_date", "params": {"frequency": "monthly"}}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "monthly_revenue"}],
  "overlays": [
    {
      "name": "yoy",
      "kind": "OVERLAY_YOY",
      "scope": "group",
      "ref": {"yoy": {}}
    }
  ]
}
```

The handler reads `frequency` from the host's `GROUP_DATE.Params["frequency"]` slot (`monthly`) — no `Params` override needed on the overlay spec. Supply `Params["frequency"]` on the overlay only when you want to override the host's frequency (rare).

**Errors:** `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (non-`Ref.YoY` ref family, non-SERIES host, or non-`GROUP_DATE` first grouper), `PULSE_OVERLAY_YOY_FREQUENCY_MISSING` (no frequency on spec or host grouper), `PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY` (frequency value outside the six-element supported set), `PULSE_OVERLAY_REF_ZERO` (warning — zero prior value), `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` (non-zero `Level` / `Within`).
