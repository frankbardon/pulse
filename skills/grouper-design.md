---
name: grouper-design
description: GROUP_CATEGORY, GROUP_DATE, GROUP_QUANTILE, GROUP_RANGE, and GROUP_ROUNDED, nesting semantics
type: guide
applies_to: process, compose, predict
---

# Grouper Design

## Overview

Groupers partition records into groups before aggregation. Each group is aggregated independently, and the results include group keys identifying which partition each result belongs to.

## Group Types

### GROUP_CATEGORY

Groups records by the distinct values of a field. Each unique value becomes a separate group.

- **Fields**: Works with any field type but is most natural with categorical fields.
- **Categorical fields**: The group key is the category string label, not the dictionary index. This produces human-readable output keys.
- **Numeric fields**: Each distinct numeric value becomes a group. This can produce many groups if the field has high cardinality.
- **Null handling**: Records with null values form their own group (the "null group").
- **Use when**: You want to segment results by category (e.g., by department, by diagnosis code, by treatment group).

### GROUP_RANGE

Groups records into fixed-width numeric bins with human-readable range keys. Each key shows the bin boundaries as `"low-high"`, where the bin is `[low, high)` (inclusive lower bound, exclusive upper bound).

- **Fields**: Requires a numeric field (integer or floating point).
- **Interval**: The bin width. For example, interval=10 with values 0-100 produces keys like `"0-10"`, `"10-20"`, ..., `"90-100"`.
- **Formula**: `low = floor(value / interval) * interval`, `high = low + interval`.
- **Key format**: If the interval is integer-valued (e.g., 10.0), keys use integer format (`"0-10"`). If the interval is fractional (e.g., 0.5), keys use float format (`"0-0.5"`, `"0.5-1"`).
- **Bin boundaries**: Bins are half-open intervals `[low, high)`. A value of exactly 10 with interval 10 falls into the `"10-20"` bin, not `"0-10"`.
- **Null handling**: Records with null values are skipped (not grouped).
- **Use when**: You want human-readable range labels in output (e.g., `"20-30"` instead of just `"20"`). Ideal for reports, dashboards, and data summaries where the bin width should be explicit in the key.
- **vs GROUP_ROUNDED**: GROUP_ROUNDED produces a single number as the key (the bin's lower bound). GROUP_RANGE produces a `"low-high"` range string. Both use the same binning formula; the difference is only in key formatting.

### GROUP_DATE

Groups records by a date component extracted from a `date` type field. The field value is interpreted as epoch days (days since Unix epoch) and converted to a timestamp for component extraction.

- **Fields**: Requires a `date` type field.
- **Params**: `{"component": "<component>"}` where component is one of: `year`, `quarter`, `month`, `week`, `day`, `day_of_week`. Defaults to `month` if params are omitted.
- **Key format**:
  - `year` → `"2024"`
  - `quarter` → `"2024-Q1"`
  - `month` → `"2024-01"`
  - `week` → `"2024-W03"` (ISO week)
  - `day` → `"2024-01-15"`
  - `day_of_week` → `"Monday"`
- **Null handling**: Records with null values are skipped (not grouped).
- **Use when**: You want to segment time-series data by calendar periods (e.g., monthly enrollment counts, quarterly revenue, day-of-week patterns).

### GROUP_QUANTILE

Groups records into equal-sized quantile buckets based on rank position within a numeric field. Records are sorted by value and then divided into N buckets of approximately equal size.

- **Fields**: Requires a numeric field (integer or floating point). Not applicable to categorical fields.
- **Interval**: Reused as the bucket count. Common values: 4 (quartiles), 10 (deciles), 100 (percentiles). Defaults to 4 if not specified or zero.
- **Key prefix convention**:
  - 4 buckets: `Q1`, `Q2`, `Q3`, `Q4` (quartiles)
  - 10 buckets: `D1`, `D2`, ..., `D10` (deciles)
  - 100 buckets: `P1`, `P2`, ..., `P100` (percentiles)
  - Any other N: `B1`, `B2`, ..., `BN` (generic buckets)
- **Bucket assignment**: `bucket = rank * buckets / n` (integer math), clamped to `buckets - 1`. Rank is the zero-based position in the sorted order.
- **Null handling**: Records with null values are skipped (not grouped).
- **Uneven distribution**: When the record count is not evenly divisible by the bucket count, some buckets will have one more record than others.
- **Use when**: You want to partition records by their relative position in the distribution (e.g., top quartile vs bottom quartile, decile analysis).

### GROUP_ROUNDED

Groups records by rounding a numeric field to a specified interval. The group key is the rounded value.

- **Fields**: Requires a numeric field (integer or floating point).
- **Interval**: The rounding interval determines the bucket width. For example, interval=10 with values 0-100 produces groups 0, 10, 20, ..., 100.
- **Formula**: `floor(value / interval) * interval`
- **Null handling**: Records with null values form their own group.
- **Categorical fields**: Not applicable. Using GROUP_ROUNDED on a categorical field will produce an error because rounding string-encoded integers is meaningless.
- **Use when**: You want to bucket continuous values into ranges (e.g., age groups by decade, score bands).

## Nesting

Multiple groupers can be specified in a single request. When more than one grouper is present, they create a nested (cross-product) grouping:

1. The first grouper partitions all records.
2. The second grouper further partitions each first-level group.
3. And so on.

The result keys are compound, containing one key per grouper level.

**Example**: Grouping by `GROUP_CATEGORY` on "department" then `GROUP_ROUNDED` on "age" with interval 10 produces groups like:

- department=Engineering, age=20
- department=Engineering, age=30
- department=Sales, age=20
- department=Sales, age=30

## Categorical Field Grouping

When using GROUP_CATEGORY on a categorical field, Pulse resolves the dictionary to produce string group keys. This means the output uses the human-readable category labels rather than internal integer codes.

If the categorical dictionary has unused entries (categories defined but not present in the data), those categories do not appear as groups.

## Output Key Resolution

Each grouper contributes a key to the result. The key name defaults to the field name. For nested groupers, keys are disambiguated by field name.

Group keys appear in the response alongside aggregation values, making it possible to reconstruct the full grouping hierarchy from flat JSON output.
