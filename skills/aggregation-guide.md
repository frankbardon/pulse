---
name: aggregation-guide
description: Choose between the 16 AGG_* operators (SUM, AVG, MEDIAN, PERCENTILE, ZSCORE, FREQUENCY, MODE, KURTOSIS, ...) and 5 FILTER_* operators. Use when assembling a Process request, interpreting percentile/frequency output, or picking a row-filter.
type: guide
applies_to: process, compose, predict
---

# Aggregation Guide

<skill_overview>
Pulse exposes 18 aggregators and 6 filterers that run during `process` and `compose`. Invoke this skill when choosing aggregators for a request, validating numeric-vs-categorical compatibility, or shaping filterers before grouping.
</skill_overview>

<reference>
## Aggregators (19)

| Type | Meaning | Input |
|------|---------|-------|
| AGG_COUNT | Number of non-null values. | any |
| AGG_NULL_COUNT | Number of null values (inverse of AGG_COUNT). | any |
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
</reference>

<rule severity="caveat" topic="decimal-aggregators">
## Decimal aggregators

Aggregations on `decimal128` / `nullable_decimal128` fields are dispatched to a decimal-aware path that preserves precision:

- **AGG_SUM** errors with `PULSE_DECIMAL_OVERFLOW` on accumulator overflow.
- **AGG_AVERAGE** preserves precision; falls back to f64 with `PULSE_DECIMAL_PRECISION_LOSS` warning if the sum would overflow `decimal128(38)`.
- **AGG_MIN / AGG_MAX** return `decimal128`.
- **AGG_VARIANCE** computes a two-pass population variance entirely in decimal at `2 * mean_scale`. Falls back to f64 with `PULSE_DECIMAL_PRECISION_LOSS` only when intermediate state would overflow `decimal128(38)`.
- **AGG_STDDEV** applies a banker-rounded decimal `sqrt` to the variance and returns `decimal128` at `mean_scale`. Falls back to f64 on the same overflow path as variance.
- **AGG_COUNT / AGG_DISTINCT_COUNT** return integers.
- Other aggregations on decimal fields surface `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.

See `skills/financial-cohorts.md` for the SQL:2016 precision propagation rules and banker's rounding policy.
</rule>

<rule severity="caveat" topic="aggregator-quirks">
## Notes on non-obvious aggregators

- **AGG_PERCENTILE** takes a `params` object with key `percentile` (float, 0-100). Defaults to 50 (median) if `params` is omitted; out-of-range values produce `PROCESSING_CONFIG`.
- **AGG_ZSCORE** computes `(x - mean) / stddev` for each value, then returns the mean of those z-scores. By definition this is ~0 for any non-degenerate group; use ATTR_ZSCORE for per-record scores instead.
- **AGG_FREQUENCY** returns the count of the most-frequent value (not a histogram). Nulls are skipped before counting; they do not get their own bucket. Use AGG_MODE if you want the value itself.
- **AGG_MODE** breaks ties by returning the smallest value among those tied for highest frequency, so output is deterministic.
</rule>

<rule severity="must" topic="numeric-on-categorical">
## Numeric vs categorical-meaningful

Numeric-only (12) — applying to a categorical field emits `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`:
`AGG_SUM`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX`, `AGG_RANGE`, `AGG_MEDIAN`, `AGG_PERCENTILE`, `AGG_STDDEV`, `AGG_VARIANCE`, `AGG_SKEWNESS`, `AGG_KURTOSIS`, `AGG_ZSCORE`.

The numeric set is the broader analytics-numeric family (`encoding.FieldType.IsNumericForAnalytics`): the integer / float / decimal types plus the bit-packed integer encodings `nullable_u4`, `nullable_bool`, `packed_bool`, and `date`. `AGG_AVERAGE` on `packed_bool` returns the proportion of `true`; `AGG_AVERAGE` on `nullable_u4` returns the mean of the stored ordinals with the `0x0F` null sentinel excluded from both numerator and denominator. No `ATTR_FORMULA float(field)` cast is needed — and skipping the cast keeps the request on the streaming path that the formula would have forced into the buffered orchestrator.

Categorical-safe (5):
`AGG_COUNT`, `AGG_NULL_COUNT`, `AGG_DISTINCT_COUNT`, `AGG_FREQUENCY`, `AGG_MODE`.
</rule>

<reference>
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

### When to prefer FacetSchema over GROUP_CATEGORY + AGG_COUNT

For "give me the per-value count of one or more fields" without aggregating across groups, `pulse.FacetSchema` (`pulse_facet_schema` MCP tool) is cheaper than wiring `groups: [{type: GROUP_CATEGORY, field: F}], aggregations: [{type: AGG_COUNT, field: F}]`. FacetSchema also covers null tallies, top-K truncation with deterministic descending-by-count sort, and additive contribution counts. See the `facet-design` skill for the full surface.
</reference>

