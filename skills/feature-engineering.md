---
name: feature-engineering
description: Apply pre-filter FEAT_* operators — FEAT_LOG, FEAT_SQRT, FEAT_BUCKETIZE, FEAT_ONE_HOT, FEAT_FREQUENCY_ENCODE, FEAT_TARGET_ENCODE, FEAT_DATE_FEATURES, FEAT_TRAIN_TEST_SPLIT, FEAT_POLY — for ML pipelines. Use when preparing training data; covers the target-leakage trap and PULSE_FEAT_TARGET_LEAKAGE_RISK.
type: guide
applies_to: process, compose, predict
---

# Feature Engineering

<skill_overview>
The `FEAT_*` operators are pre-filter feature engineers: each one runs over the unfiltered record set and adds one or more derived columns that downstream filters, attributes, groupers, aggregators, and windows can reference by label. Use them as the last mile of an ML pipeline (raw → cleaned → featurized → train/test) without leaving the engine. The most important rule in this skill is the leakage trap on `FEAT_TARGET_ENCODE` — read that section before reaching for it.
</skill_overview>

<reference>
## Pipeline position

Features run **before** filters. The order is:

```
Load -> Features -> Filter -> Attributes -> Group -> Aggregate -> Windows -> Sort -> Output
```

A feature's output column is addressable by every stage that follows. This is why bucketize → filter on the bucket column works, and why train/test/split tags are usable as filter values.
</reference>

<reference>
## Streaming eligibility

Feature requests run on the streaming path when every requested operator implements `feature.StreamingComputer` and the rest of the request is stream-eligible (online-capable aggregators, no groups, no attributes, no windows). Per-row operators (`FEAT_LOG`, `FEAT_SQRT`, `FEAT_BUCKETIZE` with explicit boundaries, `FEAT_ONE_HOT`, `FEAT_DATE_FEATURES`, `FEAT_POLY`) emit derived columns one record at a time. Global-pass operators (`FEAT_FREQUENCY_ENCODE`, `FEAT_TARGET_ENCODE`, `FEAT_BUCKETIZE` with quantiles, `FEAT_TRAIN_TEST_SPLIT`) run a precompute sweep, then the iterator is rewound and per-row emit drives filters and online aggregators.

The streaming path requires the iterator to support `Reset()`. The slice iterator resets in O(1); the file-backed streaming iterator re-reads the file from disk, which doubles I/O cost for global-pass operators. The buffered path remains the fallback whenever streaming is unsafe — and is selected automatically.

`FEAT_TRAIN_TEST_SPLIT` materializes its assignment table during the precompute sweep, so streaming mode pays the same `O(rows)` memory as buffered for the split column. Per-row operators add no extra allocations beyond the derived column.
</reference>

<reference>
## Operator catalog

### FEAT_LOG

`log(x + 1)` (log1p) on a numeric field. Null-safe. Inputs `<= -1` produce null.

| Param | Type | Required | Notes |
|---|---|---|---|
| `field` | string | yes | Source numeric field |
| `label` | string | no | Output column name (default `LOG_<field>`) |

### FEAT_SQRT

`sqrt(x)` on a numeric field. Null-safe. Negative inputs produce null.

| Param | Type | Required | Notes |
|---|---|---|---|
| `field` | string | yes | Source numeric field |
| `label` | string | no | Output column name (default `SQRT_<field>`) |

### FEAT_BUCKETIZE

Discretize a numeric field into bucket indexes (`0..N`). Provide either `boundaries` (explicit edges) or `quantiles` (equal-frequency bin count). The two are mutually exclusive.

| Param | Type | Required | Notes |
|---|---|---|---|
| `field` | string | yes | Source numeric field |
| `label` | string | no | Output column name (default `BUCKET_<field>`) |
| `params.boundaries` | []float | conditional | Sorted edge values |
| `params.quantiles` | int | conditional | Number of equal-frequency bins (>= 2) |

### FEAT_ONE_HOT

Expand a categorical field into N boolean columns sourced from the schema dictionary. Output column set is deterministic regardless of which categories appear at runtime — `predict` materializes the same column set the executor will produce.

| Param | Type | Required | Notes |
|---|---|---|---|
| `field` | string | yes | Source categorical field |
| `label` | string | no | Column prefix (default `<field>`) |

Output columns are `<prefix>_<category>`. Spaces in category strings normalize to underscores.

### FEAT_DATE_FEATURES

Decompose a date-typed field into five derived columns: `year`, `month`, `day`, `dow` (0=Sunday..6=Saturday), `quarter` (1..4). Decoding mirrors `ATTR_DATE_PART`.

