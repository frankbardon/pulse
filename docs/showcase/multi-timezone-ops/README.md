# Multi-time-zone delivery operations

A logistics network with six warehouses in six time zones stores every delivery as a UTC instant. That is the right way to store time, and the wrong way to read it for operations. This walkthrough shows what changes when Pulse reads each warehouse on its own local calendar and clock. It covers daily volumes and on-time rates, incident detection, trend testing, peak-hour staffing, and two overnight feeds that arrive as naive local times across a daylight-saving change.

Every number below was returned by a Pulse request in [`req/`](req/) or by a `pulse` CLI command shown in the step. To re-run everything and check the figures, see [REPRODUCE.md](REPRODUCE.md).

## The business question

> *For each warehouse, by its own business day: how many parcels did we deliver, how many were on time, when do the peaks fall, and did anything go wrong?*

Three operational decisions depend on the answer:

1. **Incident response.** Which warehouse had a bad day, and which day was it?
2. **Performance reviews.** Is any site improving or slipping, or is it just noise?
3. **Staffing.** In which local hours does each dock need the most drivers, before and after the clocks change?

## Dataset

**Fictional network.** Six warehouses. Each is open Monday–Saturday, 07:00–20:59 local time, and closed on Sundays.

| Code | City | Zone | DST change inside the window |
|---|---|---|---|
| LAX | Los Angeles | `America/Los_Angeles` | spring forward 2026-03-08 |
| ORD | Chicago | `America/Chicago` | spring forward 2026-03-08 |
| NYC | New York | `America/New_York` | spring forward 2026-03-08 |
| LON | London | `Europe/London` | spring forward 2026-03-29 |
| BLR | Bengaluru | `Asia/Kolkata` | none (UTC+05:30 all year) |
| SYD | Sydney | `Australia/Sydney` | **fall back** 2026-04-05 (southern hemisphere) |

**Window.** 26 full local weeks, Monday 2026-02-02 to Sunday 2026-08-02.

**`data/deliveries.csv`: 17,193 deliveries** (one row per hand-over) → `deliveries.pulse`. Schema: [`schemas/deliveries.schema.json`](schemas/deliveries.schema.json).

| Field | Type | Meaning |
|---|---|---|
| `delivery_id` | `u32` | Unique id, assigned in UTC order |
| `delivered_at` | `datetime` | Hand-over instant, UTC (`2026-02-01T21:22:18Z`) |
| `warehouse` | `categorical_u8` | LAX, ORD, NYC, LON, BLR, SYD |
| `zone` | `categorical_u8` | IANA zone of the warehouse |
| `route` | `categorical_u8` | 8 routes per site (`SYD-R4`) |
| `service_level` | `categorical_u8` | standard / express |
| `duration_min` | `f64` | Minutes from van departure to hand-over |
| `on_time` | `packed_bool` | Handed over inside the promised window |
| `weight_kg` | `f64` | Billed weight |

**`data/feeds/*.csv`: overnight sorter batches** → [`schemas/night_sort.schema.json`](schemas/night_sort.schema.json). Each hub's sorter closes a batch every 15 minutes between 00:00 and 05:59 local. It logs the time as a **naive local wall-clock string** (`2026-04-05 02:15:00`), with no offset.

| File | Rows | Nights | What happens |
|---|---|---|---|
| `syd_night_sort.csv` | 172 | 2026-04-02 … 04-08 | The fall-back night repeats 02:00–02:59, so the sorter really ran 28 batches and wrote `02:00`, `02:15`, `02:30` and `02:45` twice |
| `nyc_night_sort.csv` | 168 | 2026-03-05 … 03-11 | A legacy controller stamps batches with schedule slots that ignore DST, so on the spring-forward night it writes four `02:xx` times that never existed in New York |

**Built-in ground truth.** The dataset is synthetic and deterministic, with the following facts planted in it for the analysis to recover:

- Every site is **closed on local Sundays**. Weekdays get 17–23 deliveries and Saturdays 8–12.
- **SYD sortation outage on Sydney days Tue 2026-05-12 to Thu 2026-05-14.** On-time probability drops to 0.45 and durations stretch by 1.8×.
- **BLR improves steadily.** Its base on-time probability rises linearly from 0.80 to 0.95 over the 26 weeks.
- **LON is flat at 0.91.** The other sites are also flat (0.90–0.93). All sites have day-level noise.
- Each site has its own local hour-of-day profile. For example, SYD is busiest early in the morning and BLR has morning and evening peaks.

## Methodology

- **Store UTC, read local.** Every request names the zone it reads time in. `time_zone` sets it for the whole request; a slot-level `tz` overrides it for one filter or grouper. Pulse then applies that zone to the local calendar day, ISO week, hour of day and date-range boundaries, including across DST changes.
- **One zone per request, so one request per warehouse.** Each warehouse needs its own zone. The site-by-site views therefore use a **Compose** batch of six requests, each filtered to one warehouse and carrying that warehouse's zone. Each request is also shown with `"time_zone": "UTC"` for contrast.
- **Daily series.** Each daily series is `GROUP_DATE` (`component: day`) plus `AGG_AVERAGE` over the boolean `on_time`, which gives the on-time rate. Window functions add smoothing and day-over-day change. Rolling overlays score each day against the 28 operating days before it.
- **Trend testing.** Trends are tested with Mann-Kendall (`TEST_TREND`) on **weekly** on-time rates, with a Holm correction across the six warehouses.
- **Feed validation.** Naive-time feeds are imported with `--source-tz`. Pulse refuses DST-ambiguous or nonexistent times unless a `--dst-policy` is chosen.