<reference>
## Filterers (6)

Filterers run before grouping and aggregation. The `types.Filterer` JSON shape is `{"type", "field", "values", "expression"}`; only the keys relevant to each filterer type are used.

| Type | Config | Effect |
|------|--------|--------|
| FILTER_INCLUDE | `field`, `values` (string list) | Keep records whose field value is in `values`. Categorical values resolved through the field's dictionary; nulls are dropped. |
| FILTER_EXCLUDE | `field`, `values` (string list) | Drop records whose field value is in `values`. Nulls pass through. |
| FILTER_RANGE | `field`, `values` (exactly `[min, max]`) | Keep records where `min <= value <= max` (both bounds inclusive). Nulls are dropped. |
| FILTER_NULL | `field`, `values` (exactly `["is_null"]` or `["is_not_null"]`) | Keep records based on null state of the field. Use `is_null` to keep records where the field is null; `is_not_null` to drop them. |
| FILTER_EXPRESSION | `expression` (expr-lang string returning bool) | Evaluate `expression` against the record's field map; keep records where it returns `true`. No `field` key. |
</reference>

<example name="filter-include">
Keep records whose `grade` is `A` or `B`.

```json
{"type": "FILTER_INCLUDE", "field": "grade", "values": ["A", "B"]}
```
</example>

<example name="filter-exclude">
Drop records whose `status` is `archived`.

```json
{"type": "FILTER_EXCLUDE", "field": "status", "values": ["archived"]}
```
</example>

<example name="filter-range">
Keep records where `age` is between 18 and 65 inclusive.

```json
{"type": "FILTER_RANGE", "field": "age", "values": ["18", "65"]}
```
</example>

<example name="filter-null">
Keep only records where `email` is null.

```json
{"type": "FILTER_NULL", "field": "email", "values": ["is_null"]}
```

Drop records where `email` is null (keep populated emails only).

```json
{"type": "FILTER_NULL", "field": "email", "values": ["is_not_null"]}
```

To count nulls and non-nulls in a single request, combine `AGG_COUNT` (non-null) with `AGG_NULL_COUNT` (null) on the same field.

```json
{"aggregations": [
  {"type": "AGG_COUNT",      "field": "email", "label": "n_present"},
  {"type": "AGG_NULL_COUNT", "field": "email", "label": "n_missing"}
]}
```
</example>

<example name="filter-expression">
Keep records that match an expr-lang predicate.

```json
{"type": "FILTER_EXPRESSION", "expression": "score > 90 && active == true"}
```
</example>

<rule severity="caveat" topic="sharded-buffered-memory">
## Shard archives and forced-buffered ops

Forced-buffered operations on a shard archive materialize across the **union** of shards, not per-shard. This is mathematically required for global percentile semantics (median-of-medians is not the median). Buffered ops in this set: `AGG_MEDIAN`, `AGG_PERCENTILE`, `AGG_ZSCORE`, all window operators (`WIN_*`), decimal paths, `ATTR_PERCENTILE`, `GROUP_QUANTILE`, `GROUP_DATE`, tier-1 tests combined with groupers/features/two-pass attrs, and every tier-2 post test.

Memory cost scales with shard count. A 13-week quarterly archive costs roughly 13× the single-shard buffer for these ops. Pick shard granularity with this multiplier in mind. The single-file path is unaffected. See `cohort-schema-design` (Sharded cohorts) for the archive layout and the full streamability gate list.
</rule>

<rule severity="should" topic="null-strategy">
## Null strategy and small-group caveats

Aggregations skip nulls (see the table above); the choice is what to do about records that became null upstream.

- **Filter when null means "out of scope."** Use `FILTER_INCLUDE` on the field, or `FILTER_EXPRESSION` checking non-null, to drop those records before aggregation.
- **Re-import with imputed values when null is a known stand-in.** Pulse does not impute at process-time; substitute the value (mean, median, sentinel) in the source data and re-run import.
- **Default: filter, and document the choice in the request comment** so downstream consumers see which records were excluded.

Tiny groups produce unstable summary stats. Pair non-trivial aggregations with `AGG_COUNT` (alias `n`) so the consumer can flag thin slices and downweight or hide them.

Higher moments (`AGG_STDDEV`, `AGG_VARIANCE`, `AGG_SKEWNESS`, `AGG_KURTOSIS`) require n ≥ 2 non-null values; below that, they return 0 rather than erroring (skewness/kurtosis also return 0 when stddev is 0).
</rule>

<see_also>
- attribute-composition — per-record attributes (including ATTR_ZSCORE).
- grouper-design — how groupers partition data before aggregation runs.
</see_also>
