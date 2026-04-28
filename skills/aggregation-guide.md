---
name: aggregation-guide
description: When to use each of the 16 aggregators
type: guide
applies_to: process, compose, predict
---

# Aggregation Guide

## Overview

Pulse provides 16 aggregation operations that compute summary statistics over filtered, grouped records. Each aggregator has specific semantics for null handling and categorical field behavior.

## Aggregators

### AGG_COUNT

Counts the number of non-null values in a field. If applied to a non-nullable field, this equals the number of records in the group.

- **Use when**: You need to know how many records have a value for a field.
- **Null handling**: Null values are excluded from the count.
- **Categorical**: Valid. Counts the number of records with a category assigned.

### AGG_SUM

Computes the arithmetic sum of all non-null values in a numeric field.

- **Use when**: You need a total (e.g., total revenue, total hours).
- **Null handling**: Null values are skipped.
- **Categorical**: Not meaningful. Summing category indices has no semantic value. Pulse will emit a `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` warning.

### AGG_AVERAGE

Computes the arithmetic mean of all non-null values. Equivalent to SUM / COUNT.

- **Use when**: You need a central tendency measure for numeric data.
- **Null handling**: Null values are excluded from both numerator and denominator.
- **Categorical**: Not meaningful. Averaging category indices produces nonsensical results.

### AGG_MIN

Returns the minimum non-null value in a numeric field.

- **Use when**: You need the smallest observed value (e.g., lowest score, earliest date).
- **Null handling**: Null values are ignored.
- **Categorical**: Not meaningful for semantic purposes. The minimum dictionary index does not correspond to a meaningful ordering unless categories are ordinal.

### AGG_MAX

Returns the maximum non-null value in a numeric field.

- **Use when**: You need the largest observed value (e.g., highest score, latest date).
- **Null handling**: Null values are ignored.
- **Categorical**: Not meaningful. Same caveat as AGG_MIN.

### AGG_STDDEV

Computes the population standard deviation of all non-null values.

- **Use when**: You need to measure spread or variability in numeric data.
- **Null handling**: Null values are excluded from the calculation.
- **Categorical**: Not meaningful. Standard deviation of category indices is semantically meaningless.
- **Formula**: `sqrt(sum((x - mean)^2) / N)`

### AGG_RANGE

Computes the difference between the maximum and minimum non-null values: `MAX - MIN`.

- **Use when**: You need to know the spread of values in a field.
- **Null handling**: Null values are excluded.
- **Categorical**: Not meaningful.

### AGG_FREQUENCY

Returns a frequency distribution (histogram) of values in a field. For numeric fields, counts occurrences of each distinct value. For categorical fields, counts occurrences of each category label.

- **Use when**: You need a distribution breakdown or want to see the most common values.
- **Null handling**: Null values get their own count entry.
- **Categorical**: Valid and recommended. This is the primary aggregation for understanding categorical distributions.

### AGG_MEDIAN

Returns the middle value of sorted non-null numeric values. For even-length sets, returns the average of the two middle values.

- **Use when**: You need a central tendency measure that is robust to outliers, unlike the arithmetic mean.
- **Null handling**: Null values are excluded before sorting.
- **Categorical**: Not meaningful. The median of dictionary indices has no semantic value. Pulse will emit a `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` warning.

### AGG_VARIANCE

Computes the population variance of all non-null values. This is the square of the standard deviation (stddev^2).

- **Use when**: You need to measure the dispersion of numeric data, especially when comparing variability across datasets or when variance is needed directly (e.g., for statistical tests, ANOVA).
- **Null handling**: Null values are excluded from the calculation.
- **Categorical**: Not meaningful. Variance of category indices is semantically meaningless. Pulse will emit a `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` warning.
- **Formula**: `sum((x - mean)^2) / N`

### AGG_MODE

Returns the most frequently occurring non-null value. When multiple values tie for highest frequency, returns the smallest value (deterministic tie-breaking).

- **Use when**: You need to find the most common value in a field, such as the most popular category or the most frequent measurement.
- **Null handling**: Null values are excluded from frequency counting.
- **Categorical**: Valid. Mode is the natural summary statistic for categorical data, identifying the most common category.

### AGG_SKEWNESS

Computes the population skewness of all non-null values, measuring the asymmetry of the distribution. Positive skewness indicates a longer right tail, negative skewness indicates a longer left tail, and zero indicates symmetry.

