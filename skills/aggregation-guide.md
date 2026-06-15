---
name: aggregation-guide
description: Choose between the 16 AGG_* operators (SUM, AVG, MEDIAN, PERCENTILE, ZSCORE, FREQUENCY, MODE, KURTOSIS, ...) and 7 FILTER_* operators. Use when assembling a Process request, interpreting percentile/frequency output, or picking a row-filter.
type: guide
applies_to: process, compose, predict
---

# Aggregation Guide

<skill_overview>
Pulse exposes 22 scalar aggregators and 7 filterers that run during `process` and `compose` (plus the six set-typed aggregators documented in their own section below). Invoke this skill when choosing aggregators for a request, validating numeric-vs-categorical compatibility, or shaping filterers before grouping.
</skill_overview>

<reference>
## Aggregators (22)

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
| AGG_WEIGHTED_MEAN | `sum(field * weight) / sum(weight)` — streaming Chan-Welford with per-row weight column. | numeric (Params.`weight_field` required) |
| AGG_RATIO | `sum(numerator_field) / sum(denominator_field)`. Aggregation `Field` is ignored. | numeric (Params.`numerator_field` + Params.`denominator_field`) |
| AGG_CI_LOWER | Lower bound of the mean's confidence interval, normal method (Welford + inverse-normal quantile). | numeric (Params.`confidence`, Params.`method`) |
| AGG_CI_UPPER | Upper bound of the mean's confidence interval. Mirrors AGG_CI_LOWER. | numeric (Params.`confidence`, Params.`method`) |
| AGG_WELFORD | Streaming Welford-Pébaÿ triple `(mean, sample_variance, n)` emitted via `RichAggregator`. Scalar fallback is the running mean. | numeric scalar (`u8`/`u16`/`u32`/`u64`/`f32`/`f64`) |
</reference>

<rule severity="must" topic="components-contract">
## Components contract — universal floor + per-operator keys

Every aggregation result that lands in `Response.Components.Aggregations` carries a **universal floor** of two integer keys regardless of operator:

- `n` — number of records aggregated (non-null inputs).
- `n_null` — number of null inputs encountered.

The floor is emitted unconditionally by the orchestrator's universal-floor pass — embedders can rely on it being present even when an aggregator declines to add any operator-specific keys.

Operator-specific keys ride in the `Operator map[string]any` field of `AggregationComponents`. The authoritative key list for each `AGG_*` is the operator's `ComponentSchema` declared in `descriptor/capabilities_aggregators.go`, and the same schema is mirrored in the manifest at `components_schemas.aggregators[<op>].keys` for self-describing client lookups.

### `MetaAggregator` sibling interface

`MetaAggregator` joins `OnlineAggregator`, `MergeableAggregator`, and `RichAggregator` as a new sibling on the `Aggregator` interface:

```go
type MetaAggregator interface {
    Components() (map[string]any, error)
}
```

- Called **once** after the aggregator's `Aggregate` / `Finalize` cycle completes; never mid-stream.
- Returning `(nil, nil)` signals "no operator-specific keys; the universal floor still applies."
- Returned keys MUST match the operator's declared `ComponentSchema` — mismatches fire `PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH` at extension probe time.
- Aggregators with no extra keys (`AGG_COUNT`, `AGG_NULL_COUNT`) may omit the implementation — the floor pass populates `n` / `n_null` either way.

### `ComponentsMergeability` axis

Every aggregator declares one of three merge classifications on its `ComponentSchema`:

- **`Mergeable`** — components fold across chunks via the same `MergeOnline` path as the scalar value. Streaming chunks carry `ComponentsDelta` and consumers reconcile lossless. Welford-family aggregators (`AGG_VARIANCE`, `AGG_STDDEV`, `AGG_SKEWNESS`, `AGG_KURTOSIS`, `AGG_WELFORD`, `AGG_ZSCORE`), sums / counts / extrema (`AGG_SUM`, `AGG_AVERAGE`, `AGG_COUNT`, `AGG_NULL_COUNT`, `AGG_MIN`, `AGG_MAX`, `AGG_RANGE`), CI bounds (`AGG_CI_LOWER`, `AGG_CI_UPPER`), weighted / ratio (`AGG_WEIGHTED_MEAN`, `AGG_RATIO`), and every set aggregator that does NOT carry a per-label map all sit here.
- **`Partial`** — map / set merges that work but cost allocation. The orchestrator may stage merge at terminal flush. `AGG_FREQUENCY`, `AGG_MODE`, `AGG_DISTINCT_COUNT`, and `AGG_SET_FREQUENCY` ride this path.
- **`None`** — non-mergeable; components emitted only on terminal buffered flush. Streaming chunks omit them. Predict declares the slot as buffered-components-only. `AGG_MEDIAN` and `AGG_PERCENTILE` are the only entries — both need sorted full input.

### Streaming-consumer hint

