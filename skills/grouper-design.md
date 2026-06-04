---
name: grouper-design
description: Pick a grouper — GROUP_CATEGORY, GROUP_DATE (day/week/month/year/quarter), GROUP_RANGE (intervals), GROUP_ROUNDED, GROUP_QUANTILE. Use when bucketing rows, nesting groupers for cross-tabs, or choosing between range and quantile bins.
type: guide
applies_to: process, compose, predict
---

# Grouper Design

<skill_overview>
Groupers partition records into groups before aggregation; each group is aggregated independently and the output rows include a key column identifying the partition. Invoke this skill when choosing a grouper type, configuring its params, or reasoning about how group keys appear in output.
</skill_overview>

<reference>
## GROUP_CATEGORY

Groups records by the distinct values of a field. Each unique value becomes a separate group.

- **Fields**: Works with any field type but is most natural with categorical fields. For categorical fields, the dictionary is resolved so the group key is the human-readable category label, not the integer code. Categorical entries defined in the dictionary but not present in any record do not appear as groups.
- **Numeric fields**: Each distinct numeric value becomes a group. High-cardinality numeric fields produce many groups.
- **Null handling**: Records with null values are skipped (not grouped).
- **Use when**: You want to segment results by category (e.g., by department, by diagnosis code, by treatment group).
- **Config**: No extra params; only `field` is required.
</reference>

<example name="group-category">
Group records by department.

```json
{
  "groups": [
    {"type": "GROUP_CATEGORY", "field": "department"}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id"}
  ]
}
```
</example>

<reference>
## GROUP_RANGE

Groups records into fixed-width numeric bins with human-readable range keys. Each key shows the bin boundaries as `"low-high"`, where the bin is `[low, high)` (inclusive lower bound, exclusive upper bound).