- **Use when**: You need to assess whether a distribution is symmetric or skewed, such as detecting outlier-heavy tails in financial or measurement data.
- **Null handling**: Null values are excluded from the calculation.
- **Categorical**: Not meaningful. Skewness of category indices is semantically meaningless. Pulse will emit a `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` warning.
- **Formula**: `(1/N) * sum(((x - mean) / stddev)^3)`
- **Edge cases**: Returns 0 for empty sets, single values, or when all values are identical (stddev = 0).

### AGG_KURTOSIS

Computes the excess kurtosis of all non-null values, measuring the tail heaviness of the distribution relative to a normal distribution. Positive kurtosis (leptokurtic) indicates heavy tails, negative kurtosis (platykurtic) indicates light tails, and zero indicates normal-like tails.

- **Use when**: You need to assess whether a distribution has heavier or lighter tails than a normal distribution, such as detecting extreme outlier risk in financial data or measurement quality.
- **Null handling**: Null values are excluded from the calculation.
- **Categorical**: Not meaningful. Kurtosis of category indices is semantically meaningless. Pulse will emit a `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` warning.
- **Formula**: `((1/N) * sum(((x - mean) / stddev)^4)) - 3`
- **Edge cases**: Returns 0 for empty sets, single values, or when all values are identical (stddev = 0).

### AGG_DISTINCT_COUNT

Counts the number of unique non-null values in a field. Unlike AGG_COUNT which counts all non-null values, AGG_DISTINCT_COUNT deduplicates before counting.

- **Use when**: You need to know how many unique values exist in a field (e.g., number of distinct categories, unique patient IDs, unique scores).
- **Null handling**: Null values are excluded before counting unique values.
- **Categorical**: Valid. Counts the number of distinct categories present in the data.

### AGG_PERCENTILE

Returns the value at a given percentile rank using linear interpolation. This is the first aggregator that accepts configuration parameters via the `params` field.

- **Use when**: You need a specific quantile value (e.g., 90th percentile response time, 25th percentile score). More flexible than AGG_MEDIAN, which is fixed at the 50th percentile.
- **Null handling**: Null values are excluded before sorting and interpolation.
- **Categorical**: Not meaningful. Percentile of category indices is semantically meaningless. Pulse will emit a `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` warning.
- **Params**: Requires a `params` JSON object with a `percentile` field (float64, 0-100). If `params` is omitted, defaults to 50 (equivalent to median).
  ```json
  {"percentile": 90}
  ```
- **Formula**: Linear interpolation: `rank = percentile / 100 * (N - 1)`, then interpolate between `vals[floor(rank)]` and `vals[ceil(rank)]`.
- **Edge cases**: Returns 0 for empty sets. Returns the single value for single-element sets regardless of percentile. Returns an error if percentile is outside [0, 100].

### AGG_ZSCORE

Computes the z-score (standard score) for each value relative to the field's mean and standard deviation. This is an aggregation that produces per-record output rather than a single summary value.

- **Use when**: You need to identify outliers or normalize values for comparison across fields.
- **Null handling**: Null values produce null z-scores.
- **Categorical**: Not meaningful.
- **Formula**: `(x - mean) / stddev`

## Null Handling Summary

| Aggregator | Null Behavior |
|------------|---------------|
| AGG_COUNT | Excludes nulls |
| AGG_SUM | Skips nulls |
| AGG_AVERAGE | Excludes from both numerator and denominator |
| AGG_MIN | Ignores nulls |
| AGG_MAX | Ignores nulls |
| AGG_STDDEV | Excludes nulls |
| AGG_RANGE | Excludes nulls |
| AGG_VARIANCE | Excludes nulls |
| AGG_FREQUENCY | Counts nulls separately |
| AGG_MODE | Excludes nulls |
| AGG_MEDIAN | Excludes nulls |
| AGG_SKEWNESS | Excludes nulls |
| AGG_KURTOSIS | Excludes nulls |
| AGG_DISTINCT_COUNT | Excludes nulls |
| AGG_PERCENTILE | Excludes nulls |
| AGG_ZSCORE | Produces null output for null input |

## Categorical Field Warnings

When a numeric aggregation (SUM, AVERAGE, MIN, MAX, STDDEV, RANGE, VARIANCE, MEDIAN, PERCENTILE, SKEWNESS, KURTOSIS, ZSCORE) is applied to a categorical field, Pulse emits the warning code `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`. The operation will still execute (operating on the dictionary index integers), but the results have no semantic meaning.

Use `AGG_COUNT`, `AGG_FREQUENCY`, `AGG_MODE`, or `AGG_DISTINCT_COUNT` for categorical fields.