`pulse.Predict(req)` returns one `PredictResult.Aggregations[i]` per request aggregation. The `BufferedComponents` flag tells streaming consumers whether per-chunk `ComponentsDelta` will appear (`false`) or whether components will arrive only at terminal flush (`true`). The flag is `true` exactly when the operator's `ComponentsMergeability` is `None`.

### Go access pattern

```go
resp, err := pulse.Process(ctx, req)
if err != nil { return err }
for _, agg := range resp.Components.Aggregations {
    fmt.Printf("%s: n=%d, n_null=%d, op=%v\n", agg.Label, agg.N, agg.NNull, agg.Operator)
}
```

The cross-cutting `Response.Components` shape — including the `Groupers`, `Filterers`, `Crosstab`, and `Run` siblings — is documented in the [response components contract](response-components.md).
</rule>

<reference>
## Per-operator components — canonical key list

Every row matches the `ComponentSchema` declared in `descriptor/capabilities_aggregators.go`. Universal floor (`n`, `n_null`) is implicit and not repeated.

| Operator | Mergeability | Operator-specific keys |
|---|---|---|
| `AGG_COUNT` | `mergeable` | (floor only) |
| `AGG_NULL_COUNT` | `mergeable` | (floor only — `n_null` is the value) |
| `AGG_SUM` | `mergeable` | `sum` |
| `AGG_AVERAGE` | `mergeable` | `sum` |
| `AGG_MIN` | `mergeable` | `min` |
| `AGG_MAX` | `mergeable` | `max` |
| `AGG_RANGE` | `mergeable` | `min`, `max` |
| `AGG_STDDEV` | `mergeable` | `mean`, `m2`, `variance`, `stddev` |
| `AGG_VARIANCE` | `mergeable` | `mean`, `m2`, `variance` |
| `AGG_SKEWNESS` | `mergeable` | `mean`, `m2`, `m3`, `skewness` |
| `AGG_KURTOSIS` | `mergeable` | `mean`, `m2`, `m3`, `m4`, `kurtosis` |
| `AGG_ZSCORE` | `mergeable` | `pop_mean`, `pop_stddev`, `target_value`, `zscore` |
| `AGG_WELFORD` | `mergeable` | `mean`, `m2`, `variance`, `stddev` |
| `AGG_CI_LOWER` | `mergeable` | `mean`, `stderr`, `alpha`, `t_critical`, `lower` |
| `AGG_CI_UPPER` | `mergeable` | `mean`, `stderr`, `alpha`, `t_critical`, `upper` |
| `AGG_WEIGHTED_MEAN` | `mergeable` | `sum_weighted`, `sum_weights`, `weighted_mean` |
| `AGG_RATIO` | `mergeable` | `numerator`, `denominator`, `ratio` |
| `AGG_FREQUENCY` | `partial` | `distinct_count`, `mode_value`, `mode_count` |
| `AGG_MODE` | `partial` | `value`, `count`, `distinct_count`, `tie_count` |
| `AGG_DISTINCT_COUNT` | `partial` | `cardinality` |
| `AGG_MEDIAN` | `none` (buffered only) | `position_low`, `position_high`, `median` |
| `AGG_PERCENTILE` | `none` (buffered only) | `p`, `position`, `lower`, `upper`, `method`, `value` |
| `AGG_SET_UNION` | `mergeable` | `mask_union`, `popcount`, `labels` |
| `AGG_SET_INTERSECTION` | `mergeable` | `mask_intersection`, `popcount`, `labels` |
| `AGG_SET_FREQUENCY` | `partial` | `total_label_observations`, `distinct_labels`, `per_label_count` |
| `AGG_SET_CARDINALITY_SUM` | `mergeable` | `sum_cardinality` |
| `AGG_SET_CARDINALITY_AVG` | `mergeable` | `sum_cardinality`, `avg_cardinality` |
| `AGG_SET_DISTINCT_VALUES` | `mergeable` | `mask_union`, `popcount`, `labels` |

Per-operator detail blocks (one line per row in the table above):

