---
name: aggregation-guide
description: When to use each of the 16 aggregators
type: guide
applies_to: process, compose, predict
---

# Aggregation Guide

## Aggregators (16)

| Type | Meaning | Input |
|------|---------|-------|
| AGG_COUNT | Number of non-null values. | any |
| AGG_DISTINCT_COUNT | Number of distinct non-null values. | any |
| AGG_FREQUENCY | Highest frequency count among observed values. | any |
| AGG_MODE | Most common value (smallest on tie). | any |
| AGG_SUM | Arithmetic sum. | numeric |
| AGG_AVERAGE | Arithmetic mean (`SUM / COUNT`). | numeric |
| AGG_MIN | Smallest value. | numeric |
| AGG_MAX | Largest value. | numeric |
| AGG_RANGE | `MAX - MIN`. | numeric |
| AGG_MEDIAN | 50th percentile (linear interpolation). | numeric |
| AGG_PERCENTILE | Value at `percentile` rank, linear interpolation. | numeric |
| AGG_STDDEV | Population standard deviation. | numeric |
| AGG_VARIANCE | Population variance (`STDDEV^2`). | numeric |
| AGG_SKEWNESS | Population skewness (asymmetry). | numeric |
| AGG_KURTOSIS | Excess kurtosis (tail heaviness vs normal). | numeric |
| AGG_ZSCORE | Mean of per-value z-scores over the group (always ~0 by construction; useful as a sentinel). | numeric |

## Notes on non-obvious aggregators

- **AGG_PERCENTILE** takes a `params` object with key `percentile` (float, 0-100). Defaults to 50 (median) if `params` is omitted; out-of-range values produce `PROCESSING_CONFIG`.
- **AGG_ZSCORE** computes `(x - mean) / stddev` for each value, then returns the mean of those z-scores. By definition this is ~0 for any non-degenerate group; use ATTR_ZSCORE for per-record scores instead.
- **AGG_FREQUENCY** returns the count of the most-frequent value (not a histogram). Nulls are skipped before counting; they do not get their own bucket. Use AGG_MODE if you want the value itself.
- **AGG_MODE** breaks ties by returning the smallest value among those tied for highest frequency, so output is deterministic.

## Numeric vs categorical-meaningful

Numeric-only (12) — applying to a categorical field emits `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`:
`AGG_SUM`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX`, `AGG_RANGE`, `AGG_MEDIAN`, `AGG_PERCENTILE`, `AGG_STDDEV`, `AGG_VARIANCE`, `AGG_SKEWNESS`, `AGG_KURTOSIS`, `AGG_ZSCORE`.

Categorical-safe (4):
`AGG_COUNT`, `AGG_DISTINCT_COUNT`, `AGG_FREQUENCY`, `AGG_MODE`.

## Null handling

| Aggregator | Behavior |
|------------|----------|
| AGG_COUNT | Excludes nulls. |
| AGG_DISTINCT_COUNT | Excludes nulls before deduplication. |
| AGG_FREQUENCY | Skips nulls; no separate null bucket. |
| AGG_MODE | Excludes nulls before frequency counting. |
| AGG_SUM | Skips nulls. |
| AGG_AVERAGE | Excludes from numerator and denominator. |
| AGG_MIN | Ignores nulls. |
| AGG_MAX | Ignores nulls. |
| AGG_RANGE | Excludes nulls. |
| AGG_MEDIAN | Excludes nulls before sorting. |
| AGG_PERCENTILE | Excludes nulls before interpolation. |
| AGG_STDDEV | Excludes nulls. |
| AGG_VARIANCE | Excludes nulls. |
| AGG_SKEWNESS | Excludes nulls. |
| AGG_KURTOSIS | Excludes nulls. |
| AGG_ZSCORE | Skips nulls; returns 0 for empty groups or zero-stddev groups. |

## Filterers (4)

Filterers run before grouping and aggregation. The `types.Filterer` JSON shape is `{"type", "field", "values", "expression"}`; only the keys relevant to each filterer type are used.

| Type | Config | Effect |
|------|--------|--------|
| FILTER_INCLUDE | `field`, `values` (string list) | Keep records whose field value is in `values`. Categorical values resolved through the field's dictionary; nulls are dropped. |
| FILTER_EXCLUDE | `field`, `values` (string list) | Drop records whose field value is in `values`. Nulls pass through. |
| FILTER_RANGE | `field`, `values` (exactly `[min, max]`) | Keep records where `min <= value <= max` (both bounds inclusive). Nulls are dropped. |
| FILTER_EXPRESSION | `expression` (expr-lang string returning bool) | Evaluate `expression` against the record's field map; keep records where it returns `true`. No `field` key. |

```json
[
  {"type": "FILTER_INCLUDE", "field": "grade", "values": ["A", "B"]},
  {"type": "FILTER_EXCLUDE", "field": "status", "values": ["archived"]},
  {"type": "FILTER_RANGE",   "field": "age",    "values": ["18", "65"]},
  {"type": "FILTER_EXPRESSION", "expression": "score > 90 && active == true"}
]
```

## Null strategy and small-group caveats

(see plan 09 — fills this in after statistical-concepts merge)

## See also

- `attribute-composition.md` — per-record attributes (including ATTR_ZSCORE).
- `grouper-design.md` — how groupers partition data before aggregation runs.