## Walkthrough

### Step 1: The UTC calendar invents Sunday deliveries

**Request.** [`req/01_weekday_volume_utc.json`](req/01_weekday_volume_utc.json) counts deliveries by warehouse × day of week, reading days in UTC:

```json
{
  "cohort": {"filename": "deliveries.pulse", "data_dir": "."},
  "time_zone": "UTC",
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "warehouse"}],
    "columns": [{"type": "GROUP_DATE", "field": "delivered_at", "params": {"component": "day_of_week"}}],
    "cell":    {"type": "AGG_COUNT", "field": "delivery_id", "label": "deliveries"},
    "margins": {"rows": true, "columns": true, "grand": true}
  }
}
```

**Result in UTC days.** Columns are reordered Monday→Sunday for reading.

| warehouse | Mon | Tue | Wed | Thu | Fri | Sat | **Sun** | total |
|---|---|---|---|---|---|---|---|---|
| BLR | 512 | 511 | 527 | 521 | 526 | 250 | **0** | 2,847 |
| LAX | 434 | 520 | 508 | 500 | 531 | 301 | **51** | 2,845 |
| LON | 526 | 515 | 523 | 507 | 514 | 280 | **0** | 2,865 |
| NYC | 515 | 518 | 517 | 506 | 520 | 267 | **6** | 2,849 |
| ORD | 507 | 539 | 528 | 547 | 531 | 273 | **8** | 2,933 |
| SYD | 533 | 551 | 515 | 509 | 401 | 124 | **221** | 2,854 |
| total | 3,027 | 3,154 | 3,118 | 3,090 | 3,023 | 1,495 | **286** | 17,193 |

**Request in local days.** [`req/02_weekday_volume_local.json`](req/02_weekday_volume_local.json) is a Compose of six identical slots. Each slot filters to one warehouse and reads its own zone:

```json
{"label": "SYD_local", "cohort": {"filename": "deliveries.pulse", "data_dir": "."},
 "time_zone": "Australia/Sydney",
 "filterers": [{"type": "FILTER_INCLUDE", "field": "warehouse", "values": ["SYD"]}],
 "crosstab": {"rows": [{"type": "GROUP_CATEGORY", "field": "warehouse"}],
              "columns": [{"type": "GROUP_DATE", "field": "delivered_at", "params": {"component": "day_of_week"}}],
              "cell": {"type": "AGG_COUNT", "field": "delivery_id", "label": "deliveries"},
              "margins": {"rows": true}}}
```

**Result in each warehouse's local days.** No slot returns a Sunday column.

| warehouse | Mon | Tue | Wed | Thu | Fri | Sat | Sun | total |
|---|---|---|---|---|---|---|---|---|
| BLR | 512 | 511 | 527 | 521 | 526 | 250 | 0 | 2,847 |
| LAX | 523 | 538 | 507 | 512 | 502 | 263 | 0 | 2,845 |
| LON | 526 | 515 | 523 | 507 | 514 | 280 | 0 | 2,865 |
| NYC | 529 | 517 | 517 | 506 | 516 | 264 | 0 | 2,849 |
| ORD | 523 | 546 | 527 | 544 | 537 | 256 | 0 | 2,933 |
| SYD | 527 | 533 | 519 | 535 | 499 | 241 | 0 | 2,854 |

**Reading.**

- The UTC view books **286 deliveries on Sundays**, when every site is closed. 221 of them are Sydney's Monday mornings and 51 are Los Angeles's Saturday evenings.
- Sydney's Saturday shows 124 deliveries instead of 241. Los Angeles's Monday shows 434 instead of 523.
- BLR and LON are identical in both views because their working hours never cross UTC midnight.
- Totals are unchanged: the zone moves deliveries between days and never drops one.

### Step 2: The incident week, read on the wrong calendar

**Request.** [`req/03_syd_incident_week_utc_vs_local.json`](req/03_syd_incident_week_utc_vs_local.json) runs two slots that differ only in `time_zone`. Each filters SYD to the 2026-05-11 … 05-17 window, with `FILTER_DATE_RANGES` boundaries read in that slot's zone, and groups by day:

```json
{"label": "syd_week_sydney_days", "cohort": {"filename": "deliveries.pulse", "data_dir": "."},
 "time_zone": "Australia/Sydney",
 "filterers": [{"type": "FILTER_INCLUDE", "field": "warehouse", "values": ["SYD"]},
               {"type": "FILTER_DATE_RANGES", "field": "delivered_at",
                "params": {"ranges": [{"label": "incident_week", "start": "2026-05-11", "end": "2026-05-17"}]}}],
 "groups": [{"type": "GROUP_DATE", "field": "delivered_at", "params": {"component": "day"}}],
 "aggregations": [{"type": "AGG_COUNT", "field": "delivery_id", "label": "deliveries"},
                  {"type": "AGG_AVERAGE", "field": "on_time", "label": "on_time_rate"},
                  {"type": "AGG_AVERAGE", "field": "duration_min", "label": "avg_duration_min"}],
 "sort": [{"field": "delivered_at"}]}
```

**Result.**

| day | UTC: deliveries | UTC: on-time | UTC: avg min | Sydney: deliveries | Sydney: on-time | Sydney: avg min |
|---|---|---|---|---|---|---|
| Mon 05-11 | 24 | 66.7% | 62.1 | 22 | **100.0%** | 36.9 |
| Tue 05-12 | 19 | 52.6% | 77.8 | 22 | **40.9%** | 84.2 |
| Wed 05-13 | 23 | 34.8% | 86.6 | 19 | **47.4%** | 75.0 |
| Thu 05-14 | 13 | 69.2% | 59.5 | 19 | **36.8%** | 90.7 |
| Fri 05-15 | 18 | 100.0% | 40.1 | 19 | 94.7% | 39.4 |
| Sat 05-16 | 5 | 100.0% | 41.3 | 10 | 100.0% | 40.6 |
| Sun 05-17 | 7 | 85.7% | 41.1 | (closed) | | |

**Reading.** On UTC days the outage starts on **Monday**, which in Sydney was a perfect day (22 of 22 on time). Thursday looks like a partial recovery at 69%, although Sydney's Thursday was the worst day of the incident at 36.8%. Read on Sydney days, the incident is exactly Tue–Thu.

**Request: incident periods.** [`req/04_syd_incident_periods.json`](req/04_syd_incident_periods.json) uses `GROUP_DATE_RANGES` to split SYD into a two-week baseline, the incident and a two-week recovery. Range boundaries are local days in the slot's zone:

```json
"groups": [{"type": "GROUP_DATE_RANGES", "field": "delivered_at", "params": {
  "ranges": [{"label": "1_baseline", "start": "2026-04-28", "end": "2026-05-11"},
             {"label": "2_incident", "start": "2026-05-12", "end": "2026-05-14"},
             {"label": "3_recovery", "start": "2026-05-15", "end": "2026-05-28"}],
  "unmatched_label": "outside_window"}}],
"aggregations": [{"type": "AGG_COUNT", ...}, {"type": "AGG_AVERAGE", "field": "on_time", ...},
                 {"type": "AGG_AVERAGE", "field": "duration_min", ...},
                 {"type": "AGG_PERCENTILE", "field": "duration_min", "params": {"percentile": 90}, ...}]
```

**Result.**

| period | UTC: n | UTC: on-time | UTC: avg / p90 min | Sydney: n | Sydney: on-time | Sydney: avg / p90 min |
|---|---|---|---|---|---|---|
| baseline (Apr 28–May 11) | 223 | 90.6% | 44.3 / 65.9 | 220 | **94.1%** | 42.1 / 62.2 |
| incident (May 12–14) | 55 | 49.1% | 77.2 / 110.9 | 60 | **41.7%** | 83.4 / 120.3 |
| recovery (May 15–28) | 220 | 91.4% | 40.4 / 59.3 | 221 | 90.9% | 40.2 / 59.3 |

**Reading.** The UTC calendar leaks incident deliveries into the baseline and normal deliveries into the incident. It shows on-time falling from 90.6% to 49.1%, when on Sydney days it fell from 94.1% to 41.7%. It also reports the outage's p90 delivery time as 110.9 minutes instead of 120.3.

### Step 3: Flag the incident automatically

**Request: daily screen.** [`req/05_daily_anomaly_screen.json`](req/05_daily_anomaly_screen.json) has one slot per warehouse in its own zone. Each slot groups by local day and decorates the series with two rolling overlays. Both overlays score each day against the **28 preceding operating days**, and both read the request's first aggregation, the on-time rate:

```json
{"label": "SYD_daily_screen", "cohort": {"filename": "deliveries.pulse", "data_dir": "."},
 "time_zone": "Australia/Sydney",
 "filterers": [{"type": "FILTER_INCLUDE", "field": "warehouse", "values": ["SYD"]}],
 "groups": [{"type": "GROUP_DATE", "field": "delivered_at", "params": {"component": "day"}}],
 "aggregations": [{"type": "AGG_AVERAGE", "field": "on_time", "label": "on_time_rate"},
                  {"type": "AGG_COUNT", "field": "delivery_id", "label": "deliveries"}],
 "overlays": [
   {"name": "on_time_rolling_z", "kind": "OVERLAY_ZSCORE_VS_ROLLING", "scope": "group",
    "ref": {"rolling_mean": {}}, "params": {"window": 28}},
   {"name": "on_time_rolling_index", "kind": "OVERLAY_INDEX_VS_ROLLING_MEAN", "scope": "group",
    "ref": {"rolling_mean": {}}, "params": {"window": 28}}],
 "sort": [{"field": "delivered_at"}]}
```

