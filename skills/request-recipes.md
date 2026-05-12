---
name: request-recipes
description: Canonical request JSON skeletons keyed by analytical intent
type: reference
applies_to: process, compose, predict
---

# Request Recipes

> For a searchable library of concrete, executable example requests, call the `pulse_examples_search` MCP tool and fetch with `pulse_examples_get`. Recipes in this file are templated patterns for learning the request shape; the embedded library is reference material you can search by tag, category, or operator and run verbatim. The two skills are complementary — keep this one for pattern recognition, hit the library when you need a working starting point.

<skill_overview>
A copy-pasteable catalogue of canonical `Request` JSON skeletons keyed by analytical intent. Pick the recipe that matches the question, fill the named slots, and submit it as the `request` argument to `pulse_predict` (to validate) and then `pulse_process` (to execute). The shapes mirror the `types.Request` wire format.
</skill_overview>

<reference>
## Slot syntax

Every recipe is a JSON template with two kinds of placeholders. Replace each `$...` marker before submitting; the rest of the document is wire-ready.

| Marker | Replace with |
|---|---|
| `$file` | The `.pulse` cohort filename (e.g. `"sales.pulse"`). |
| `$field:numeric` | Any numeric field: `u8`, `u16`, `u32`, `u64`, `f32`, `f64`, `nullable_u4`/`u8`/`u16`, `decimal128`, `nullable_decimal128`. |
| `$field:categorical` | Any categorical field: `categorical_u8`, `categorical_u16`, `categorical_u32`. |
| `$field:date` | A `date` field. |
| `$field:geo` | A `point_f64` or `h3_cell` field. |
| `$field:any` | Any field type. Useful for `AGG_COUNT` / `AGG_DISTINCT_COUNT`. |
| `$param:int`, `$param:float`, `$param:string` | A scalar parameter value. |

When a recipe needs two distinct fields of the same kind, the marker is suffixed: `$field:numeric_X`, `$field:numeric_Y`, `$field:categorical_A`, `$field:categorical_B`. Match the suffix to the comment beside each slot.

Call `pulse_inspect` with `{"path": "$file"}` to list every field name and type in the cohort so you can resolve markers against the actual schema before running the request. After the first inspect in a session, the action tools' JSON Schemas embed enums of the cohort's field names — picking a wrong name becomes an authoring-time error instead of a runtime one.
</reference>

<reference>
## Key ordering

Recipes order their JSON keys to mirror `types.Request`: `cohort`, `filterers`, `aggregations`, `attributes`, `groups`, `outputs`, `windows`, `features`, `sort`, `tests`, `post_tests`. The order is consistent across every recipe so the LLM can scan for the right slot to fill.

Operator `type` is always written explicitly, even where the smart-defaults table in `getting-started.md` would let it be omitted, because the LLM authoring the request and the human reviewing it both benefit from intent that is visible without consulting a separate table.
</reference>

## Recipes

### Group-by aggregation

**When:** you need a value summed (or averaged, min'd, max'd) per category.

```json
{
  "cohort": {"filename": "$file"},
  "aggregations": [
    {"type": "AGG_SUM", "field": "$field:numeric", "label": "total"}
  ],
  "groups": [
    {"type": "GROUP_CATEGORY", "field": "$field:categorical"}
  ]
}
```

**Returns:** one row per distinct category, with the category column and a single aggregate column named `total`.

**Edge cases:** swap `AGG_SUM` for `AGG_AVERAGE`, `AGG_MIN`, or `AGG_MAX` without changing the rest of the recipe. If the categorical has more than ~65k distinct values, switch to `GROUP_QUANTILE` over a derived numeric or pre-filter to a known subset first.

### Top-N by count

**When:** you want the most-frequent values of a categorical field.

```json
{
  "cohort": {"filename": "$file"},
  "aggregations": [
    {"type": "AGG_COUNT", "field": "$field:any", "label": "n"}
  ],
  "groups": [
    {"type": "GROUP_CATEGORY", "field": "$field:categorical"}
  ],
  "sort": [
    {"field": "n", "desc": true}
  ]
}
```

**Returns:** one row per distinct category sorted by row count descending. Truncate downstream (Pulse does not cap row counts; pipe to `head` or use a `FILTER_RANGE` post hoc if needed).

**Edge cases:** `AGG_COUNT` accepts any field type — pick a non-nullable column to avoid undercounting. `AGG_DISTINCT_COUNT` answers a different question (unique values, not rows). For the value itself with no per-row context, `AGG_MODE` is one row, not Top-N.

### Range filter then sum

