---
name: aggregation-guide
description: When to use each of the 9 aggregators
type: guide
applies_to: process, compose, predict
---

# Aggregation Guide

## Overview

Pulse provides 9 aggregation operations that compute summary statistics over filtered, grouped records. Each aggregator has specific semantics for null handling and categorical field behavior.

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
| AGG_FREQUENCY | Counts nulls separately |
| AGG_ZSCORE | Produces null output for null input |

## Categorical Field Warnings

When a numeric aggregation (SUM, AVERAGE, MIN, MAX, STDDEV, RANGE, ZSCORE) is applied to a categorical field, Pulse emits the warning code `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`. The operation will still execute (operating on the dictionary index integers), but the results have no semantic meaning.

Use `AGG_COUNT` or `AGG_FREQUENCY` for categorical fields.