Each slot returns 156 operating days. **Result.** After the 28-day warm-up, these are all warehouse-days with rolling z ≤ −3 or rolling index < 70:

| warehouse | local day | deliveries | on-time | rolling z | rolling index (100 = normal) |
|---|---|---|---|---|---|
| **SYD** | **Tue 2026-05-12** | 22 | 40.9% | **−7.65** | **44.3** |
| **SYD** | **Wed 2026-05-13** | 19 | 47.4% | **−3.69** | **52.1** |
| **SYD** | **Thu 2026-05-14** | 19 | 36.8% | **−3.67** | **41.4** |
| NYC | Sat 2026-03-21 | 10 | 50.0% | −5.82 | 54.9 |
| ORD | Thu 2026-05-14 | 23 | 78.3% | −4.48 | 81.8 |
| ORD | Sat 2026-03-14 | 8 | 62.5% | −4.38 | 67.1 |
| ORD | Sat 2026-05-30 | 9 | 66.7% | −4.14 | 71.7 |
| SYD | Sat 2026-08-01 | 8 | 75.0% | −4.07 | 79.1 |
| LAX | Sat 2026-06-20 | 8 | 62.5% | −3.99 | 69.3 |
| LAX | Tue 2026-06-16 | 23 | 69.6% | −3.83 | 77.2 |
| NYC | Fri 2026-05-29 | 20 | 75.0% | −3.76 | 80.8 |
| NYC | Sat 2026-07-04 | 12 | 66.7% | −3.61 | 74.2 |
| LON | Sat 2026-06-20 | 9 | 66.7% | −3.52 | 72.9 |
| SYD | Tue 2026-04-21 | 19 | 73.7% | −3.28 | 79.7 |

**Reading.** Over 6 × 156 warehouse-days, the planted outage gives the strongest signal in the network: z = −7.65 and an index of 44, meaning the day ran at 44% of its own recent norm. All three outage days have the three lowest rolling indexes. Two traits separate the false alarms:

- Seven of the other eleven flags fall on **Saturdays**, with 8–12 deliveries. One or two late parcels move a small day's rate by 10–20 points.
- The weekday false alarms sit at an index of 77–82. They run below normal but are far from broken.

A screen that pages someone on **z ≤ −3 *and* index ≤ 60 *and* at least 15 deliveries** returns exactly the three SYD outage days on this data.

**Request: SYD daily trend panel.** [`req/06_syd_daily_windows.json`](req/06_syd_daily_windows.json) adds window functions to the SYD daily series. It puts average duration first, so the overlays score duration this time:

```json
"aggregations": [{"type": "AGG_AVERAGE", "field": "duration_min", "label": "avg_duration_min"},
                 {"type": "AGG_AVERAGE", "field": "on_time", "label": "on_time_rate"},
                 {"type": "AGG_COUNT", "field": "delivery_id", "label": "deliveries"}],
"windows": [
  {"type": "WIN_MOVING_AVG", "field": "on_time_rate", "label": "on_time_ma6",
   "order_by": [{"field": "delivered_at"}], "frame": {"mode": "rows", "preceding": 5, "following": 0}},
  {"type": "WIN_LAG", "field": "on_time_rate", "label": "on_time_prev_day",
   "order_by": [{"field": "delivered_at"}], "params": {"offset": 1}},
  {"type": "WIN_PCT_CHANGE", "field": "avg_duration_min", "label": "duration_pct_change",
   "order_by": [{"field": "delivered_at"}], "params": {"periods": 1}}],
"overlays": [{"kind": "OVERLAY_ZSCORE_VS_ROLLING", ..., "params": {"window": 28}},
             {"kind": "OVERLAY_INDEX_VS_ROLLING_MEAN", ..., "params": {"window": 28}}]
```

The moving average spans 6 rows, which is one operating week (Mon–Sat).

**Result: Sydney days around the incident.**

| day | n | avg min | Δ vs prior day | duration z | duration index | on-time | prior day on-time | on-time 6-day avg |
|---|---|---|---|---|---|---|---|---|
| 05-08 | 17 | 39.9 | −12.2% | −0.35 | 96.1 | 88.2% | 82.6% | 92.7% |
| 05-09 | 10 | 40.9 | +2.6% | −0.11 | 98.8 | 90.0% | 88.2% | 91.1% |
| 05-11 | 22 | 36.9 | −9.8% | −0.95 | 89.4 | 100.0% | 90.0% | 91.8% |
| **05-12** | 22 | **84.2** | **+128.1%** | **9.34** | **204.1** | 40.9% | 100.0% | 83.6% |
| **05-13** | 19 | **75.0** | −10.9% | **3.47** | **175.9** | 47.4% | 40.9% | 74.9% |
| **05-14** | 19 | **90.7** | +20.9% | **4.21** | **206.5** | 36.8% | 47.4% | 67.2% |
| 05-15 | 19 | 39.4 | −56.6% | −0.46 | 85.8 | 94.7% | 36.8% | 68.3% |
| 05-16 | 10 | 40.6 | +3.2% | −0.35 | 89.3 | 100.0% | 94.7% | 70.0% |
| 05-18 | 20 | 43.8 | +7.9% | −0.12 | 96.3 | 95.0% | 100.0% | 69.1% |