| Param | Type | Required | Notes |
|---|---|---|---|
| `field` | string | yes | Source field of type `date` |
| `label` | string | no | Column prefix (default `<field>`) |

Output columns: `<prefix>_year`, `<prefix>_month`, `<prefix>_day`, `<prefix>_dow`, `<prefix>_quarter`.

### FEAT_FREQUENCY_ENCODE

Replace a categorical with the proportion of rows that share its value (count / total non-null). Two-pass.

| Param | Type | Required | Notes |
|---|---|---|---|
| `field` | string | yes | Source categorical field |
| `label` | string | no | Output column name (default `FREQ_<field>`) |

### FEAT_TARGET_ENCODE

Replace a categorical with the mean of a numeric target field over rows sharing that category. Optional smoothing pulls rare categories toward the global mean.

| Param | Type | Required | Notes |
|---|---|---|---|
| `field` | string | yes | Source categorical field |
| `label` | string | no | Output column name (default `TARGET_<field>`) |
| `params.target` | string | yes | Numeric target field |
| `params.smoothing` | float | no | Additive prior toward global mean (default 0) |

Smoothing formula: `(n * mean_cat + s * mean_global) / (n + s)`. Larger `s` → more shrinkage.

**LEAKAGE TRAP (read this):** target encoding mixes target signal from validation/test rows into the training feature unless you split first. The canonical fix is to place a `FEAT_TRAIN_TEST_SPLIT` operator BEFORE every `FEAT_TARGET_ENCODE` in the same `features` list. Predict emits `PULSE_FEAT_TARGET_LEAKAGE_RISK` (warning by default; error in `--strict`) when a TARGET_ENCODE has no preceding TRAIN_TEST_SPLIT.

### FEAT_POLY

Per-row polynomial expansion of a numeric field. Emits `Degree - 1` derived columns named `<prefix>_2`, `<prefix>_3`, ..., `<prefix>_<Degree>`, one per integer power level `k ∈ [2, Degree]`. The original column (degree 1, the linear term) is left in place — downstream `REG_OLS` can reference both `<field>` and the synthesized higher-order names by including them in `predictors`.