**When:** restrict to a numeric window before aggregating.

```json
{
  "cohort": {"filename": "$file"},
  "filterers": [
    {"type": "FILTER_RANGE", "field": "$field:numeric_X", "values": ["$param:string_low", "$param:string_high"]}
  ],
  "aggregations": [
    {"type": "AGG_SUM", "field": "$field:numeric_Y", "label": "total"}
  ]
}
```

**Returns:** one scalar row with the summed value over the filtered subset.

**Edge cases:** `FILTER_RANGE.values` is a two-element string array `[low, high]` (Pulse parses each entry as a number; quote them in JSON). The interval is inclusive on both ends. Stack additional filterers in the same array — they AND together. To filter on a string set instead, swap in `FILTER_INCLUDE` with `"values": ["A", "B", "C"]`.

### Moving average over date

**When:** smooth a numeric series across a date axis.

```json
{
  "cohort": {"filename": "$file"},
  "aggregations": [
    {"type": "AGG_SUM", "field": "$field:numeric", "label": "daily_total"}
  ],
  "groups": [
    {"type": "GROUP_DATE", "field": "$field:date", "params": {"component": "day"}}
  ],
  "windows": [
    {
      "type": "WIN_MOVING_AVG",
      "field": "daily_total",
      "label": "ma_7",
      "order_by": [{"field": "$field:date"}],
      "frame": {"mode": "rows", "preceding": 6, "following": 0}
    }
  ],
  "sort": [{"field": "$field:date"}]
}
```

