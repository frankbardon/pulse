---
name: categorical-fields-guide
description: When to use categorical fields, width selection, aggregator policy
type: guide
applies_to: process, compose, inspect, predict
---

# Categorical Fields Guide

## Overview

Categorical fields store string-valued data as dictionary-encoded integers. The dictionary maps each unique string to an integer index, and records store the index rather than the string. This provides compact storage and fast equality comparisons.

## When to Use Categorical Fields

Use categorical fields when:

- The field has a bounded set of distinct values (e.g., country codes, diagnosis types, treatment groups).
- You need to group or filter by exact string match.
- Storage efficiency matters (dictionary encoding is much smaller than repeated strings).

Do NOT use categorical fields when:

- The field has unbounded or very high cardinality (e.g., free-text names, UUIDs).
- The field values are numeric and will be used in arithmetic.
- The field is an identifier (unique per record).

## Width Selection

Pulse offers three categorical widths:

### categorical_u8

- **Max entries**: 256
- **Byte size**: 1 byte per record
- **Use when**: The field has fewer than 256 distinct values. This covers most categorical use cases (e.g., US states = 50, blood types = 8, Likert scales).

### categorical_u16

- **Max entries**: 65,536
- **Byte size**: 2 bytes per record
- **Use when**: The field has between 257 and 65,536 distinct values (e.g., ICD-10 diagnosis codes, ZIP codes).

### categorical_u32

- **Max entries**: 4,294,967,295
- **Byte size**: 4 bytes per record
- **Use when**: The field has more than 65,536 distinct values. This is rarely needed and should prompt reconsideration of whether categorical encoding is appropriate.

### Choosing the Right Width

1. Count the distinct values in your data.
2. Choose the narrowest width that fits: categorical_u8 > categorical_u16 > categorical_u32.
3. Add headroom for future values if the dictionary may grow.

If the import detects overflow, it emits `PULSE_IMPORT_CATEGORICAL_OVERFLOW`. If the sample suggests unbounded cardinality, it emits `PULSE_IMPORT_CATEGORICAL_UNBOUNDED`.

## Aggregator Policy

Categorical fields interact with aggregators as follows:

### Valid Aggregations

- **AGG_COUNT**: Counts non-null categorical values. Fully valid.
- **AGG_FREQUENCY**: Returns the distribution of category labels. This is the primary aggregation for categorical fields.

### Warned Aggregations

Numeric aggregations on categorical fields produce `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`:

- AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX, AGG_STDDEV, AGG_RANGE, AGG_ZSCORE

These operations execute on the integer dictionary indices, which are arbitrary. The results have no semantic meaning. Use `api predict` to catch these warnings before processing.

## Predict Warnings

When a request applies numeric aggregation to a categorical field, `api predict` emits:

```json
{
  "code": "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL",
  "message": "Numeric aggregation on categorical field has no semantic meaning",
  "field": "diagnosis_code"
}
```

Use `--strict` mode to treat this as an error.

## Formula Usage

In ATTR_FORMULA expressions, categorical fields provide their string label, not the integer index. This allows comparisons like:

```
if(treatment_group == "Control", 0, 1)
```

Arithmetic on categorical fields in formulas is not supported; use string comparisons.

## Group-By

GROUP_CATEGORY is the natural grouper for categorical fields. It uses the dictionary to resolve group keys to human-readable string labels. This is more readable than grouping by integer index.

Example:
```json
{"type": "GROUP_CATEGORY", "field": "department"}
```

Produces groups named by the department string values, not by their dictionary indices.

## Inspecting Categorical Fields

Use `pulse cohort inspect --full-dict` to see the full dictionary for categorical fields. This shows every string-to-index mapping and helps verify the encoding.