This is the operator backing polynomial regression (Indeed #8): combined with `REG_OLS`, it lifts a univariate predictor into a linear model over the polynomial basis `{x, x², ..., x^d}`.

| Param | Type | Required | Notes |
|---|---|---|---|
| `field` | string | yes | Source numeric field (`u*`, `f32`, `f64`) |
| `label` | string | no | Output column prefix (default `<field>_poly`) |
| `params.degree` | int | yes | Polynomial degree; `2 ≤ Degree ≤ 10` |

**Output column naming.** With `Field: "x"` and `Label` unset, FEAT_POLY emits `x_poly_2`, `x_poly_3`, ..., `x_poly_<Degree>`. Set `Label: "x"` to drop the `_poly` infix and get `x_2`, `x_3`, ..., `x_<Degree>` — convenient when chaining the linear term `x` and the higher-order terms in the same `predictors` list. Null inputs propagate to null on every output column.

**Numerical stability.** Naive `x^d` overflows quickly when `x` is large or untransformed: for `Degree=10` and `|x|=100` the leading term is already `1e20`. Standardize or center predictors before requesting `FEAT_POLY`; Pulse does not synthesize an orthogonal basis (Chebyshev, Legendre) — that would warrant a dedicated operator and is reserved for a later phase. The factory rejects `Degree < 2` (use the original column for the linear term) and `Degree > 10` (cap; standardize first) with `PROCESSING_CONFIG`.

**Worked example — polynomial regression.** A cubic fit `y = β₀ + β₁ x + β₂ x² + β₃ x³ + ε` reachable as `FEAT_POLY` upstream of `REG_OLS`:

```json
{
  "features": [{"type": "FEAT_POLY", "field": "x", "label": "x",
                "params": {"degree": 3}}],
  "regressions": [{
    "type": "REG_OLS",
    "name": "polynomial_fit",
    "target": "y",
    "predictors": ["x", "x_2", "x_3"]
  }]
}
```

Setting `Label: "x"` makes the synthesized names `x_2` and `x_3`; if you omit `Label`, the predictors list becomes `["x", "x_poly_2", "x_poly_3"]` instead. Either form is supported.

### FEAT_TRAIN_TEST_SPLIT

Tag each row with a partition assignment in a numeric `split` column (`0=train`, `1=val`, `2=test`). Two- or three-element ratio vectors supported. Optional stratification preserves class balance across partitions.

| Param | Type | Required | Notes |
|---|---|---|---|
| `field` | string | no | Unused unless stratifying |
| `label` | string | no | Output column name (default `split`) |
| `params.ratios` | []float | yes | 2 or 3 elements summing to 1.0 |
| `params.seed` | int | no | Deterministic shuffle seed (default 0) |
| `params.stratify` | string | no | Categorical field to stratify by |

The encoded values are the constants `feature.SplitTrain` (0), `feature.SplitVal` (1), `feature.SplitTest` (2). Filter by `split == 0` for training-only operations downstream.
</reference>

<workflow id="A" name="basic-featurize-then-aggregate">
### Featurize then aggregate

```json
{
  "cohort": {"filename": "transactions.pulse"},
  "features": [
    {"type": "FEAT_LOG", "field": "amount", "label": "log_amount"},
    {"type": "FEAT_BUCKETIZE", "field": "log_amount", "label": "amount_bucket",
     "params": {"quantiles": 10}}
  ],
  "groups": [{"type": "GROUP_CATEGORY", "field": "amount_bucket"}],
  "aggregations": [{"type": "AGG_COUNT", "field": "id", "label": "n"}]
}
```

Note `FEAT_BUCKETIZE` references `log_amount` — the output of the previous feature. Features compose in order.
</workflow>

<workflow id="B" name="safe-target-encoding">
### Target encoding without leakage

Order matters:

```json
{
  "features": [
    {"type": "FEAT_TRAIN_TEST_SPLIT",
     "params": {"ratios": [0.7, 0.15, 0.15], "seed": 42, "stratify": "label"}},
    {"type": "FEAT_TARGET_ENCODE", "field": "region",
     "params": {"target": "price", "smoothing": 5.0}}
  ]
}
```

`FEAT_TRAIN_TEST_SPLIT` first, then `FEAT_TARGET_ENCODE`. Reverse the order and predict surfaces `PULSE_FEAT_TARGET_LEAKAGE_RISK`.
</workflow>

<workflow id="C" name="one-hot-then-filter">
### One-hot then filter on a category

```json
{
  "features": [{"type": "FEAT_ONE_HOT", "field": "region"}],
  "filterers": [
    {"type": "FILTER_INCLUDE", "field": "region_north", "values": ["1"]}
  ]
}
```

`region_north` is reachable to the filter because features run before filters in the pipeline.
</workflow>

<reference>
## Predict surface

Call `pulse_predict` against any feature-using request. Predict will:
- Reject unknown `FEAT_*` types (`SERVICE_VALIDATION`).
- Verify required input fields exist in the cohort schema.
- Verify type compatibility (categorical for ONE_HOT/FREQUENCY/TARGET, date for DATE_FEATURES, numeric target for TARGET_ENCODE).
- Validate parameter shape (boundaries vs quantiles mutual exclusion, ratios summing to 1.0, smoothing >= 0).
- Materialize the post-feature column set so downstream filter/group/attribute/aggregation references are checked against derived columns too.
- Surface `PULSE_FEAT_TARGET_LEAKAGE_RISK` for misordered TARGET_ENCODE.

The post-feature schema is virtual — `pulse inspect` still reports only on-disk fields.
</reference>

<reference>
## Decimal128 inputs

`FEAT_LOG`, `FEAT_SQRT`, and `FEAT_BUCKETIZE` accept `decimal128` source fields (set `Nullable: true` on the field to participate in the per-record bitmap). Pulse's record reader populates the typed mantissa in the wide map and a paired f64 approximation in the values map; feature operators read the f64. Null cells are excluded via the bitmap before the operator runs. The derived output column is f64 — `log` and `sqrt` are inherently irrational, so there is no auditor-grade decimal version.

Categorical-only operators (`FEAT_ONE_HOT`, `FEAT_FREQUENCY_ENCODE`, `FEAT_TARGET_ENCODE`) and `FEAT_DATE_FEATURES` continue to reject decimal128 inputs per their existing type contracts. See `skills/financial-cohorts.md` for the full decimal interaction matrix.
</reference>

<see_also>
- getting-started — pipeline order, vocabulary, command tree
- aggregation-guide — `AGG_*` operations that consume feature outputs
- attribute-composition — `ATTR_*` derivations that compose with features
- error-code-reference — `PULSE_FEAT_TARGET_LEAKAGE_RISK` recovery playbook
- mcp-integration — calling features through the MCP tool surface
</see_also>
