# Window operators

The window operators this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`WIN_DELTA`](#op-win_delta) | Adds the change from a set number of rows earlier in the field's own units, such as orders this month minus last month. | By how many orders did each month go up or down on the month before? | basic | [`WIN_PCT_CHANGE`](#op-win_pct_change) when you want the change relative to the earlier value.<br>[`OVERLAY_DELTA_VS_PRIOR`](overlay.md#op-overlay_delta_vs_prior) when you want each period's change from the one before as a decoration on the result. |
| [`WIN_DENSE_RANK`](#op-win_dense_rank) | Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next follows on: 1, 2, 2, 3. | Which price tier is each product in, counting equal prices as one tier? | basic | [`WIN_RANK`](#op-win_rank) when the rank should skip past equal values, as in sports standings.<br>[`WIN_ROW_NUMBER`](#op-win_row_number) when every row needs its own number even when values are equal. |
| [`WIN_EWMA`](#op-win_ewma) | Adds a smoothed series: each new value counts for a fixed share w, the previous smoothed level for 1 - w, so older values fade. | What is the underlying level of daily demand once day-to-day noise is damped? | intermediate | [`WIN_MOVING_AVG`](#op-win_moving_avg) when you want every row in a fixed window to count equally.<br>[`OVERLAY_ZSCORE_VS_ROLLING`](overlay.md#op-overlay_zscore_vs_rolling) when you want how unusual each period is against its recent run. |
| [`WIN_LAG`](#op-win_lag) | Adds to every row the value of a field from a set number of rows earlier in the ordered series, such as last month's sales. | What were each store's sales in the month before, on the same row as this month's? | basic | [`WIN_DELTA`](#op-win_delta) when you want the difference from the earlier row, not its value.<br>[`WIN_LEAD`](#op-win_lead) when you want the value from a later row.<br>[`OVERLAY_INDEX_VS_PRIOR`](overlay.md#op-overlay_index_vs_prior) when you want each period against the period before it as an index. |
| [`WIN_LEAD`](#op-win_lead) | Adds to every row the value of a field from a set number of rows later in the ordered series, such as next month's sales. | What did each customer spend in the following month, beside this month's spend? | basic | [`WIN_LAG`](#op-win_lag) when you want the value from an earlier row.<br>[`WIN_DELTA`](#op-win_delta) when you want how much the field changed between rows. |
| [`WIN_MOVING_AVG`](#op-win_moving_avg) | Adds a moving average: the mean of a field over a fixed number of neighbouring rows, smoothing short-term swings in a series. | What is the trailing seven-day average of daily orders? | basic | [`WIN_RUNNING_AVG`](#op-win_running_avg) when you want the average of everything up to this row.<br>[`WIN_EWMA`](#op-win_ewma) when you want recent rows to count more than older ones.<br>[`OVERLAY_INDEX_VS_ROLLING_MEAN`](overlay.md#op-overlay_index_vs_rolling_mean) when you want each period against its recent average. |
| [`WIN_PCT_CHANGE`](#op-win_pct_change) | Adds the change from a set number of rows earlier as a fraction of the earlier value: 0.05 is a 5% rise on last month. | By what percentage did each month's revenue grow on the month before? | basic | [`WIN_DELTA`](#op-win_delta) when the earlier value can be 0, near 0 or negative, or the field is itself a percentage.<br>[`OVERLAY_YOY`](overlay.md#op-overlay_yoy) when you want the same year-ago period as the comparison.<br>[`OVERLAY_INDEX_VS_PRIOR`](overlay.md#op-overlay_index_vs_prior) when you want each period as an index value against the one before. |
| [`WIN_RANK`](#op-win_rank) | Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next skips: 1, 2, 2, 4. | Where does each store rank on revenue within its region? | basic | [`WIN_DENSE_RANK`](#op-win_dense_rank) when equal values should share a rank with no gap after them.<br>[`WIN_ROW_NUMBER`](#op-win_row_number) when every row needs its own number even when values are equal.<br>[`ATTR_PERCENTILE`](attribute.md#op-attr_percentile) when you want each record's position as a percentage of all records. |
| [`WIN_ROW_NUMBER`](#op-win_row_number) | Numbers the rows 1, 2, 3 within each partition in the chosen order, for picking the first few rows of every group. | Which are the top three products by revenue in each region? | basic | [`WIN_RANK`](#op-win_rank) when rows with equal values should share a position.<br>[`WIN_DENSE_RANK`](#op-win_dense_rank) when you want ranks without gaps after equal values. |
| [`WIN_RUNNING_AVG`](#op-win_running_avg) | Adds the average of a field over the rows so far in the ordered series, such as the average order value to date. | What is the average monthly revenue for the year so far, at each month? | basic | [`WIN_MOVING_AVG`](#op-win_moving_avg) when you want the average of a fixed number of recent rows.<br>[`AGG_AVERAGE`](aggregator.md#op-agg_average) when you want one average per group, not a running one. |
| [`WIN_RUNNING_SUM`](#op-win_running_sum) | Adds a running total of a field down the ordered rows, such as revenue for the year to date. | What is the year-to-date revenue at the end of each month? | basic | [`AGG_SUM`](aggregator.md#op-agg_sum) when you want one total per group, not a running one.<br>[`WIN_RUNNING_AVG`](#op-win_running_avg) when you want the running average instead of the total. |

## Operators

<a id="op-win_delta"></a>

### `WIN_DELTA`

Adds the change from a set number of rows earlier in the field's own units, such as orders this month minus last month.

**Level:** basic

**Questions it answers:**

- By how many orders did each month go up or down on the month before?
- How many points did satisfaction move between waves?

**Use cases by domain:**

- *survey:* Change in a brand's awareness, in percentage points, from the last wave.
- *ops:* Week-on-week change in tickets opened per queue.
- *science:* Change in each subject's reading since the previous visit.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Computed separately within each partition_by slice, in order_by order; with no partition the whole result is one series.
- Steps are rows, not calendar periods: a period with no row is skipped, not filled, so one row back can span two periods.
- The first rows of each partition, and any row where either value is missing, read null; an earlier value of 0 is a real change.

**Use something else:**

- [`WIN_PCT_CHANGE`](#op-win_pct_change) when you want the change relative to the earlier value.
- [`OVERLAY_DELTA_VS_PRIOR`](overlay.md#op-overlay_delta_vs_prior) when you want each period's change from the one before as a decoration on the result.

**Glossary:** [`percentage-point`](../glossary.md#term-percentage-point), [`baseline`](../glossary.md#term-baseline), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-win-delta`](../skills/op-win-delta.md)

<a id="op-win_dense_rank"></a>

### `WIN_DENSE_RANK`

Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next follows on: 1, 2, 2, 3.

**Level:** basic

**Questions it answers:**

- Which price tier is each product in, counting equal prices as one tier?
- With scores ordered desc, how many distinct scores sit above each respondent's (the dense rank minus 1)?

**Use cases by domain:**

- *survey:* Position of each rating level from the top, with equal ratings sharing one position.
- *ops:* Tier number of each store by sales, equal sales sharing a tier.
- *science:* Order of distinct dose levels within each trial arm.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Rank 1 goes to the first row in order_by order: the smallest value, or the largest when the key is desc.
- Rows tie only when equal on every order_by key.
- Rows with a missing order_by value sort last, in both directions.

**Use something else:**

- [`WIN_RANK`](#op-win_rank) when the rank should skip past equal values, as in sports standings.
- [`WIN_ROW_NUMBER`](#op-win_row_number) when every row needs its own number even when values are equal.

**Glossary:** [`rank`](../glossary.md#term-rank), [`ties`](../glossary.md#term-ties)

**Skill:** [`op-win-dense-rank`](../skills/op-win-dense-rank.md)

<a id="op-win_ewma"></a>

### `WIN_EWMA`

Adds a smoothed series: each new value counts for a fixed share w, the previous smoothed level for 1 - w, so older values fade.

**Level:** intermediate

**Questions it answers:**

- What is the underlying level of daily demand once day-to-day noise is damped?
- Is the smoothed defect rate drifting up over recent batches?

**Use cases by domain:**

- *survey:* Smoothed tracking score that reacts to recent waves more than old ones.
- *ops:* Smoothed daily demand for a stock forecast.
- *science:* Smoothed sensor reading that follows slow drift and damps spikes.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Computed separately within each partition_by slice, in order_by order; with no partition the whole result is one series.
- Steps are rows, not calendar periods: a period with no row is skipped, not filled, so one row back can span two periods.
- The share w is set directly (params.alpha, above 0 and at most 1); it seeds from the partition's first value present.
- A missing value reads null and the smoothing carries over it.

**Use something else:**

- [`WIN_MOVING_AVG`](#op-win_moving_avg) when you want every row in a fixed window to count equally.
- [`OVERLAY_ZSCORE_VS_ROLLING`](overlay.md#op-overlay_zscore_vs_rolling) when you want how unusual each period is against its recent run.

**Glossary:** [`mean`](../glossary.md#term-mean), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-win-ewma`](../skills/op-win-ewma.md)

<a id="op-win_lag"></a>

### `WIN_LAG`

Adds to every row the value of a field from a set number of rows earlier in the ordered series, such as last month's sales.

**Level:** basic

**Questions it answers:**

- What were each store's sales in the month before, on the same row as this month's?
- What score did each respondent give in the previous wave?

**Use cases by domain:**

- *survey:* Previous wave's score beside the current one for each tracked brand.
- *ops:* Last week's order count beside this week's, per warehouse.
- *science:* The previous reading beside each measurement in a time series.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Computed separately within each partition_by slice, in order_by order; with no partition the whole result is one series.
- Steps are rows, not calendar periods: a period with no row is skipped, not filled, so one row back can span two periods.
- The first rows of each partition have no earlier row and read null, or the default when one is set.

**Use something else:**

- [`WIN_DELTA`](#op-win_delta) when you want the difference from the earlier row, not its value.
- [`WIN_LEAD`](#op-win_lead) when you want the value from a later row.
- [`OVERLAY_INDEX_VS_PRIOR`](overlay.md#op-overlay_index_vs_prior) when you want each period against the period before it as an index.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-win-lag`](../skills/op-win-lag.md)

<a id="op-win_lead"></a>

### `WIN_LEAD`

Adds to every row the value of a field from a set number of rows later in the ordered series, such as next month's sales.

**Level:** basic

**Questions it answers:**

- What did each customer spend in the following month, beside this month's spend?
- What reading came next after each measurement?

**Use cases by domain:**

- *survey:* Next wave's answer beside the current one, to see who changed their mind.
- *ops:* Next shipment date beside each order, to measure the wait.
- *science:* The following reading beside each measurement in a series.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Computed separately within each partition_by slice, in order_by order; with no partition the whole result is one series.
- Steps are rows, not calendar periods: a period with no row is skipped, not filled, so one row back can span two periods.
- The last rows of each partition have no later row and read null, or the default when one is set.

**Use something else:**

- [`WIN_LAG`](#op-win_lag) when you want the value from an earlier row.
- [`WIN_DELTA`](#op-win_delta) when you want how much the field changed between rows.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-win-lead`](../skills/op-win-lead.md)

<a id="op-win_moving_avg"></a>

### `WIN_MOVING_AVG`

Adds a moving average: the mean of a field over a fixed number of neighbouring rows, smoothing short-term swings in a series.

**Level:** basic

**Questions it answers:**

- What is the trailing seven-day average of daily orders?
- Is the trend in weekly sign-ups rising once the week-to-week noise is smoothed out?

**Use cases by domain:**

- *survey:* Three-wave average of a tracking score to steady small-sample waves.
- *ops:* Seven-day average of daily orders.
- *science:* Smooth a noisy sensor series before reading its trend.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Computed separately within each partition_by slice, in order_by order; with no partition the whole result is one series.
- Steps are rows, not calendar periods: a period with no row is skipped, not filled, so one row back can span two periods.
- The frame must be bounded on both sides; near a partition's edges it holds fewer rows.
- Missing values are skipped, so the figure covers the values present; a frame with no value reads null.

**Use something else:**

- [`WIN_RUNNING_AVG`](#op-win_running_avg) when you want the average of everything up to this row.
- [`WIN_EWMA`](#op-win_ewma) when you want recent rows to count more than older ones.
- [`OVERLAY_INDEX_VS_ROLLING_MEAN`](overlay.md#op-overlay_index_vs_rolling_mean) when you want each period against its recent average.

**Glossary:** [`rolling-mean`](../glossary.md#term-rolling-mean), [`mean`](../glossary.md#term-mean), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-win-moving-avg`](../skills/op-win-moving-avg.md)

<a id="op-win_pct_change"></a>

### `WIN_PCT_CHANGE`

Adds the change from a set number of rows earlier as a fraction of the earlier value: 0.05 is a 5% rise on last month.

**Level:** basic

**Questions it answers:**

- By what percentage did each month's revenue grow on the month before?
- Which regions grew fastest relative to their own previous quarter?

**Use cases by domain:**

- *survey:* Relative change in sample size per wave.
- *ops:* Month-on-month growth in revenue per product line.
- *science:* Relative change in a measurement since the previous visit.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Computed separately within each partition_by slice, in order_by order; with no partition the whole result is one series.
- Steps are rows, not calendar periods: a period with no row is skipped, not filled, so one row back can span two periods.
- The first rows of each partition, any row where either value is missing, and any row whose earlier value is 0 read null.

**Use something else:**

- [`WIN_DELTA`](#op-win_delta) when the earlier value can be 0, near 0 or negative, or the field is itself a percentage.
- [`OVERLAY_YOY`](overlay.md#op-overlay_yoy) when you want the same year-ago period as the comparison.
- [`OVERLAY_INDEX_VS_PRIOR`](overlay.md#op-overlay_index_vs_prior) when you want each period as an index value against the one before.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`percentage-point`](../glossary.md#term-percentage-point), [`index-value`](../glossary.md#term-index-value), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-win-pct-change`](../skills/op-win-pct-change.md)

<a id="op-win_rank"></a>

### `WIN_RANK`

Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next skips: 1, 2, 2, 4.

**Level:** basic

**Questions it answers:**

- Where does each store rank on revenue within its region?
- Which product came first in each month by units sold?

**Use cases by domain:**

- *survey:* Rank of each brand on preference within every market.
- *ops:* Rank of each warehouse on on-time delivery within its region.
- *science:* Rank of each site's yield within each season.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Rank 1 goes to the first row in order_by order: the smallest value, or the largest when the key is desc.
- Rows tie only when equal on every order_by key.
- Rows with a missing order_by value sort last, in both directions.

**Use something else:**

- [`WIN_DENSE_RANK`](#op-win_dense_rank) when equal values should share a rank with no gap after them.
- [`WIN_ROW_NUMBER`](#op-win_row_number) when every row needs its own number even when values are equal.
- [`ATTR_PERCENTILE`](attribute.md#op-attr_percentile) when you want each record's position as a percentage of all records.

**Glossary:** [`rank`](../glossary.md#term-rank), [`ties`](../glossary.md#term-ties)

**Skill:** [`op-win-rank`](../skills/op-win-rank.md)

<a id="op-win_row_number"></a>

### `WIN_ROW_NUMBER`

Numbers the rows 1, 2, 3 within each partition in the chosen order, for picking the first few rows of every group.

**Level:** basic

**Questions it answers:**

- Which are the top three products by revenue in each region?
- What is each customer's first order, by date?

**Use cases by domain:**

- *survey:* Number each respondent's answers in the order given.
- *ops:* Top five stores by sales within each region.
- *harness:* Number result rows to page through them in a fixed order.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Row 1 goes to the first row in order_by order: the smallest value, or the largest when the key is desc, so set desc for a top-N.
- Rows with equal order_by values still get different numbers, in an arbitrary but repeatable order.
- Rows with a missing order_by value sort last, in both directions.

**Use something else:**

- [`WIN_RANK`](#op-win_rank) when rows with equal values should share a position.
- [`WIN_DENSE_RANK`](#op-win_dense_rank) when you want ranks without gaps after equal values.

**Glossary:** [`rank`](../glossary.md#term-rank), [`ties`](../glossary.md#term-ties)

**Skill:** [`op-win-row-number`](../skills/op-win-row-number.md)

<a id="op-win_running_avg"></a>

### `WIN_RUNNING_AVG`

Adds the average of a field over the rows so far in the ordered series, such as the average order value to date.

**Level:** basic

**Questions it answers:**

- What is the average monthly revenue for the year so far, at each month?
- How has the cumulative average score settled as more waves came in?

**Use cases by domain:**

- *survey:* Average satisfaction to date across fieldwork days.
- *ops:* Average handling time to date, month by month.
- *science:* Cumulative mean of repeated measurements as each one is added.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Computed separately within each partition_by slice, in order_by order; with no partition the whole result is one series.
- The frame sets which rows count: no preceding bound with following 0 gives the average from the start to this row.
- Missing values are skipped, so the figure covers the values present; a frame with no value reads null.

**Use something else:**

- [`WIN_MOVING_AVG`](#op-win_moving_avg) when you want the average of a fixed number of recent rows.
- [`AGG_AVERAGE`](aggregator.md#op-agg_average) when you want one average per group, not a running one.

**Glossary:** [`mean`](../glossary.md#term-mean), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-win-running-avg`](../skills/op-win-running-avg.md)

<a id="op-win_running_sum"></a>

### `WIN_RUNNING_SUM`

Adds a running total of a field down the ordered rows, such as revenue for the year to date.

**Level:** basic

**Questions it answers:**

- What is the year-to-date revenue at the end of each month?
- How many responses had come in by each day of fieldwork?

**Use cases by domain:**

- *survey:* Cumulative completes per day against the fieldwork quota.
- *ops:* Year-to-date sales per region, month by month.
- *science:* Cumulative dose received by each visit.

**Assumptions:**

- Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped.
- Computed separately within each partition_by slice, in order_by order; with no partition the whole result is one series.
- The frame sets which rows add up: no preceding bound with following 0 gives the total from the start to this row.
- Missing values are skipped, so the figure covers the values present; a frame with no value reads null.

**Use something else:**

- [`AGG_SUM`](aggregator.md#op-agg_sum) when you want one total per group, not a running one.
- [`WIN_RUNNING_AVG`](#op-win_running_avg) when you want the running average instead of the total.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-win-running-sum`](../skills/op-win-running-sum.md)