**Returns:** one row per day with `daily_total` (the day's sum) and `ma_7` (its 7-day trailing average).

**Edge cases:** the window's `field` is the *aggregated* column label (`daily_total`), not the raw column — windows run after aggregation. `frame.following: 0` keeps the average causal (no peek into the future). For weekly or monthly grain, switch `params.component` to `"week"`, `"month"`, `"quarter"`, or `"year"`. Any request with a `windows` array is buffered, not streamed.

### Lag-1 percent change

**When:** compute period-over-period change.

```json
{
  "cohort": {"filename": "$file"},
  "aggregations": [
    {"type": "AGG_SUM", "field": "$field:numeric", "label": "total"}
  ],
  "groups": [
    {"type": "GROUP_DATE", "field": "$field:date", "params": {"component": "month"}}
  ],
  "windows": [
    {
      "type": "WIN_PCT_CHANGE",
      "field": "total",
      "label": "pct_change_mom",
      "order_by": [{"field": "$field:date"}],
      "params": {"periods": 1}
    }
  ],
  "sort": [{"field": "$field:date"}]
}
```

**Returns:** one row per month with `total` and `pct_change_mom` (NaN on the first row; no prior period exists).

**Edge cases:** to compare per-region series independently, add `"partition_by": ["$field:categorical"]` to the window — each partition resets the lag. `WIN_PCT_CHANGE` returns NaN when the prior value is zero; if that's a problem, substitute `WIN_LAG` and compute the ratio with `ATTR_FORMULA`.

### Two-sample t-test

**When:** test whether the mean of a numeric outcome differs between two groups.

```json
{
  "cohort": {"filename": "$file"},
  "tests": [
    {
      "type": "TEST_T",
      "field": "$field:numeric",
      "split_by": "$field:categorical",
      "alpha": 0.05
    }
  ]
}
```

**Returns:** an empty `data` array plus one entry in `tests` carrying `statistic`, `df`, `p_value`, `alpha`, `reject_null`, and `details` (per-group `n`, `mean`, `variance`, plus `effect_size.cohens_d`).

**Edge cases:** `split_by` must produce exactly two distinct values — Pulse rejects k≠2 with `PULSE_TEST_SPLIT_GROUPS_LT_2`. For one-sample (test the field's mean against a hypothesized value), drop `split_by` and add `"params": {"mu": $param:float}`. Use `TEST_WELCH` if you want to document the Welch variant explicitly; behaviour is identical. For nonparametric, swap in `TEST_MANN_WHITNEY_U` (same wire shape).

### Correlation (Pearson or Spearman)

**When:** measure association between two numeric fields.

```json
{
  "cohort": {"filename": "$file"},
  "tests": [
    {
      "type": "TEST_PEARSON_R",
      "field": "$field:numeric_X",
      "field2": "$field:numeric_Y",
      "alpha": 0.05
    }
  ]
}
```

**Returns:** an empty `data` array plus one entry in `tests` with `statistic` (the correlation coefficient r), `df` = n − 2, `p_value`, and `details` carrying `n`, the sample means, and the sample covariance.

**Edge cases:** swap `TEST_PEARSON_R` for `TEST_SPEARMAN_R` to measure monotonic (rank-based) association instead of linear — the request shape is identical, but Spearman is buffered (ranks the full value set). Use `TEST_KENDALL_TAU` for tie-robust concordance correlation on small-to-medium n (O(n²)). Pearson streams; Spearman and Kendall force the buffered path.

### Chi-square contingency

**When:** test independence of two categorical variables.

```json
{
  "cohort": {"filename": "$file"},
  "tests": [
    {
      "type": "TEST_CHISQ",
      "rows": "$field:categorical_A",
      "cols": "$field:categorical_B",
      "alpha": 0.05
    }
  ]
}
```

**Returns:** an empty `data` array plus one entry in `tests` with `statistic` (χ²), `df` = (r−1)(c−1), `p_value`, and `details` carrying the observed contingency table, row/col labels, and `expected_min` (smallest expected cell count).

**Edge cases:** when any expected cell count drops below 5 Pulse emits `PULSE_TEST_EXPECTED_COUNT_TOO_LOW`. On a 2×2 table with small expected counts, switch to `TEST_FISHER_EXACT` (same `rows`/`cols` shape). Unlike value-bearing tests, `TEST_CHISQ` reads no `field` or `split_by`.

### Quantile grouping for cohort analysis

**When:** segment records into equal-population buckets (quartiles, deciles, percentiles) over a numeric field.

```json
{
  "cohort": {"filename": "$file"},
  "aggregations": [
    {"type": "AGG_COUNT", "field": "$field:any", "label": "n"},
    {"type": "AGG_AVERAGE", "field": "$field:numeric_Y", "label": "avg_y"}
  ],
  "groups": [
    {"type": "GROUP_QUANTILE", "field": "$field:numeric_X", "interval": 4}
  ]
}
```

**Returns:** one row per quantile bucket (`Q1`..`Q4` for interval 4, `D1`..`D10` for 10, `P1`..`P100` for 100) with `n` and `avg_y`.

**Edge cases:** `interval` lives on the top-level `Group.interval` field (a float), not in `params`. Common values: 4 (quartiles), 10 (deciles), 100 (percentiles). `GROUP_QUANTILE` is buffered — cutpoints require the sorted value set. For a streaming-friendly substitute when the value range is known, use `GROUP_RANGE` with an explicit `interval` width.

### Feature engineering pipeline (one-hot + bucketize + sum)

**When:** materialize ML-ready derived columns before filtering and aggregating in a single Process request.

```json
{
  "cohort": {"filename": "$file"},
  "features": [
    {
      "type": "FEAT_BUCKETIZE",
      "field": "$field:numeric_X",
      "label": "x_bucket",
      "params": {"quantiles": 4}
    },
    {
      "type": "FEAT_ONE_HOT",
      "field": "$field:categorical",
      "label": "cat"
    }
  ],
  "aggregations": [
    {"type": "AGG_SUM", "field": "$field:numeric_Y", "label": "y_sum"}
  ],
  "groups": [
    {"type": "GROUP_CATEGORY", "field": "x_bucket"}
  ]
}
```

**Returns:** one row per quartile bucket of `$field:numeric_X`, plus one-hot columns named `cat_<value>` for every dictionary entry of `$field:categorical`, with `y_sum` per bucket.

**Edge cases:** features run **before** filterers, so the derived columns (`x_bucket`, `cat_*`) are addressable as group keys, filter targets, and window inputs in the same request. Order in the `features` array matters — operators downstream in the list can reference labels produced upstream. If a `FEAT_TARGET_ENCODE` appears anywhere in the pipeline it MUST be preceded by a `FEAT_TRAIN_TEST_SPLIT` in the same list, or `PULSE_FEAT_TARGET_LEAKAGE_RISK` fires (warning by default, error under `--strict`).

<see_also>
- getting-started — vocabulary, request shape, and the pipeline order all recipes assume
- aggregation-guide — full `AGG_*` and `FILTER_*` catalog with numeric-vs-categorical compatibility
- grouper-design — `GROUP_*` semantics including quantile labelling and date components
- window-operations — `WIN_*` operators, partitioning, and frame rules used in the moving-average and percent-change recipes
- feature-engineering — `FEAT_*` operator catalog and the target-leakage trap referenced by the pipeline recipe
- statistical-testing — tier-1 vs tier-2 testing and per-operator output schemas
- debugging-with-predict — validate a filled-in recipe with `pulse_predict` (or `pulse_ask` with `predict=true`) before running it
</see_also>