- **`AGG_COUNT`** — **Components** (`mergeable`): `n`, `n_null`. Floor only; the count itself IS `n`.
- **`AGG_NULL_COUNT`** — **Components** (`mergeable`): `n`, `n_null`. Floor only; the null count itself IS `n_null`.
- **`AGG_SUM`** — **Components** (`mergeable`): `n`, `n_null`, `sum`. Running sum across non-null inputs.
- **`AGG_AVERAGE`** — **Components** (`mergeable`): `n`, `n_null`, `sum`. Combine `sum` with `n` to recover the mean.
- **`AGG_MIN`** — **Components** (`mergeable`): `n`, `n_null`, `min`.
- **`AGG_MAX`** — **Components** (`mergeable`): `n`, `n_null`, `max`.
- **`AGG_RANGE`** — **Components** (`mergeable`): `n`, `n_null`, `min`, `max`. Range itself = `max - min`.
- **`AGG_STDDEV`** — **Components** (`mergeable`): `n`, `n_null`, `mean`, `m2`, `variance`, `stddev`. Population denominator (`m2 / n`).
- **`AGG_VARIANCE`** — **Components** (`mergeable`): `n`, `n_null`, `mean`, `m2`, `variance`. Population denominator (`m2 / n`).
- **`AGG_SKEWNESS`** — **Components** (`mergeable`): `n`, `n_null`, `mean`, `m2`, `m3`, `skewness`. Extended Welford recurrence.
- **`AGG_KURTOSIS`** — **Components** (`mergeable`): `n`, `n_null`, `mean`, `m2`, `m3`, `m4`, `kurtosis`. Extended Welford recurrence.
- **`AGG_ZSCORE`** — **Components** (`mergeable`): `n`, `n_null`, `pop_mean`, `pop_stddev`, `target_value`, `zscore`. Group-level standardization summary.
- **`AGG_WELFORD`** — **Components** (`mergeable`): `n`, `n_null`, `mean`, `m2`, `variance`, `stddev`. Sample denominator (`m2 / (n-1)`) — distinct from `AGG_VARIANCE` / `AGG_STDDEV` which use the population denominator.
- **`AGG_CI_LOWER`** — **Components** (`mergeable`): `n`, `n_null`, `mean`, `stderr`, `alpha`, `t_critical`, `lower`.
- **`AGG_CI_UPPER`** — **Components** (`mergeable`): `n`, `n_null`, `mean`, `stderr`, `alpha`, `t_critical`, `upper`.
- **`AGG_WEIGHTED_MEAN`** — **Components** (`mergeable`): `n`, `n_null`, `sum_weighted`, `sum_weights`, `weighted_mean`. Rows with null weight or `weight == 0` are skipped (do not advance `n`).
- **`AGG_RATIO`** — **Components** (`mergeable`): `n`, `n_null`, `numerator`, `denominator`, `ratio`. `ratio == NaN` when `denominator == 0`.
- **`AGG_FREQUENCY`** — **Components** (`partial`): `n`, `n_null`, `distinct_count`, `mode_value`, `mode_count`. Map merge at terminal flush.
- **`AGG_MODE`** — **Components** (`partial`): `n`, `n_null`, `value`, `count`, `distinct_count`, `tie_count`. First-seen tie-break for `value`.
- **`AGG_DISTINCT_COUNT`** — **Components** (`partial`): `n`, `n_null`, `cardinality`. Set merge at terminal flush.
- **`AGG_MEDIAN`** — **Components** (`none` — buffered only): `n`, `n_null`, `position_low`, `position_high`, `median`. Streaming chunks emit no components; `BufferedComponents=true` in Predict.
- **`AGG_PERCENTILE`** — **Components** (`none` — buffered only): `n`, `n_null`, `p`, `position`, `lower`, `upper`, `method`, `value`. Streaming chunks emit no components; `BufferedComponents=true` in Predict.
- **`AGG_SET_UNION`** — **Components** (`mergeable`): `n`, `n_null`, `mask_union`, `popcount`, `labels`. Bitwise-OR fold.
- **`AGG_SET_INTERSECTION`** — **Components** (`mergeable`): `n`, `n_null`, `mask_intersection`, `popcount`, `labels`. Bitwise-AND fold; margin recompute at the crosstab layer.
- **`AGG_SET_FREQUENCY`** — **Components** (`partial`): `n`, `n_null`, `total_label_observations`, `distinct_labels`, `per_label_count`. Per-label map merges at terminal flush.
- **`AGG_SET_CARDINALITY_SUM`** — **Components** (`mergeable`): `n`, `n_null`, `sum_cardinality`. Sum of popcounts.
- **`AGG_SET_CARDINALITY_AVG`** — **Components** (`mergeable`): `n`, `n_null`, `sum_cardinality`, `avg_cardinality`.
- **`AGG_SET_DISTINCT_VALUES`** — **Components** (`mergeable`): `n`, `n_null`, `mask_union`, `popcount`, `labels`.
</reference>

<rule severity="caveat" topic="cohort-aggregators">
## Cohort-analytics aggregators

Four mergeable + streamable aggregators land first-class server-side computation for stats that were previously client-side folds:

- **`AGG_WEIGHTED_MEAN`** — `Params.weight_field` (required). Skips rows whose target field OR weight field is null and rows whose weight is exactly zero. Mergeable via the parallel weighted Chan-Welford reduction.
- **`AGG_RATIO`** — `Params.numerator_field` + `Params.denominator_field` (both required). Returns `NaN` when the denominator sum collapses to zero. The Aggregation's own `Field` is intentionally ignored — callers can pass the field name as a no-op or leave it empty when smart defaults are disabled.
- **`AGG_CI_LOWER` / `AGG_CI_UPPER`** — `Params.confidence` (default `0.95`, must lie in `(0, 1)`), `Params.method` (default `"normal"`). The `"normal"` method uses sample variance `M2 / (n-1)` and the Beasley-Springer-Moro inverse-normal quantile; `"bootstrap"` is reserved for a buffered follow-up and returns `PROCESSING_CONFIG` today. Both bounds return `NaN` when `n < 2`.