**Reading.**

- On duration, SYD's only days with z ≥ 3 in the whole series (after warm-up) are the three outage days. Average delivery time doubled, to an index of 204–207.
- The day-over-day change catches only the onset (+128%). Days 2 and 3 compare against an already-broken day.
- The 6-day moving average stays depressed for a week after the outage ends. It suits trend charts, not alerting.

**Request: the same screen on UTC days.** [`req/07_syd_screen_utc_vs_local.json`](req/07_syd_screen_utc_vs_local.json) runs the SYD slot of step 3 twice, once with `"time_zone": "UTC"`.

**Result.**

| day | UTC: n | UTC: z | Sydney: n | Sydney: z |
|---|---|---|---|---|
| Mon 05-11 | 24 | **−3.29** (false alarm) | 22 | 1.13 |
| Tue 05-12 | 19 | −4.19 | 22 | −7.65 |
| Wed 05-13 | 23 | −4.70 | 19 | −3.69 |
| Thu 05-14 | 13 | **−1.23** (missed) | 19 | −3.67 |

Over the full period, the UTC screen also flags two "days" holding **2 deliveries each**: 2026-03-07 (z −7.24) and 2026-08-01 (z −8.09). Each is the slice of a Sydney Monday morning or Saturday that falls on a different UTC date. Read on UTC days, the alert fires on the wrong weekday, misses the worst day and adds two false alarms.

### Step 4: Real trend or noise?

**Request.** [`req/08_weekly_on_time_trend.json`](req/08_weekly_on_time_trend.json) has one slot per warehouse. Each groups by local ISO week (`GROUP_DATE` `component: week`) and runs a Mann-Kendall test on the 26 weekly on-time rates. The batch-level `multiplicity` block applies a Holm correction across all six tests:

```json
{"multiplicity": {"method": "holm", "family": "compose"},
 "requests": [
  {"label": "BLR_weekly_trend", "cohort": {"filename": "deliveries.pulse", "data_dir": "."},
   "time_zone": "Asia/Kolkata",
   "filterers": [{"type": "FILTER_INCLUDE", "field": "warehouse", "values": ["BLR"]}],
   "groups": [{"type": "GROUP_DATE", "field": "delivered_at", "params": {"component": "week"}}],
   "aggregations": [{"type": "AGG_AVERAGE", "field": "on_time", "label": "on_time_rate"},
                    {"type": "AGG_COUNT", "field": "delivery_id", "label": "deliveries"}],
   "post_tests": [{"type": "TEST_TREND", "field": "on_time_rate", "label": "on_time_trend", "alpha": 0.05,
                   "order_by": [{"field": "delivered_at"}], "params": {"variant": "mann_kendall"}}],
   "sort": [{"field": "delivered_at"}]},
  "... same for LAX, LON, NYC, ORD, SYD with their zones ..."]}
```

**Result.**

| warehouse | on-time W06 | on-time W31 | Mann-Kendall Z | Kendall τ | p | Holm-adjusted p | trend? |
|---|---|---|---|---|---|---|---|
| **BLR** | 77.0% | 94.7% | **4.98** | 0.698 | 6.3 × 10⁻⁷ | **3.8 × 10⁻⁶** | **yes, improving** |
| LAX | 87.7% | 91.9% | 0.22 | 0.034 | 0.83 | 1.00 | no |
| LON | 86.9% | 90.7% | −0.90 | −0.129 | 0.37 | 1.00 | no |
| NYC | 92.4% | 88.6% | −1.21 | −0.172 | 0.23 | 0.90 | no |
| ORD | 87.7% | 92.1% | 0.53 | 0.077 | 0.60 | 1.00 | no |
| SYD | 89.6% | 94.6% | 1.43 | 0.203 | 0.15 | 0.76 | no |

**Reading.**

- Bengaluru's improvement is real and very strong (τ = 0.70).
- London's first-to-last swing (86.9% → 90.7%) is noise, and so are the swings at the other four sites. Comparing first and last week would have "found" improvements at LAX, ORD and SYD and a decline at NYC.
- The SYD outage week does not create a trend, because Mann-Kendall is rank-based.

**Request: don't test a smoothed series.** [`req/09_daily_trend_raw_vs_smoothed.json`](req/09_daily_trend_raw_vs_smoothed.json) runs the same test on 156 daily values, raw and after a 6-day `WIN_MOVING_AVG`.

**Result.**

| series | BLR Z | BLR p | LON Z | LON p |
|---|---|---|---|---|
| daily raw | 5.34 | 9.2 × 10⁻⁸ | −0.62 | 0.53 |
| daily 6-day moving average | 11.76 | 6.5 × 10⁻³² | −1.43 | 0.15 |

**Reading.** Smoothing makes neighbouring points dependent, which inflates the statistic. BLR's Z goes from 5.34 to 11.76 and LON's p-value from 0.53 to 0.15, with no new information. Test weekly aggregates or raw daily values, and keep moving averages for charts.

