---
name: attribute-composition
description: z-score, t-score, normalized, formula, date-part — composition rules
type: guide
applies_to: process, compose, predict
---

# Attribute Composition

## Overview

Attributes are derived values computed per-record from existing fields. They extend the output with calculated columns without modifying the underlying cohort data.

## Attribute Types

### ATTR_ZSCORE

Computes the z-score (standard score) for each record's value on a given field.

- **Formula**: `(value - mean) / stddev`
- **Output**: A floating-point value indicating how many standard deviations from the mean.
- **Null handling**: Null input produces null output.
- **Categorical**: Not applicable; z-scores require numeric input.
- **Use when**: Comparing values across different scales or identifying outliers.

### ATTR_TSCORE

Computes the t-score for each record, which is a linear transformation of the z-score.

- **Formula**: `(z * 10) + 50`
- **Output**: A floating-point value centered at 50 with standard deviation of 10.
- **Null handling**: Null input produces null output.
- **Categorical**: Not applicable.
- **Use when**: You want a normalized score without negative values, commonly used in psychometrics.

### ATTR_NORMALIZED

Normalizes each value to a 0..1 range using min-max normalization.

- **Formula**: `(value - min) / (max - min)`
- **Output**: A floating-point value between 0 and 1 inclusive.
- **Null handling**: Null input produces null output. If max equals min, output is 0.
- **Categorical**: Not applicable.
- **Use when**: You need values on a common scale for comparison or visualization.

### ATTR_FORMULA

Computes a derived value using a runtime expression that can reference any field in the record.

- **Expression**: A string expression evaluated per-record. Supports arithmetic operators (+, -, *, /), field references by name, and standard math functions.
- **Output**: The result of evaluating the expression.
- **Null handling**: If any referenced field is null, the result is null.
- **Categorical**: Categorical fields can be accessed in formulas. When referenced, the categorical string label is available for string operations, not the dictionary index.
- **Use when**: You need custom derived fields like BMI = weight / (height * height), or composite scores.

### ATTR_PERCENTILE

Computes the percentile rank of each record's value within the field distribution.

- **Formula**: `(count of values <= x) / total count * 100`
- **Output**: A floating-point value between 0 and 100.
- **Null handling**: Null input produces null output.
- **Categorical**: Not applicable.
- **Use when**: You need to know where a value falls in the distribution (e.g., "this score is in the 85th percentile").

### ATTR_RANK

Computes the ordinal rank of each record's value within the field.

- **Output**: An integer rank (1 = smallest value).
- **Null handling**: Null values receive null rank.
- **Categorical**: Not applicable.
- **Use when**: You need ordered ranking for leaderboards or stratification.

### ATTR_DATE_PART

Extracts a date component from a date field and converts it to an integer value suitable for grouping.

- **Params**: `{"part": "<part>"}` where part is one of: `year`, `month`, `day`, `year_month`, `year_month_day`, `month_day`.
- **Output formats**:
  - `year` → YYYY (e.g., 2024)
  - `month` → M (e.g., 3 for March, no zero-padding)
  - `day` → D (e.g., 15, no zero-padding)
  - `year_month` → YYYYM[M] (e.g., 202403)
  - `year_month_day` → YYYYM[M]DD (e.g., 20240315)
  - `month_day` → M[M]DD (e.g., 315 for March 15, 1201 for December 1)
- **Input**: Must be a `date` field. Errors on non-date fields with `PROCESSING_CONFIG`.
- **Null handling**: Null date values produce 0.
- **Use when**: You need to group or aggregate records by date components (e.g., group by year-month to see monthly trends).

## Composition Rules

1. **Attributes are computed after aggregation and grouping.** They operate on the final record set.
2. **Multiple attributes can reference the same source field.** For example, you can compute both z-score and percentile for the same field.
3. **Attributes cannot reference other attributes.** There is no chaining; each attribute reads from the original data.
4. **Labels must be unique.** Each attribute must have a distinct label in the output.
5. **Formula expressions** are evaluated in a sandboxed environment with access only to field values from the current record.

## Categorical String Access

When a formula references a categorical field, the expression environment provides the string label (not the integer index). This allows expressions like:

```
if(category == "Group A", 1, 0)
```

The dictionary lookup is performed automatically before expression evaluation.