All four pass the chain gate (`processing.CanChainRequest`) — they emit a single scalar per output row, so they compose with `pulse.ProcessChain` for source-rooted dashboards that materialise weighted/ratio aggregates over multi-shard cohorts. `AGG_LIFT` and `AGG_SHARE` (the two remaining cohort aggregates from the Prism upstream plan) are deferred to a follow-up that adds a global-context hook to the Aggregator interface.
</rule>

<rule severity="caveat" topic="welford-aggregator">
## Welford triple aggregator

`AGG_WELFORD` folds the streaming Welford-Pébaÿ recurrence over a numeric field and emits the per-cell triple `(mean, sample_variance, n)` via `RichAggregator`. The cell payload is a `processing.WelfordTriple{Mean, Variance float64; N uint64}` returned by value; the scalar `Value()` fallback is the running mean (`NaN` when `N == 0`) so consumers that do not type-assert the Rich path still see a defensible scalar.

- **Variance denominator.** Sample variance `M2 / (n - 1)` — the same denominator `TEST_WELCH` consumes via `welfordBucket.sampleVariance()`. The shared recurrence type lives at `processing/welford_bucket.go` and is referenced by both `processing/aggregator_welford.go` and `processing/test_t.go`, so AGG_WELFORD output is byte-equal to a TEST_WELCH per-group variance for the same input stream. Single-row and empty cells yield `Variance = 0` (mirrors `welfordBucket`'s zero-on-`N<2` behaviour).
- **Streamable.** Welford is online — one pass over records, fixed `(n, mean, M2)` state per cell. The aggregator participates in the streaming Process orchestrator without forcing the buffered fallback.
- **MarginReducibility = MarginRecompute.** Variance does not pool by addition — the variance of a union is not the sum (or mean) of per-cell variances. Crosstab row / column / grand margins re-walk raw rows through a fresh `welfordBucket` rather than reducing across already-folded cells.
- **Accepted field types.** Strict scalar numeric only: `u8`, `u16`, `u32`, `u64`, `f32`, `f64`. `decimal128`, `date`, `packed_bool`, `u4`, `categorical_*`, and `set_*` are rejected at factory time with `PROCESSING_CONFIG` — the per-record hot path stays branch-free.
- **Null handling.** Skips nulls the same way the rest of the scalar family does; nulls do not advance `N`.

Example request fragment — Welford triple of a numeric `score` field, grouped by cohort segment:

```json
{
  "groups":       [{"type": "GROUP_CATEGORY", "field": "segment"}],
  "aggregations": [{"type": "AGG_WELFORD", "field": "score", "label": "welford"}]
}
```

Per-cell `Response.Data` rows carry the Rich payload directly:

```json
{"segment": "A", "welford": {"mean": 72.4, "variance": 81.6, "n": 412}}
```

Forward-link: the Rich triple is the consumption surface for the `OVERLAY_T_CELL` Welch t-test overlay and the `OVERLAY_Z_CELL` proportion-z overlay added in S25 — both overlays type-assert `MatrixCell.Value` to `processing.WelfordTriple` and reuse the stored `(mean, variance, n)` to compute the per-cell test statistic without re-walking raw rows. See `skills/overlay-system.md` (per-cell statistical overlays) once that section lands.
</rule>

<rule severity="caveat" topic="decimal-aggregators">
## Decimal aggregators

Aggregations on `decimal128` fields are dispatched to a decimal-aware path that preserves precision (nullability is orthogonal — set `Nullable: true` on the field to opt into the per-record null bitmap):

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

Numeric-only (13) — applying to a categorical field emits `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`:
`AGG_SUM`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX`, `AGG_RANGE`, `AGG_MEDIAN`, `AGG_PERCENTILE`, `AGG_STDDEV`, `AGG_VARIANCE`, `AGG_SKEWNESS`, `AGG_KURTOSIS`, `AGG_ZSCORE`, `AGG_WELFORD`.

The numeric set is the broader analytics-numeric family (`encoding.FieldType.IsNumericForAnalytics`): the integer / float / decimal types plus the bit-packed encodings `u4`, `packed_bool`, and `date`. `AGG_AVERAGE` on `packed_bool` returns the proportion of `true`; `AGG_AVERAGE` on `u4` returns the mean of the stored ordinals. Null cells (flagged via the per-record bitmap when a field is `Nullable: true`) are excluded from both numerator and denominator. No `ATTR_FORMULA float(field)` cast is needed — and skipping the cast keeps the request on the streaming path that the formula would have forced into the buffered orchestrator.

`AGG_WELFORD` is the one exception to the broad analytics-numeric admission: it enforces the strict scalar numeric subset (`u8`/`u16`/`u32`/`u64`/`f32`/`f64`) — `u4`, `packed_bool`, `date`, and `decimal128` are rejected with `PROCESSING_CONFIG` so the per-record hot path stays branch-free. See the "Welford triple aggregator" caveat above.

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
## Filterers (7)

Filterers run before grouping and aggregation. The `types.Filterer` JSON shape is `{"type", "field", "values", "expression"}`; only the keys relevant to each filterer type are used.

| Type | Config | Effect |
|------|--------|--------|
| FILTER_INCLUDE | `field`, `values` (string list) | Keep records whose field value is in `values`. Categorical values resolved through the field's dictionary; nulls are dropped. |
| FILTER_EXCLUDE | `field`, `values` (string list) | Drop records whose field value is in `values`. Nulls pass through. |
| FILTER_RANGE | `field`, `values` (exactly `[min, max]`) | Keep records where `min <= value <= max` (both bounds inclusive). Nulls are dropped. |
| FILTER_NULL | `field`, `values` (exactly `["is_null"]` or `["is_not_null"]`) | Keep records based on null state of the field. Use `is_null` to keep records where the field is null; `is_not_null` to drop them. |
| FILTER_TRUE | `field`, optional `values=["truthy"]` | Keep records where `field` is logically true. Strict mode (default, omit `values`) requires `field` to be `packed_bool` and matches rows whose value is 1; null rows are dropped. Opt-in `values=["truthy"]` accepts any field type and applies JavaScript `Boolean(value)` semantics — `0`, `NaN`, `""`, and null coerce to falsy and are dropped. |
| FILTER_FALSE | `field`, optional `values=["truthy"]` | Mirror of FILTER_TRUE. Strict mode keeps `packed_bool` rows whose value is 0; null rows are dropped. With `values=["truthy"]`, JS-falsy rows (including null / missing) are kept — same coercion table as FILTER_TRUE. |
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

<example name="filter-true-false">
Keep records where `is_active` (a `packed_bool` field) is true. Strict mode — no `values` key.

```json
{"type": "FILTER_TRUE", "field": "is_active"}
```

Drop records where `is_active` is true (i.e. keep the explicit `false` rows; nulls are dropped).

```json
{"type": "FILTER_FALSE", "field": "is_active"}
```

Opt into JavaScript-style coercion to filter a non-boolean column. The block below keeps any row whose `notes` text is non-empty (empty string and null are dropped):

```json
{"type": "FILTER_TRUE", "field": "notes", "values": ["truthy"]}
```

`values=["truthy"]` is the only opt-in token; without it, `FILTER_TRUE` / `FILTER_FALSE` reject non-`packed_bool` fields with `PROCESSING_CONFIG` so a coercion is never silent. Coercion table mirrors JavaScript `Boolean(value)`:

| Field shape | Falsy values | Everything else |
|---|---|---|
| numeric (u*, f*, date, packed_bool) | `0`, `NaN` | truthy |
| categorical_* (resolved string) | `""` | truthy |
| decimal128 | mantissa == 0 | truthy |
| null / missing | always falsy | — |

Use strict mode whenever the field actually is `packed_bool` — it avoids a per-row map allocation that the coercion path needs.
</example>

<example name="filter-expression">
Keep records that match an expr-lang predicate.

```json
{"type": "FILTER_EXPRESSION", "expression": "score > 90 && active == true"}
```
</example>

<rule severity="must" topic="filterer-components-contract">
## Filterer components — uniform counter contract

Every filterer pass that runs during a Process / Compose request lands in `Response.Components.Filterers[i]` carrying the same three-integer floor regardless of operator. The orchestrator's counter pass (E2-S9) populates the slice in spec order so consumers see one entry per `Request.Filterers[i]` in matching index order.

### Universal floor — `{n_in, n_out, n_null_input}`

- `n_in` — records entering this filter pass.
- `n_out` — records exiting (passing the filter).
- `n_null_input` — records where the filter input field was null (regardless of pass / fail decision).

All 11 registered filterers emit the same shape:
`FILTER_INCLUDE`, `FILTER_EXCLUDE`, `FILTER_RANGE`, `FILTER_EXPRESSION`, `FILTER_NULL`, `FILTER_TRUE`, `FILTER_FALSE`, `FILTER_SET_CONTAINS_ANY`, `FILTER_SET_CONTAINS_ALL`, `FILTER_SET_CONTAINS_NONE`, `FILTER_SET_EQUALS`.

### `n_in` invariant — chained filterers compose

For the filterer at index `i`:

- `n_in_i == n_out_{i-1}` — the prior filterer's exit count is this filterer's entry count.
- `n_in_0 == TotalRecordsAfterDecode` — the first filterer sees every decoded record from the cohort.

Consumers can therefore reconstruct the filter funnel (records dropped per stage, cumulative pass rate, null-input share per stage) directly from `Response.Components.Filterers` without re-reading the request.

### `MetaFilterer` sibling interface

`MetaFilterer` is the filterer-side sibling of `MetaAggregator` and `MetaGrouper`:

```go
type MetaFilterer interface {
    Components() (map[string]any, error)
}
```

- In v1 no built-in filterer overrides the floor — the orchestrator's universal-floor pass fills `{n_in, n_out, n_null_input}` uniformly across all 11 operators.
- Extensions MAY implement `Components() (map[string]any, error)` to add operator-specific keys (for example, a custom range filterer could expose `n_below_min` / `n_above_max`). This extension surface is deferred for v1; see `skills/extension-points.md` for the planned schema-declaration path.
- Returning `(nil, nil)` is the no-op signal — the floor still applies.

### Mergeability

The filterer counters are **`Mergeable`** — `{n_in, n_out, n_null_input}` fold trivially across shards by per-index addition. Parallel-shard and parallel-buffered Process collapse partial counters at terminal flush with no precision concerns (all three are exact integer counts).

### Go access pattern

```go
resp, err := pulse.Process(ctx, req)
if err != nil { return err }
for _, f := range resp.Components.Filterers {
    fmt.Printf("%s: %d→%d (null=%d)\n", f.Label, f.NIn, f.NOut, f.NNullInput)
}
```

The cross-cutting `Response.Components` shape — including the `Aggregations`, `Groupers`, `Crosstab`, and `Run` siblings — is documented in the [response components contract](response-components.md).
</rule>

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

<section title="Set-typed aggregators (multi-select bitmasks)">

For columns typed `set_u8`, `set_u16`, `set_u32`, or `set_u64` (multi-select survey-style fields backed by a shared dictionary), six dedicated aggregators sit alongside the scalar family:

- `AGG_SET_UNION` — bitwise OR across rows. Rich result is the sorted slice of labels selected by at least one contributing row; scalar fallback is the popcount of the union mask.
- `AGG_SET_INTERSECTION` — bitwise AND across rows. Rich result is the labels selected by every contributing row; scalar fallback is the popcount. Margin recompute (AND across cells is NOT equal to AND across all rows in general).
- `AGG_SET_FREQUENCY` — per-bit row count returning a `map[label]int`. Scalar fallback is the max single-label frequency. Smart-default aggregator for `set_*` fields.
- `AGG_SET_CARDINALITY_SUM` — total number of selections across the input (sum of popcounts).
- `AGG_SET_CARDINALITY_AVG` — mean popcount per contributing row.
- `AGG_SET_DISTINCT_VALUES` — count of distinct exact mask values seen (each combination treated atomically).

All six are streamable and mergeable; `INTERSECTION`'s margin is recompute, `CARDINALITY_AVG` is mean-reducible, the rest are summable. UNION / INTERSECTION / FREQUENCY satisfy `RichAggregator` and surface their typed payload through `Response.Data` rows and Crosstab cells.

</section>

<section title="Filtering set columns">

Set fields support four label-resolved filterers:

- `FILTER_SET_CONTAINS_ANY` — keep rows with at least one of the listed labels selected.
- `FILTER_SET_CONTAINS_ALL` — keep rows with every listed label selected.
- `FILTER_SET_CONTAINS_NONE` — drop rows with any listed label selected (null rows pass).
- `FILTER_SET_EQUALS` — exact-mask match. Useful with `GROUP_SET_VALUE`.

Pass labels through `Filterer.Values` exactly as they appear in the schema dictionary; resolution to bitmask happens once at build time. Ad-hoc expressions can also reach for the built-in helpers `contains`, `has_any`, `has_all`, `has_none`, `popcount`, `set_union`, `set_intersect`, `set_diff`, `set_xor` via `FILTER_EXPRESSION` / `ATTR_FORMULA`.

</section>

<section title="Overlays — group-scoped decorations on a grouped Process result">

A grouped Process result (`Request.Groups + Request.Aggregations`) can be decorated with one or more **overlays** — additive, read-only `SeriesPayload`s attached to `Response.Overlays` in matching `Request.Overlays[i]` ↔ `Response.Overlays[i]` slot order. Overlays never mutate the base aggregation rows; they ride alongside carrying derived projections (index score against the grand total, share of grand, standardised z-score, additive delta vs a sibling group, ratio index vs a sibling group) so renderers paint a single host series with one or more decoration layers without re-deriving the math on the client.

Five overlay kinds target the SERIES host (grouped Process). Three stream — they fold a tiny accumulator (one `f64` grand-total or three `f64`s for Welford count+mean+M2) alongside the per-group accumulators inside the streaming Process pass with no second pass over records. Two are buffered — sibling resolution needs the finalised SeriesPayload before the `(Field, Value)` lookup can run, so the handler runs at the post-finalize exit.

Mixed-mode downgrade rule (E3-S6): when a single Request carries one streamable overlay and one buffered overlay, the WHOLE Request runs buffered — `processing.CanStreamRequest` short-circuits to `false` when any spec is non-streamable, mirroring how `AGG_MEDIAN` forces the whole streaming pass into the buffered orchestrator.

For the general overlay framework — the three-shape model (scalar / series / matrix), the parallel-slice contract for series payloads (entry `i` aligns with host axis-key `i`), the validation rules, the manifest capability block, the `OverlaySummary` shape, and the recipe for adding a new kind — see `skills/overlay-system.md`. For Crosstab-host overlays (share triad + margin-comparison family + χ² / Fisher inferential family) see `skills/crosstab-guide.md` ("Overlays" section).

#### Quick reference table — grouped Process overlays (E3)

| Kind | Scope | Shape | Streamable | Ref family | Math |
|---|---|---|---|---|---|
| `OVERLAY_INDEX_VS_TOTAL` | `group` | `series` | yes | — (implicit-grand-total) | `(group_val / grand_total) * 100.0` |
| `OVERLAY_SHARE_OF_TOTAL` | `group` | `series` | yes | — (implicit-grand-total) | `group_val / grand_total` (raw share — no ×100) |
| `OVERLAY_ZSCORE_VS_TOTAL` | `group` | `series` | yes | — (implicit-grand-total) | `(group_val - mean) / sd` where `mean`/`sd` are population stats over the N present groups (Welford-Pébaÿ) |
| `OVERLAY_DELTA_VS_SIBLING` | `group` | `series` | no (buffered) | `Sibling` (`Field` + `Value`) | `group_val - sibling_val` against `Ref.Sibling.{Field, Value}` |
| `OVERLAY_INDEX_VS_SIBLING` | `group` | `series` | no (buffered) | `Sibling` (`Field` + `Value`) | `(group_val / sibling_val) * 100.0` against `Ref.Sibling.{Field, Value}` |

Every layer produces a `SeriesPayload.Entries` slice with one `SeriesEntry` per host group key in host order. Each entry carries its score on `Summary.Statistic`. Absent host groups (the resolver reports `(0, false)`) surface a present `SeriesEntry` whose Summary leaves `Statistic` unset and do NOT contribute to the grand total / Welford / sibling accumulators.

Use `pulse predict --json` to confirm the streamability classification before an expensive overlay against a large cohort.

#### Recipes — streamable (implicit-grand-total)

The three streamable kinds share one denominator surface — the host's grand total over post-filter rows. `Ref` MUST be empty; supplying any ref-family pointer fires `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` MUST be zero; non-zero values fire `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Zero grand total emits ONE `PULSE_OVERLAY_REF_ZERO` warning and populates every entry's `Summary.Statistic` with NaN.

##### `OVERLAY_INDEX_VS_TOTAL`

One-liner: per-group index against the grand total. Baseline is 100. Renderers centre diverging colour ramps on `baseline = 100`. Mirrors the runnable example `examples/overlays/06_process_index_vs_total.json`.

Request:

```json
{
  "cohort": {"filename": "experiment.pulse", "data_dir": ".data"},
  "groups":       [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "revenue"}],
  "overlays": [
    {"name": "i_total", "kind": "OVERLAY_INDEX_VS_TOTAL", "scope": "group"}
  ]
}
```

Expected `Response.Overlays[0]` shape — given regions north / south / east / west with revenues `400 / 600 / 300 / 700` (grand total `2000`):

```json
{
  "name":  "i_total",
  "kind":  "OVERLAY_INDEX_VS_TOTAL",
  "scope": "group",
  "payload": {
    "shape": "series",
    "series": {
      "entries": [
        {"key": ["north"], "summary": {"statistic": 20.0}},
        {"key": ["south"], "summary": {"statistic": 30.0}},
        {"key": ["east"],  "summary": {"statistic": 15.0}},
        {"key": ["west"],  "summary": {"statistic": 35.0}}
      ]
    }
  },
  "summary": {"baseline": 100.0}
}
```

##### `OVERLAY_SHARE_OF_TOTAL` (SERIES dispatch)

One-liner: per-group raw share of grand total. Entries over a complete partition sum to 1.0 within ULP. Baseline is 1.0. Note this kind is dual-shape — the SERIES dispatch is the streamable one; the MATRIX dispatch against a crosstab host is buffered (see `skills/crosstab-guide.md`).

```json
{
  "groups":       [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "revenue"}],
  "overlays": [
    {"name": "s_total", "kind": "OVERLAY_SHARE_OF_TOTAL", "scope": "group"}
  ]
}
```

Sibling of `OVERLAY_INDEX_VS_TOTAL`. A Request carrying BOTH overlays folds the grand total ONCE — the streaming Process orchestrator shares the `computeSeriesGrandTotal` accumulator between layers.

##### `OVERLAY_ZSCORE_VS_TOTAL`

One-liner: per-group standardised z-score against the population distribution of N present groups. `mean = Σ group_val / N`, `sd = sqrt(M2 / N)` (population variance — divide by N, not N-1). Baseline is 0 — positive groups are above mean, negative groups are below. The streaming pass folds Welford over GROUPS, not raw records — variance is across N groups (distinct from `ATTR_ZSCORE`'s record-level semantics).

```json
{
  "groups":       [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "revenue"}],
  "overlays": [
    {"name": "z_total", "kind": "OVERLAY_ZSCORE_VS_TOTAL", "scope": "group"}
  ]
}
```

Zero variance (every present group equal, single-present-group, or every-group-zero) emits one `PULSE_OVERLAY_REF_ZERO` warning and populates every present entry's `Summary.Statistic` with NaN.

#### Recipes — buffered (sibling reference)

The two SIBLING kinds compare every group against ONE fixed reference group named by `Ref.Sibling.{Field, Value}`. The caller authors a valid `(Field, Value)` pair — `Field` MUST be a grouper Field on the host, `Value` MUST match an observed axis-key value (the sibling resolver runs a single `(field, value)` lookup against the host's group-key list at `processing/overlay_sibling_resolver.go`). Unknown sibling emits ONE `PULSE_OVERLAY_REF_UNKNOWN` warning and surfaces NaN statistics across every present entry. `Level` / `Within` MUST be zero (the sibling reference is a single fixed group, not an axis prefix); non-zero values fire `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Both kinds are buffered today — sibling resolution requires the finalised SeriesPayload.

##### `OVERLAY_DELTA_VS_SIBLING`

One-liner: per-group additive delta against a sibling group. The sibling group itself emits `0` (self-vs-self under additive subtraction). Output preserves the host cell's units. Baseline is 0. Does NOT raise `PULSE_OVERLAY_REF_ZERO` when sibling resolves to zero — subtraction by zero recovers the host's raw value (distinct from the INDEX_VS_SIBLING twin which divides). Mirrors the runnable example `examples/overlays/07_process_delta_vs_sibling.json`.

Request:

```json
{
  "cohort": {"filename": "experiment.pulse", "data_dir": ".data"},
  "groups":       [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "revenue"}],
  "overlays": [
    {
      "name":  "d_sibling",
      "kind":  "OVERLAY_DELTA_VS_SIBLING",
      "scope": "group",
      "ref":   {"sibling": {"field": "region", "value": "north"}}
    }
  ]
}
```

Expected `Response.Overlays[0]` shape — given the same `400 / 600 / 300 / 700` setup with `north = 400` as the sibling:

```json
{
  "name":  "d_sibling",
  "kind":  "OVERLAY_DELTA_VS_SIBLING",
  "scope": "group",
  "payload": {
    "shape": "series",
    "series": {
      "entries": [
        {"key": ["north"], "summary": {"statistic":    0.0}},
        {"key": ["south"], "summary": {"statistic":  200.0}},
        {"key": ["east"],  "summary": {"statistic": -100.0}},
        {"key": ["west"],  "summary": {"statistic":  300.0}}
      ]
    }
  },
  "summary": {"baseline": 0.0}
}
```

##### `OVERLAY_INDEX_VS_SIBLING`

One-liner: per-group ratio index against a sibling group. The sibling group itself emits `100.0` (self-vs-self under the ratio scaling: `sibling / sibling * 100 = 100`). Baseline is 100. Emits ONE `PULSE_OVERLAY_REF_ZERO` warning and surfaces NaN when sibling resolves to zero — division by zero is mathematically undefined.

```json
{
  "groups":       [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "revenue"}],
  "overlays": [
    {
      "name":  "i_sibling",
      "kind":  "OVERLAY_INDEX_VS_SIBLING",
      "scope": "group",
      "ref":   {"sibling": {"field": "region", "value": "north"}}
    }
  ]
}
```

#### Combining multiple overlays

Multiple specs ride the same `Request.Overlays` slice — each produces one layer in matching index order. A Request carrying every E3 kind streams the three implicit-grand-total layers and falls back to the buffered orchestrator for the two SIBLING layers under the mixed-mode downgrade rule (the whole Request runs buffered when any spec is non-streamable):

```json
{
  "overlays": [
    {"name": "i_total",   "kind": "OVERLAY_INDEX_VS_TOTAL",   "scope": "group"},
    {"name": "s_total",   "kind": "OVERLAY_SHARE_OF_TOTAL",   "scope": "group"},
    {"name": "z_total",   "kind": "OVERLAY_ZSCORE_VS_TOTAL",  "scope": "group"},
    {"name": "d_sibling", "kind": "OVERLAY_DELTA_VS_SIBLING", "scope": "group",
     "ref": {"sibling": {"field": "region", "value": "north"}}},
    {"name": "i_sibling", "kind": "OVERLAY_INDEX_VS_SIBLING", "scope": "group",
     "ref": {"sibling": {"field": "region", "value": "north"}}}
  ]
}
```

`Response.Overlays` carries five layers, indices 0 / 1 / 2 / 3 / 4, in spec order. Renderers can offer the user a "switch denominator" dropdown without re-issuing the request.

</section>

<see_also>
- attribute-composition — per-record attributes (including ATTR_ZSCORE).
- grouper-design — how groupers partition data before aggregation runs.
- cohort-schema-design — set_u8/u16/u32/u64 field type semantics.
- overlay-system — the general overlay framework (kinds × shapes × scopes × refs taxonomy).
- crosstab-guide — Crosstab-host overlay catalogue (share triad, margin-comparison, χ² / Fisher).
- streaming-and-watching — per-kind streamability cross-reference and the mixed-mode downgrade rule.
</see_also>
