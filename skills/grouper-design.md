---
name: grouper-design
description: GROUP_CATEGORY vs GROUP_ROUNDED, nesting semantics
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