### Step 5: Peak hours for staffing, and what DST does to them

**Request: UTC hours.** [`req/10_peak_hours_utc.json`](req/10_peak_hours_utc.json) builds warehouse × hour-of-day using `ATTR_DATE_PART` (`part: hour`) read in UTC:

```json
{"cohort": {"filename": "deliveries.pulse", "data_dir": "."}, "time_zone": "UTC",
 "attributes": [{"type": "ATTR_DATE_PART", "field": "delivered_at", "label": "hour_of_day", "params": {"part": "hour"}}],
 "crosstab": {"rows": [{"type": "GROUP_CATEGORY", "field": "warehouse"}],
              "columns": [{"type": "GROUP_CATEGORY", "field": "hour_of_day", "include": ["0", "1", "...", "23"]}],
              "cell": {"type": "AGG_COUNT", "field": "delivery_id", "label": "deliveries"},
              "margins": {"rows": true, "columns": true, "grand": true}}}
```

**Result: deliveries by UTC hour.** Blank = 0.

| | 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 | 16 | 17 | 18 | 19 | 20 | 21 | 22 | 23 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| BLR | | 34 | 100 | 227 | **319** | 298 | 179 | 129 | 103 | 127 | 213 | 305 | 283 | 304 | 185 | 41 | | | | | | | | |
| LAX | 235 | 158 | 103 | 56 | 8 | | | | | | | | | | 40 | 84 | 143 | 182 | 222 | 259 | 324 | 348 | **362** | 321 |
| LON | | | | | | | 62 | 156 | 247 | **321** | 320 | 270 | 273 | 322 | 279 | 205 | 182 | 117 | 59 | 42 | 10 | | | |
| NYC | 60 | 8 | | | | | | | | | | 83 | 189 | 325 | **385** | 375 | 337 | 244 | 222 | 205 | 177 | 95 | 101 | 43 |
| ORD | 52 | 48 | 13 | | | | | | | | | | 121 | 268 | 360 | **387** | 275 | 215 | 209 | 251 | 230 | 214 | 164 | 126 |
| SYD | 274 | 191 | 163 | 167 | 214 | 184 | 151 | 95 | 51 | 57 | 41 | | | | | | | | | | 94 | 328 | **444** | 400 |

**Request: local hours.** [`req/11_peak_hours_local.json`](req/11_peak_hours_local.json) is the same crosstab as a six-slot Compose, each slot filtered to one warehouse and read in its own zone.

**Result: deliveries by local hour.** Peak in bold.

| | 07 | 08 | 09 | 10 | 11 | 12 | 13 | 14 | 15 | 16 | 17 | 18 | 19 | 20 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| BLR | 51 | 165 | 295 | **349** | 228 | 149 | 100 | 91 | 161 | 273 | 302 | 331 | 250 | 102 |
| LAX | 47 | 98 | 152 | 185 | 224 | 294 | 319 | 345 | **378** | 294 | 237 | 137 | 90 | 45 |
| LON | 86 | 188 | 283 | **335** | 315 | 245 | 292 | 309 | 275 | 186 | 180 | 92 | 39 | 40 |
| NYC | 100 | 215 | 341 | **413** | 385 | 291 | 234 | 222 | 205 | 155 | 107 | 82 | 37 | 62 |
| ORD | 157 | 291 | **375** | 366 | 282 | 194 | 211 | 261 | 227 | 209 | 163 | 98 | 46 | 53 |
| SYD | 265 | **454** | 435 | 311 | 210 | 173 | 143 | 206 | 209 | 171 | 107 | 54 | 46 | 70 |

**Reading.**

- Locally, every site works the same 14 hours (07–20). In UTC, every site spans 15 hour columns:
  - LAX, ORD, NYC, LON and SYD because their UTC offset moved by an hour mid-period;
  - BLR because its +05:30 offset splits every local hour across two UTC hours.
- In UTC, SYD's morning rush (local 08:00, 454 deliveries) is split between 21Z and 22Z.
- BLR's evening wave (local 17–18) is real in both views. Bengaluru has no DST, so its UTC columns are a clean 5½-hour shift.

**Request: what the clock change does to a UTC roster.** [`req/12_nyc_dst_hour_shift.json`](req/12_nyc_dst_hour_shift.json) splits NYC into its EST and EDT periods. The rows use `GROUP_DATE_RANGES` with a slot-level `"tz": "America/New_York"`, so both slots cut the periods on New York days. Each cell is a row share (`normalize: row`). The columns read hours in UTC in one slot and in New York time in the other:

```json
"crosstab": {"rows": [{"type": "GROUP_DATE_RANGES", "field": "delivered_at", "tz": "America/New_York",
                       "params": {"ranges": [{"label": "1 EST (Feb 2 - Mar 7)", "start": "2026-02-02", "end": "2026-03-07"},
                                             {"label": "2 EDT (Mar 8 - Aug 2)", "start": "2026-03-08", "end": "2026-08-02"}]}}],
             "columns": [{"type": "GROUP_CATEGORY", "field": "hour_of_day", "include": ["0", "...", "23"]}],
             "cell": {"type": "AGG_COUNT", "field": "delivery_id", "label": "deliveries"},
             "margins": {"rows": true}, "normalize": "row"}
```