- **Fields**: Requires a numeric field (integer or floating point).
- **Config**: `interval` (float, defaults to 1 if zero or negative) — the bin width. For example, interval=10 with values 0-100 produces keys like `"0-10"`, `"10-20"`, ..., `"90-100"`.
- **Formula**: `low = floor(value / interval) * interval`, `high = low + interval`.
- **Key format**: If `low` and `high` are integer-valued, keys use integer format (`"0-10"`); otherwise float format (`"0-0.5"`, `"0.5-1"`).
- **Bin boundaries**: Half-open `[low, high)`. A value of exactly 10 with interval 10 falls into the `"10-20"` bin, not `"0-10"`.
- **Null handling**: Records with null values are skipped (not grouped).
- **vs GROUP_ROUNDED**: GROUP_ROUNDED keys are a single number (the bin's lower bound). GROUP_RANGE keys are a `"low-high"` string. Same binning formula, different key formatting.
</reference>

<example name="group-range">
Bin ages into 10-year ranges.

```json
{
  "groups": [
    {"type": "GROUP_RANGE", "field": "age", "interval": 10}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id"}
  ]
}
```
</example>

<reference>
## GROUP_DATE

Groups records by a date component extracted from a `date` type field. The field value is interpreted as epoch days since Unix epoch (1970-01-01) and converted to a UTC timestamp for component extraction.

- **Fields**: Requires a `date` type field.
- **Config**: `params` is a JSON object `{"component": "<component>", "fiscal_offset": <int>}`. `component` is one of `year`, `quarter`, `month`, `week`, `day`, `day_of_week` and defaults to `month`. `fiscal_offset` is optional and defaults to `0`.
- **Key format (calendar)**:
  - `year` → `"2024"`
  - `quarter` → `"2024-Q1"`
  - `month` → `"2024-01"`
  - `week` → `"2024-W03"` (ISO week)
  - `day` → `"2024-01-15"`
  - `day_of_week` → `"Monday"`
- **Fiscal offset**: `fiscal_offset` is the number of months after January that the fiscal year starts; valid range `[-11, 11]`. Only meaningful with `component=year` or `component=quarter` — combining `fiscal_offset != 0` with any other component is rejected with `PROCESSING_CONFIG`. Offsets normalise modulo 12, so `fiscal_offset=-3` and `fiscal_offset=9` both pin an October-start fiscal year. `fiscal_offset=0` is byte-identical to the no-offset path (calendar year, no `FY` prefix).
- **Key format (fiscal, end-year convention)**: a fiscal year is labelled by the calendar year in which it ENDS. The April-start fiscal year running Apr 2024 → Mar 2025 emits `FY2025`; an October-start fiscal year running Oct 2023 → Sep 2024 emits `FY2024`.
  - `year` + fiscal → `"FY2025"`
  - `quarter` + fiscal → `"FY2025-Q1"` (Q1 is the first three months of the fiscal year, NOT the first three calendar months)
- **Null handling**: Records with null values are skipped (not grouped).
- **Use when**: You want to segment time-series data by calendar periods (e.g., monthly enrollment counts, quarterly revenue, day-of-week patterns), or by fiscal periods that do not align with the calendar year (UK April-start `fiscal_offset=3`, US Federal October-start `fiscal_offset=9` or equivalently `-3`).
</reference>

<example name="group-date">
Group enrollments by month.

```json
{
  "groups": [
    {"type": "GROUP_DATE", "field": "enrolled_on", "params": {"component": "month"}}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id"}
  ]
}
```
</example>

<example name="group-date-fiscal-quarter">
Group revenue by fiscal quarter under a UK-style April-start fiscal year.
Keys emit as `FY2025-Q1` (Apr-Jun 2024), `FY2025-Q2` (Jul-Sep 2024), etc.

```json
{
  "groups": [
    {"type": "GROUP_DATE", "field": "booked_on", "params": {"component": "quarter", "fiscal_offset": 3}}
  ],
  "aggregations": [
    {"type": "AGG_SUM", "field": "revenue"}
  ]
}
```
</example>

<example name="group-date-fiscal-year-us-federal">
Group obligations by US Federal fiscal year (October start). `fiscal_offset=9`
and `fiscal_offset=-3` are equivalent. Keys emit as `FY2024`, `FY2025`, ...

```json
{
  "groups": [
    {"type": "GROUP_DATE", "field": "obligated_on", "params": {"component": "year", "fiscal_offset": 9}}
  ],
  "aggregations": [
    {"type": "AGG_SUM", "field": "amount"}
  ]
}
```
</example>

<reference>
## GROUP_QUANTILE

Groups records into equal-sized quantile buckets based on rank position within a numeric field. Records are sorted by value and divided into N buckets of approximately equal size.

- **Fields**: Requires a numeric field. Not applicable to categorical fields.
- **Config**: `interval` is reused as the bucket count. Common values: 4 (quartiles), 10 (deciles), 100 (percentiles). Defaults to 4 if not specified or zero.
- **Key prefix convention**:
  - 4 buckets: `Q1`, `Q2`, `Q3`, `Q4` (quartiles)
  - 10 buckets: `D1`, `D2`, ..., `D10` (deciles)
  - 100 buckets: `P1`, `P2`, ..., `P100` (percentiles)
  - Any other N: `B1`, `B2`, ..., `BN` (generic buckets)
- **Bucket assignment**: `bucket = rank * buckets / n` (integer math), clamped to `buckets - 1`. Rank is the zero-based position in the sorted order.
- **Null handling**: Records with null values are skipped (not grouped).
- **Uneven distribution**: When the record count is not evenly divisible by the bucket count, some buckets will have one more record than others.
- **Use when**: You want to partition records by relative position in the distribution (e.g., top quartile vs bottom quartile, decile analysis).
</reference>

<example name="group-quantile">
Partition records into income quartiles.

```json
{
  "groups": [
    {"type": "GROUP_QUANTILE", "field": "income", "interval": 4}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id"}
  ]
}
```
</example>

<reference>
## GROUP_ROUNDED

Groups records by rounding a numeric field down to a specified interval. The group key is the rounded value as a string.

- **Fields**: Requires a numeric field. Not applicable to categorical fields (rounding string-encoded integers is meaningless and will produce errors at runtime).
- **Config**: `interval` (float, defaults to 1 if zero or negative) — the rounding interval determines the bucket width. For example, interval=10 with values 0-100 produces keys `"0"`, `"10"`, `"20"`, ..., `"100"`.
- **Formula**: `floor(value / interval) * interval`.
- **Key format**: Integer string when the rounded value is integer-valued, otherwise minimal float string.
- **Null handling**: Records with null values are skipped (not grouped).
- **Use when**: You want to bucket continuous values into ranges (e.g., age groups by decade, score bands).
</reference>

<example name="group-rounded">
Bucket scores into bands of 10.

```json
{
  "groups": [
    {"type": "GROUP_ROUNDED", "field": "score", "interval": 10}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id"}
  ]
}
```
</example>

<rule severity="must" topic="single-grouper-only">
The current processor honors a single grouper per request: only `req.Groups[0]` is used. Additional entries in the `groups` array are ignored. To compute multi-level breakdowns, run separate `process` requests (one per grouping level) or compose them via `compose`.
</rule>

<reference>
## Output Key Resolution

Each output row contains the aggregation values plus one extra key whose name is the grouper's `field` and whose value is the group key string (e.g., `"department": "Engineering"`). Reserve aggregation `label`s that do not collide with the group field name.
</reference>

