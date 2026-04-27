---
name: statistical-concepts
description: Null handling, when to filter vs impute, sampling caveats
type: guide
applies_to: process, compose, predict
---

# Statistical Concepts

## Overview

Pulse performs statistical computations on cohort data. Understanding how nulls, sampling, and categorical fields interact with statistics is essential for producing valid analyses.

## Null Handling

Pulse uses type-level nullability. Nullable types (`nullable_bool`, `nullable_u4`, `nullable_u8`, `nullable_u16`) can represent missing values; all other types cannot.

### Null Exclusion in Aggregations

All aggregations except AGG_FREQUENCY exclude null values by default:

- **AGG_COUNT**: Counts non-null values only.
- **AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX, AGG_STDDEV, AGG_RANGE**: Operate on non-null values only.
- **AGG_ZSCORE**: Produces null output for null input; uses non-null values for mean/stddev calculation.
- **AGG_FREQUENCY**: Includes a separate count for null values.

This means aggregation results are computed over the "complete cases" for each field independently.

### Null Propagation in Attributes

Attribute computations propagate nulls:

- If the input field value is null, the derived attribute value is null.
- For ATTR_FORMULA, if any referenced field is null, the formula result is null.

## Filter vs Impute

When your data has missing values, you have two strategies:

### Filtering

Use filters to exclude records with null values before aggregation:

```json
{"type": "FILTER_EXPRESSION", "expression": "age != null"}
```

**When to filter**:
- Missing values are not informative (e.g., data entry errors)
- You need complete-case analysis
- The proportion of missing data is small

### Imputation

Pulse does not perform automatic imputation. If you need imputed values, prepare the data before import:

1. Replace missing values with a suitable default (mean, median, mode)
2. Use a non-nullable type for the imputed field
3. Consider adding a companion boolean field to track which values were imputed

**When to impute**:
- Missing values would significantly reduce your sample size
- The missingness pattern is random (MCAR or MAR)
- You need every record to contribute to the analysis

## Sampling Caveats

### Schema Inference Sampling

During import, Pulse samples rows to infer types. This sampling has implications:

- **Small samples may miss rare values.** A column that looks like u8 in the sample might need u16 for the full dataset.
- **Categorical cardinality** is estimated from the sample. Unbounded cardinality triggers `PULSE_IMPORT_CATEGORICAL_UNBOUNDED`.
- **Increase `--sample-rows`** for more reliable inference on heterogeneous data.

### Processing Sampling

Pulse processes all records in a cohort (no sampling during aggregation). The `sample` command returns a random subset of rows for inspection but is not used in aggregation pipelines.

### Statistical Significance

Pulse computes descriptive statistics but does not perform inferential statistical tests. When interpreting results:

- Small group sizes produce unreliable aggregations (e.g., AVERAGE of 3 values).
- Standard deviation with small N is unstable.
- Z-scores assume approximately normal distributions.

## Categorical Numeric Meaninglessness

Categorical fields store string values as integer dictionary indices. Performing numeric operations on these indices is meaningless because:

1. The mapping from string to integer is arbitrary (insertion order).
2. Arithmetic on indices does not correspond to any semantic operation.
3. "Average of category indices" is not a valid statistic.

Pulse emits `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` when numeric aggregations are applied to categorical fields.

Valid operations on categorical fields:
- **AGG_COUNT**: How many records have a value?
- **AGG_FREQUENCY**: What is the distribution of categories?
- **GROUP_CATEGORY**: Partition records by category for cross-tabulation.

Invalid (but technically executable) operations:
- AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX, AGG_STDDEV, AGG_RANGE, AGG_ZSCORE on categorical fields.
