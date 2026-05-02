---
name: grouper-design
description: GROUP_CATEGORY, GROUP_DATE, GROUP_H3_CELL, GROUP_QUANTILE, GROUP_RANGE, GROUP_ROUNDED — nesting semantics
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
- **Config**: `params` is a JSON object `{"component": "<component>"}` where component is one of: `year`, `quarter`, `month`, `week`, `day`, `day_of_week`. Defaults to `month` if `params` is omitted.
- **Key format**:
  - `year` → `"2024"`
  - `quarter` → `"2024-Q1"`
  - `month` → `"2024-01"`
  - `week` → `"2024-W03"` (ISO week)
  - `day` → `"2024-01-15"`
  - `day_of_week` → `"Monday"`
- **Null handling**: Records with null values are skipped (not grouped).
- **Use when**: You want to segment time-series data by calendar periods (e.g., monthly enrollment counts, quarterly revenue, day-of-week patterns).
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

<reference>
## GROUP_H3_CELL

Buckets records into Uber H3 hexagonal grid cells. Group keys are 15-character lowercase hex H3 indices.

- **Fields**: `point_f64` (resolution param **required**, points convert at run time) or `h3_cell` (resolution param **optional**; defaults to the cell's native resolution; supplying a coarser resolution walks parents).
- **Config**: `params.resolution` (int 0–15). Higher = finer cells. See `skills/geospatial-cohorts.md` for the resolution table.
- **Errors**: `PULSE_GEO_INVALID_RESOLUTION` for out-of-range or finer-than-native; `PULSE_GEO_INVALID_POINT` for unparseable points.
- **Use when**: You want to bin geographic activity into hexagonal cells for heat maps, density analysis, or geospatial joins.
</reference>

<example name="group-h3-cell">
Count rides per H3 cell at resolution 9.

```json
{
  "groups": [
    {"type": "GROUP_H3_CELL", "field": "pickup_location", "params": {"resolution": 9}}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "ride_id"}
  ]
}
```
</example>