**Result: NYC's busiest local hours and the UTC column each one lands in.**

| local hour | EST share (553 deliveries) | EST UTC column | EDT share (2,296 deliveries) | EDT UTC column |
|---|---|---|---|---|
| 09:00 | 10.7% | 14Z | 12.3% | 13Z |
| 10:00 | 15.7% | 15Z | 14.2% | 14Z |
| 11:00 | 17.5% | 16Z | 12.5% | 15Z |
| 12:00 | 9.2% | 17Z | 10.5% | 16Z |

**Reading.** The same local shift lands one UTC column earlier after 2026-03-08. For example, the 10:00 local hour is 15.7% of volume in the 15Z column before the change and 14.2% in the 14Z column after it. A roster built on UTC hours would be an hour late for 21 of the 26 weeks in New York, Chicago and Los Angeles, from 29 March in London, and an hour *early* in Sydney from 5 April.

### Step 6: Overnight feeds that arrive as naive local times

**Default import.** Importing with `--source-tz` alone refuses the DST-affected rows and writes no file:

```
$ pulse import csv -i data/feeds/syd_night_sort.csv -o syd_night_sort.pulse \
    --schema schemas/night_sort.schema.json --source-tz Australia/Sydney --json
  errors[0].code    = PULSE_IMPORT_DST_AMBIGUOUS
  errors[0].message = row 81, column "batch_closed_at": local time "2026-04-05 02:00:00" in
                      Australia/Sydney is ambiguous: the zone shows it twice (a DST fall-back);
                      choose a DST policy (earlier or later) or give the value an offset

$ pulse import csv -i data/feeds/nyc_night_sort.csv ... --source-tz America/New_York
  error: PULSE_IMPORT_DST_NONEXISTENT: row 81, column "batch_closed_at": local time
         "2026-03-08 02:00:00" in America/New_York does not exist: the zone skips it (a DST spring-forward)
```

**Import with an explicit policy.** Choosing `--dst-policy earlier` (use the pre-transition offset) or `later` (use the post-transition offset) imports every row and reports what was resolved:

| feed | policy | rows imported | warning `PULSE_IMPORT_DST_RESOLVED` |
|---|---|---|---|
| SYD | earlier | 172 | 8 ambiguous, 0 nonexistent |
| SYD | later | 172 | 8 ambiguous, 0 nonexistent |
| NYC | earlier | 168 | 0 ambiguous, 4 nonexistent |
| NYC | later | 168 | 0 ambiguous, 4 nonexistent |

**Request: SYD fall-back night.** [`req/13_syd_dst_night.json`](req/13_syd_dst_night.json) has hourly slots for the Sydney night of 5 April and a nightly-total slot. The `FILTER_DATE_RANGES` uses a slot-level `"tz": "Australia/Sydney"` even in the UTC slots, so every slot selects the same night:

```json
{"label": "utc_hours_earlier", "cohort": {"filename": "syd_night_sort_earlier.pulse", "data_dir": "."},
 "time_zone": "UTC",
 "filterers": [{"type": "FILTER_DATE_RANGES", "field": "batch_closed_at", "tz": "Australia/Sydney",
                "params": {"ranges": [{"label": "dst_night", "start": "2026-04-05", "end": "2026-04-05"}]}}],
 "groups": [{"type": "GROUP_DATE", "field": "batch_closed_at", "params": {"component": "hour"}}],
 "aggregations": [{"type": "AGG_COUNT", "field": "parcels", "label": "batches"},
                  {"type": "AGG_SUM", "field": "parcels", "label": "parcels"}],
 "sort": [{"field": "batch_closed_at"}]}
```

**Result: batches per hour.**

| Sydney wall clock | batches (parcels) | UTC hour, `earlier` | batches | UTC hour, `later` | batches |
|---|---|---|---|---|---|
| 00:00 | 4 (469) | 13Z | 4 | 13Z | 4 |
| 01:00 | 4 (468) | 14Z | 4 | 14Z | 4 |
| **02:00** (twice) | **8 (1,012)** | **15Z** | **8** | 15Z | **0** (no bucket) |
| | | 16Z | **0** (no bucket) | **16Z** | **8** |
| 03:00 | 4 (444) | 17Z | 4 | 17Z | 4 |
| 04:00 | 4 (445) | 18Z | 4 | 18Z | 4 |
| 05:00 | 4 (476) | 19Z | 4 | 19Z | 4 |

**Result: nightly totals** (Sydney days, Apr 2–8): 24, 24, 24, **28**, 24, 24, 24 batches. The 5 April night has 28 batches (3,314 parcels) because it really had a 25-hour day.

**Request: NYC spring-forward night.** [`req/14_nyc_dst_night.json`](req/14_nyc_dst_night.json) is the same for New York on 8 March.

**Result.**

| New York wall clock | `earlier`: batches (parcels) | `later`: batches (parcels) |
|---|---|---|
| 00:00 | 4 (466) | 4 (466) |
| 01:00 | 4 (531) | **8 (1,008)** |
| 02:00 | no bucket (the hour does not exist) | no bucket |
| 03:00 | **8 (957)** | 4 (480) |
| 04:00 | 4 (434) | 4 (434) |
| 05:00 | 4 (480) | 4 (480) |

Nightly totals (New York days, Mar 5–11) are 24 batches every night, including the 8th.

**Reading.**

- On local wall-clock time Pulse never loses or double-counts a batch:
  - the repeated 02:00 hour is one bucket holding both passes (8 batches);
  - the skipped 02:00 hour has no bucket;
  - every night's total is preserved.
- The UTC rows expose what a naive feed has lost. Sydney's two passes through 02:00 were really an hour apart (15Z and 16Z). The feed's strings can't tell them apart, so either policy stacks all 8 into one UTC hour and leaves the other empty.
- For New York's four impossible stamps, `earlier` reads them as EST and lands them in 03:xx EDT. `later` reads them as EDT and lands them in 01:xx EST.

`pulse export csv --tz Australia/Sydney` makes the loss visible row by row. Under `earlier`, all eight `02:xx` rows render with `+11:00`; under `later`, with `+10:00`. The real data had four of each.

**Fix.** Ask both hubs to emit offsets (`2026-04-05T02:15:00+10:00`); Pulse then needs no policy. Until then, import with an explicit policy and record the `PULSE_IMPORT_DST_RESOLVED` counts as a data-quality metric.

## Findings and implications

1. **Report every warehouse on its own local calendar.** On UTC days the network shows 286 Sunday deliveries at sites that are closed on Sundays. Sydney's Saturday shows 124 deliveries instead of 241, and Los Angeles's Monday 434 instead of 523.
2. **Incidents land on the wrong day in UTC.** Sydney's Tue–Thu outage reads in UTC as Mon–Wed with a "partial recovery" on Thursday. The UTC view shows on-time falling from 90.6% to 49.1%, when on Sydney days it fell from 94.1% to 41.7%.
3. **Rolling overlays catch the outage, but only on local days.**
   - On Sydney days the outage is the strongest signal across 6 warehouses × 156 days: on-time z = −7.65 and index 44; duration z = 9.34 and index 204.
   - On UTC days the same screen fires on the wrong weekday, misses the worst day (z −1.23) and raises two false alarms on 2-delivery fragments.
   - Saturday volumes (8–12 parcels) generate most of the false alarms. Pair the z-score with the rolling index and a minimum daily volume before paging anyone.
4. **Only Bengaluru's improvement is a real trend** (Mann-Kendall Z = 4.98, Holm-adjusted p = 3.8 × 10⁻⁶; 77.0% → 94.7%). London and the other four sites also move between their first and last weeks, but none of those moves is significant (adjusted p ≥ 0.76). Test weekly or raw daily values, not a moving average, which inflates Z from 5.34 to 11.76.
5. **Build staffing rosters in local hours.** Each site's peak is a fixed local hour: SYD 08:00, ORD 09:00, NYC/LON/BLR 10:00, LAX 15:00. In UTC that peak moves by an hour at each DST change, at a different date per hemisphere.
6. **Naive-time feeds need a DST policy and should be fixed at source.** Pulse refuses 8 ambiguous Sydney stamps and 4 impossible New York stamps by default. It resolves them only when told how, and counts what it changed. Local nightly totals stay exact (28 batches on Sydney's 25-hour night), but the order inside the repeated hour cannot be recovered without offsets.

## Pulse features used

| Feature | Where |
|---|---|
| Request `time_zone` and slot-level `tz` (slot wins) | every step; `tz` on `FILTER_DATE_RANGES` / `GROUP_DATE_RANGES` in 12–14 |
| `GROUP_DATE` with `day_of_week`, `day`, `week`, `hour` in a zone | 01–03, 05–09, 13–14 |
| `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES` with local-day bounds | 03, 04, 12–14 |
| `ATTR_DATE_PART` `hour` on the local clock | 10–12 |
| Crosstab with margins, `include` ordering and `normalize: row` | 01, 02, 10–12 |
| Compose: one slot per warehouse and zone | 02–05, 07–09, 11–14 |
| `WIN_MOVING_AVG`, `WIN_LAG`, `WIN_PCT_CHANGE` | 06, 09 |
| `OVERLAY_ZSCORE_VS_ROLLING`, `OVERLAY_INDEX_VS_ROLLING_MEAN` | 05–07 |
| `TEST_TREND` (Mann-Kendall) as a post-test, with Holm `multiplicity` across Compose slots | 08, 09 |
| `AGG_COUNT`, `AGG_AVERAGE` (rate of a boolean), `AGG_SUM`, `AGG_PERCENTILE` | throughout |
| `import csv --source-tz` / `--dst-policy` with `PULSE_IMPORT_DST_*` codes | step 6 |
| `export csv --tz` (local-offset rendering) | step 6 |
| `api predict` echo of resolved zones (`time_zones[]`) | `run_all.sh` (06) |
