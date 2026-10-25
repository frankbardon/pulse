package types

import (
	"encoding/json"

	"github.com/frankbardon/pulse/internal/returnplan"
)

// This file defines the request/response types for Pulse as plain Go structs
// with JSON tags. These types are the JSON-serializable shapes used by the
// processing pipeline, CLI --json envelopes, and api predict.
//
// Design note: these are plain Go structs rather than protobuf-generated code.
// This is acceptable for v0.x; the types can be migrated to protobuf later.
// The key requirement is that JSON round-tripping works correctly.

// AggregationType identifies a specific aggregation operation.
type AggregationType string

const (
	AGG_COUNT          AggregationType = "AGG_COUNT"
	AGG_SUM            AggregationType = "AGG_SUM"
	AGG_AVERAGE        AggregationType = "AGG_AVERAGE"
	AGG_MIN            AggregationType = "AGG_MIN"
	AGG_MAX            AggregationType = "AGG_MAX"
	AGG_STDDEV         AggregationType = "AGG_STDDEV"
	AGG_RANGE          AggregationType = "AGG_RANGE"
	AGG_MODE_COUNT     AggregationType = "AGG_MODE_COUNT"
	AGG_ZSCORE         AggregationType = "AGG_ZSCORE"
	AGG_MEDIAN         AggregationType = "AGG_MEDIAN"
	AGG_VARIANCE       AggregationType = "AGG_VARIANCE"
	AGG_MODE           AggregationType = "AGG_MODE"
	AGG_SKEWNESS       AggregationType = "AGG_SKEWNESS"
	AGG_KURTOSIS       AggregationType = "AGG_KURTOSIS"
	AGG_DISTINCT_COUNT AggregationType = "AGG_DISTINCT_COUNT"
	AGG_PERCENTILE     AggregationType = "AGG_PERCENTILE"

	// AGG_FREQUENCY counts the non-null rows whose field equals one
	// chosen value. Params: `value` (string or number, required),
	// matched as FILTER_INCLUDE matches a value (a categorical label,
	// otherwise a number). Returns the count as a scalar; a value no row
	// holds counts 0. The modal count is AGG_MODE_COUNT. Streamable +
	// mergeable (two integer counters).
	AGG_FREQUENCY AggregationType = "AGG_FREQUENCY"

	// AGG_NULL_COUNT counts records where the field is null. Inverse of
	// AGG_COUNT, which counts records where the field is non-null. Stays
	// streamable (O(1) state per row, no buffering).
	AGG_NULL_COUNT AggregationType = "AGG_NULL_COUNT"

	// AGG_WEIGHTED_MEAN computes a weighted arithmetic mean of the
	// target field using a separately-named weight field. Params:
	// `weight_field` (string, required). Emits sum(field * w) / sum(w).
	// Streamable + mergeable via weighted Chan-Welford.
	AGG_WEIGHTED_MEAN AggregationType = "AGG_WEIGHTED_MEAN"

	// AGG_DISTINCT_SUM sums the target field ONCE PER DISTINCT KEY.
	// Params: `distinct_by` (string, required) names the key field;
	// the Aggregation's own Field names the value summed. A key seen
	// on N records contributes its value once, not N times — the
	// motivating shape is a survey weight that repeats across every
	// row a respondent produced.
	//
	// FIRST VALUE WINS: when one key carries conflicting values across
	// its records, the first value observed is the one summed and every
	// later value for that key is ignored. See the aggregator's own doc
	// comment in internal/processing/aggregator_distinct_sum.go.
	//
	// Components() additionally emits the DISTINCT COUNT alongside the
	// sum, so one operator yields both figures from one scan and the
	// two can never come from different passes over the data.
	// Streamable + mergeable (per-key maps union).
	AGG_DISTINCT_SUM AggregationType = "AGG_DISTINCT_SUM"

	// AGG_RATIO emits sum(numerator_field) / sum(denominator_field) at
	// finalize. Params: `numerator_field` + `denominator_field`. The
	// Aggregation's own Field is ignored. Streamable + mergeable (two
	// independent sums).
	AGG_RATIO AggregationType = "AGG_RATIO"

	// AGG_MAPE, AGG_RMSE and AGG_MAE score a prediction against an
	// actual: Field is the actual, params.prediction_field the
	// prediction (typically an ATTR_REG_PREDICT column). MAPE is the
	// mean of |y − ŷ| / |y| as a FRACTION (rows with y == 0 excluded and
	// counted in the n_zero_actual component), RMSE the square root of
	// the mean of (y − ŷ)², MAE the mean of |y − ŷ|. A null on either
	// side skips the row. Weighted: Σw·loss / Σw. Streamable + mergeable
	// (additive sums).
	AGG_MAPE AggregationType = "AGG_MAPE"
	AGG_RMSE AggregationType = "AGG_RMSE"
	AGG_MAE  AggregationType = "AGG_MAE"

	// AGG_AUC is the area under the ROC curve of a score against a
	// binary outcome: Field is the score, params.outcome the 0/1 (or
	// packed_bool) outcome, params.positive (0 or 1, default 1) the
	// event value. Exact rank-sum (Mann-Whitney) with ties counted as
	// half; weighted, the weighted probability a positive outranks a
	// negative. Components carry gini (2·AUC − 1) and the DeLong
	// standard error and interval (null under a weight). Buffered (a
	// sort), never streamable or mergeable.
	AGG_AUC AggregationType = "AGG_AUC"

	// AGG_CI_LOWER and AGG_CI_UPPER emit the lower / upper bound of a
	// confidence interval for the mean of the named numeric field.
	// Params: `confidence` (float, default 0.95), `method` (string,
	// default "normal"). The "normal" method uses Welford mean + sample
	// variance with z = inv_normal((1+confidence)/2); streamable +
	// mergeable. The "bootstrap" method is reserved for a buffered
	// follow-up — returns PROCESSING_CONFIG today.
	AGG_CI_LOWER AggregationType = "AGG_CI_LOWER"
	AGG_CI_UPPER AggregationType = "AGG_CI_UPPER"

	// AGG_WELFORD emits the streaming Welford-Pébaÿ triple — running
	// mean, running sample variance (n-1 denominator), and observed
	// count — over a numeric field. Returns the running mean through
	// the legacy float64 contract and the typed WelfordTriple via the
	// RichAggregator interface so overlay handlers (OVERLAY_T_CELL /
	// OVERLAY_Z_CELL) can type-switch on the moment payload
	// without re-deriving variance from raw rows. Streamable; mergeable
	// via Chan-Welford. Margin reducibility is recompute (variance does
	// not pool by addition). Field type restricted to the strict scalar
	// numeric family (u8/u16/u32/u64/f32/f64); decimal128 deferred.
	AGG_WELFORD AggregationType = "AGG_WELFORD"

	// Set-typed aggregators. Each consumes a single set_* field whose
	// per-row payload is a fixed-width bitmask over a shared dictionary;
	// bit i ↔ dictionary entry i.

	// AGG_SET_UNION reduces by bitwise OR across rows. The result is a
	// uint64 mask representing the union of every selection observed.
	// Mergeable + streamable; margin-summable for crosstab.
	AGG_SET_UNION AggregationType = "AGG_SET_UNION"

	// AGG_SET_INTERSECTION reduces by bitwise AND across rows. The
	// result mask carries a bit only when every contributing row had
	// that bit set. Mergeable + streamable, but NOT margin-reducible
	// (AND across cells ≠ AND across all rows in general) — crosstab
	// margins recompute from raw rows.
	AGG_SET_INTERSECTION AggregationType = "AGG_SET_INTERSECTION"

	// AGG_SET_FREQUENCY emits a per-element histogram. The result is
	// map[label]int — one entry per dictionary value, counting the rows
	// whose mask had that bit set. Mergeable (bin-by-bin sum) + summable
	// margin. This is the survey-friendly default ("respondents per
	// issuer"). Used as a Crosstab Cell aggregator the result is a
	// map-valued cell payload.
	AGG_SET_FREQUENCY AggregationType = "AGG_SET_FREQUENCY"

	// AGG_SET_CARDINALITY_SUM sums popcount(mask) across rows. Mergeable
	// + streamable + summable margin.
	AGG_SET_CARDINALITY_SUM AggregationType = "AGG_SET_CARDINALITY_SUM"

	// AGG_SET_CARDINALITY_AVG returns mean popcount(mask) per row.
	// Mergeable + streamable; margin = mean-reducible (needs per-cell n).
	AGG_SET_CARDINALITY_AVG AggregationType = "AGG_SET_CARDINALITY_AVG"

	// AGG_SET_DISTINCT_VALUES counts the distinct exact masks observed
	// (treats each combination as atomic). Mergeable via the union of
	// seen-mask sets.
	AGG_SET_DISTINCT_VALUES AggregationType = "AGG_SET_DISTINCT_VALUES"

	// Vector aggregators (U27). Each reads a vector input — the slot's
	// Vector (a Request.Vectors name) or inline Fields, never Field —
	// and emits one figure PER MEMBER, in member order: a JSON array
	// through the RichAggregator lift (ArrayValued). Missing data is per
	// element: a null member skips that element only. AGG_VEC_MEAN is
	// Σw·x / Σw per element (null when no row answered it), AGG_VEC_SUM
	// Σw·x per element (0 when none did), AGG_VEC_SD the per-element
	// sample standard deviation — √(M2 / (N* − 1)) with N* = Σw
	// (unweighted: n; frequency: Σw) or Kish n_eff (probability), null
	// when N* ≤ 1. Streamable and mergeable: per-merge-block partials
	// (sums; for AGG_VEC_SD also the weighted Welford moments, combined
	// by Chan's rule) folded in a fixed tree, so the figure is
	// bit-identical for every worker count. Refused by crosstab, Sort,
	// windows, post-tests, overlays and a chain stage that feeds another
	// stage. params.expand writes one scalar column per member,
	// "<label>[<element label>]", instead of the array — ordinary f64
	// columns Sort, windows, post-tests, series overlays and a later
	// chain stage read; the bare label stays refused.
	AGG_VEC_MEAN AggregationType = "AGG_VEC_MEAN"
	AGG_VEC_SUM  AggregationType = "AGG_VEC_SUM"
	AGG_VEC_SD   AggregationType = "AGG_VEC_SD"
)

// AllAggregationTypes returns all defined aggregation types.
func AllAggregationTypes() []AggregationType {
	return []AggregationType{
		AGG_COUNT, AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX,
		AGG_STDDEV, AGG_RANGE, AGG_MODE_COUNT, AGG_ZSCORE,
		AGG_MEDIAN, AGG_VARIANCE, AGG_MODE, AGG_SKEWNESS, AGG_KURTOSIS,
		AGG_DISTINCT_COUNT, AGG_PERCENTILE,
		AGG_FREQUENCY,
		AGG_NULL_COUNT,
		AGG_WEIGHTED_MEAN, AGG_RATIO,
		AGG_DISTINCT_SUM,
		AGG_CI_LOWER, AGG_CI_UPPER,
		AGG_WELFORD,
		AGG_MAPE, AGG_RMSE, AGG_MAE,
		AGG_AUC,
		AGG_SET_UNION, AGG_SET_INTERSECTION, AGG_SET_FREQUENCY,
		AGG_SET_CARDINALITY_SUM, AGG_SET_CARDINALITY_AVG,
		AGG_SET_DISTINCT_VALUES,
		AGG_VEC_MEAN, AGG_VEC_SUM, AGG_VEC_SD,
	}
}

// FiltererType identifies a specific filter operation.
type FiltererType string

const (
	FILTER_INCLUDE    FiltererType = "FILTER_INCLUDE"
	FILTER_EXCLUDE    FiltererType = "FILTER_EXCLUDE"
	FILTER_RANGE      FiltererType = "FILTER_RANGE"
	FILTER_EXPRESSION FiltererType = "FILTER_EXPRESSION"

	// FILTER_NULL keeps or drops records based on null state of a field.
	// Mode is "is_null" (keep null-valued records) or "is_not_null" (keep
	// non-null records). Row-local; streamable.
	FILTER_NULL FiltererType = "FILTER_NULL"

	// FILTER_TRUE keeps records where Field is logically true. Default
	// (strict) mode requires Field to be packed_bool and keeps records
	// whose value is 1. Opt-in JavaScript-style truthiness coercion is
	// enabled with Values=["truthy"]; in that mode any field type is
	// accepted and the same rules JS uses for `Boolean(value)` apply
	// (0, NaN, empty string, null → falsy; everything else → truthy).
	// Row-local; streamable.
	FILTER_TRUE FiltererType = "FILTER_TRUE"

	// FILTER_FALSE keeps records where Field is logically false. Default
	// (strict) mode requires Field to be packed_bool and keeps records
	// whose value is 0. Opt-in JavaScript-style falsiness coercion is
	// enabled with Values=["truthy"]; in that mode any field type is
	// accepted and records whose value would coerce to falsy under
	// `Boolean(value)` are kept (0, NaN, empty string, null → kept).
	// Row-local; streamable.
	FILTER_FALSE FiltererType = "FILTER_FALSE"

	// Set-typed filterers. Each takes a set_* Field plus a Values []string
	// list of dictionary labels; the builder resolves the labels into a
	// query mask at compile time so the per-row check is a single bitwise
	// op. Row-local; streamable.

	// FILTER_SET_CONTAINS_ANY keeps records whose set mask shares at
	// least one bit with the resolved query mask (row & q != 0).
	FILTER_SET_CONTAINS_ANY FiltererType = "FILTER_SET_CONTAINS_ANY"

	// FILTER_SET_CONTAINS_ALL keeps records whose set mask has every bit
	// in the resolved query mask set (row & q == q).
	FILTER_SET_CONTAINS_ALL FiltererType = "FILTER_SET_CONTAINS_ALL"

	// FILTER_SET_CONTAINS_NONE keeps records whose set mask shares no
	// bits with the resolved query mask (row & q == 0).
	FILTER_SET_CONTAINS_NONE FiltererType = "FILTER_SET_CONTAINS_NONE"

	// FILTER_SET_EQUALS keeps records whose set mask exactly equals the
	// resolved query mask.
	FILTER_SET_EQUALS FiltererType = "FILTER_SET_EQUALS"

	// FILTER_DATE_RANGES keeps records whose `date` Field falls inside any
	// of a validated set of labeled date ranges (the shared E1-S1 model),
	// dropping every other row and every null date. Ranges ride the
	// structured Params slot (`{ranges: [{label, start, end}]}`); the
	// label is irrelevant to keep/drop but is still fully validated
	// (overlap/duplicate/empty/invalid). Row-local; streamable.
	FILTER_DATE_RANGES FiltererType = "FILTER_DATE_RANGES"
)

// AllFiltererTypes returns all defined filterer types.
func AllFiltererTypes() []FiltererType {
	return []FiltererType{
		FILTER_DATE_RANGES,
		FILTER_EXCLUDE,
		FILTER_EXPRESSION,
		FILTER_FALSE,
		FILTER_INCLUDE,
		FILTER_NULL,
		FILTER_RANGE,
		FILTER_SET_CONTAINS_ALL,
		FILTER_SET_CONTAINS_ANY,
		FILTER_SET_CONTAINS_NONE,
		FILTER_SET_EQUALS,
		FILTER_TRUE,
	}
}

// GroupType identifies a specific grouping operation.
type GroupType string

const (
	GROUP_CATEGORY GroupType = "GROUP_CATEGORY"
	GROUP_ROUNDED  GroupType = "GROUP_ROUNDED"
	GROUP_RANGE    GroupType = "GROUP_RANGE"
	GROUP_QUANTILE GroupType = "GROUP_QUANTILE"
	GROUP_DATE     GroupType = "GROUP_DATE"

	// GROUP_DATE_RANGES buckets each date row by a validated set of
	// labeled date ranges (inline {label, start, end} specs). The bucket
	// key is the matching range's label; rows outside every range land in
	// a configurable unmatched bucket (default label "unmatched").
	// Streamable + mergeable (each row maps independently to one bucket).
	GROUP_DATE_RANGES GroupType = "GROUP_DATE_RANGES"

	// GROUP_NUMERIC_RANGES buckets each numeric row by a validated set
	// of labeled half-open ranges ({label, start, end}, [start, end); an
	// omitted bound is open), inline or from a numeric range table. The
	// bucket key is the matching range's label; rows outside every range
	// land in a configurable unmatched bucket (default "unmatched").
	// Buckets emit in supplied range order. Streamable + mergeable.
	GROUP_NUMERIC_RANGES GroupType = "GROUP_NUMERIC_RANGES"

	// GROUP_SET_VALUE treats the set mask as an atomic value. Each
	// distinct mask is its own bucket; the bucket key is the sorted
	// pipe-delimited label list (e.g. "AMEX|VISA"). One row → one
	// bucket. Streamable + mergeable.
	GROUP_SET_VALUE GroupType = "GROUP_SET_VALUE"

	// GROUP_SET_PER_ELEMENT explodes the set: each row fans into one
	// bucket per set bit, keyed by the resolved dictionary label. One
	// row → N buckets (where N = popcount(mask)). Cardinality multiplies.
	// Streamable via the multi-key streaming hook (KeysForRow on
	// StreamingGrouper). This is the survey-friendly default
	// ("respondents per option").
	GROUP_SET_PER_ELEMENT GroupType = "GROUP_SET_PER_ELEMENT"

	// GROUP_KMEANS segments records into params.k clusters by seeded,
	// deterministic k-means over the p members of Vector / Fields
	// (Euclidean; z-scored by default). Keys are cluster_1..cluster_k,
	// numbered by descending cluster size and emitted in that order; a
	// row with a null member is skipped (n_null). Buffered and
	// non-mergeable: the fit needs every row.
	GROUP_KMEANS GroupType = "GROUP_KMEANS"
)

// AllGroupTypes returns all defined group types.
func AllGroupTypes() []GroupType {
	return []GroupType{
		GROUP_CATEGORY,
		GROUP_DATE,
		GROUP_DATE_RANGES,
		GROUP_KMEANS,
		GROUP_NUMERIC_RANGES,
		GROUP_QUANTILE,
		GROUP_RANGE,
		GROUP_ROUNDED,
		GROUP_SET_PER_ELEMENT,
		GROUP_SET_VALUE,
	}
}

// AttributeType identifies a specific derived-attribute computation.
type AttributeType string

const (
	ATTR_ZSCORE     AttributeType = "ATTR_ZSCORE"
	ATTR_TSCORE     AttributeType = "ATTR_TSCORE"
	ATTR_NORMALIZED AttributeType = "ATTR_NORMALIZED"
	ATTR_FORMULA    AttributeType = "ATTR_FORMULA"
	ATTR_PERCENTILE AttributeType = "ATTR_PERCENTILE"
	ATTR_DATE_PART  AttributeType = "ATTR_DATE_PART"

	// ATTR_REG_FITTED emits the per-row fitted value ŷᵢ = Xᵢ · β + β₀ for
	// each filter-passing record, using a regression fit computed in a
	// streaming prepass over the same record set. Two-pass: prepass folds
	// the OLS sufficient statistics, finalize freezes the coefficient
	// vector, pass 2 emits ŷᵢ. Carries its own RegressionSpec-shaped
	// fields (Target, Predictors, Penalty, Alpha, L1Ratio); refits
	// internally per attribute (Option A). Accepts any OLS penalty
	// (unpenalized, ridge, lasso, elasticnet); GLM and Bayesian fits are
	// deferred.
	ATTR_REG_FITTED AttributeType = "ATTR_REG_FITTED"

	// ATTR_REG_RESIDUAL emits the per-row residual yᵢ − ŷᵢ for each
	// filter-passing record, using the same OLS prepass machinery as
	// ATTR_REG_FITTED. Sums to ≈ 0 when the fit includes an intercept
	// (always true for unpenalized OLS) and the response is observed for
	// every contributing row.
	ATTR_REG_RESIDUAL AttributeType = "ATTR_REG_RESIDUAL"

	// ATTR_REG_LEVERAGE emits the per-row hat-matrix diagonal
	// hᵢᵢ = xᵢ · (XᵀX)⁻¹ · xᵢᵀ for each filter-passing record. Restricted
	// to unpenalized OLS — penalized leverage uses a different formula
	// (involving the regularized resolvent) and GLM leverage requires the
	// IRLS weight matrix; both deferred to Phase 9. Specs with any
	// Penalty set are rejected at factory time with PROCESSING_CONFIG.
	ATTR_REG_LEVERAGE AttributeType = "ATTR_REG_LEVERAGE"

	// ATTR_REG_PREDICT scores each row from a BOUND model — a fit named
	// in the request (ComposedRequest `fits`) — instead of refitting:
	// intercept + Σ coefficient·predictor at the fit's full precision,
	// the GLM inverse link applied on the response scale (default) or
	// not (params.scale "link"). Params {fit, terms?, scale?}; `terms`
	// scores a subset of the model's terms (the intercept only when
	// listed as "(intercept)"). The fit's predictor list is authoritative
	// and read off the slot's own columns; a null in any column the
	// score reads yields null. Row-local once bound: streamable,
	// chain / merge eligible, materialize-admissible.
	ATTR_REG_PREDICT AttributeType = "ATTR_REG_PREDICT"

	// ATTR_SET_POPCOUNT emits popcount(mask) per row as a numeric column.
	// Field must reference a set_* field. Streamable.
	ATTR_SET_POPCOUNT AttributeType = "ATTR_SET_POPCOUNT"

	// ATTR_SET_HAS emits a packed_bool per row indicating whether the
	// set field has a specific dictionary label set. Params.value names
	// the label whose bit is tested. Streamable.
	ATTR_SET_HAS AttributeType = "ATTR_SET_HAS"

	// ATTR_CODE_IN emits a packed_bool (0/1) per row: 1 when the row's
	// Field value is one of Params.codes, else 0 (a null input reads 0).
	// Accepts categorical_* (codes are dictionary labels) and u4..u64
	// (codes are whole numbers). Row-local and streamable.
	ATTR_CODE_IN AttributeType = "ATTR_CODE_IN"

	// ATTR_MAHALANOBIS emits each row's Mahalanobis distance from a
	// fitted centre across the p members of Vector / Fields: D² =
	// (x − μ)ᵀ S⁻¹ (x − μ), μ the (weighted) fit mean and S the ddof = 1
	// covariance, or (params.output "p") the χ² upper tail of D² on p
	// degrees of freedom. Two-pass: the fit folds one co-moment over the
	// filtered rows params.fit_on admits ("filtered", the default, or an
	// inline filterer list); every filtered row is scored. A null member
	// gives a null score; a singular fit or N* ≤ p nulls every score with
	// a warning.
	ATTR_MAHALANOBIS AttributeType = "ATTR_MAHALANOBIS"

	// ATTR_PC_SCORE emits each row's score on one principal component
	// (params.component, 1-based) of the p members of Vector / Fields,
	// standardized to unit variance on the fit population: the
	// eigen-decomposition of the fit's correlation (params.on, the
	// default) or covariance, with MAT_PCA's order and sign convention.
	// params.rotation (an orthogonal criterion: "varimax", "quartimax",
	// "geomin_orthogonal", "entropy") scores the component after rotating
	// the params.components leading ones (MAT_PCA's rotation). Two-pass with
	// the fitted-attribute rules of ATTR_MAHALANOBIS (fit_on, listwise,
	// null member → null, singular or N* ≤ p → every score null).
	ATTR_PC_SCORE AttributeType = "ATTR_PC_SCORE"

	// ATTR_FACTOR_SCORE emits each row's score on one common factor
	// (params.factor, 1-based) of the p members of Vector / Fields: an
	// inline MAT_FACTOR fit of the fit rows' correlation (params
	// factors, extraction, rotation, kappa, gamma — MAT_FACTOR's knobs,
	// order and sign), scored by params.method "regression" (Thurstone,
	// the default) or "bartlett" on the members standardized by the fit.
	// Two-pass with the fitted-attribute rules of ATTR_MAHALANOBIS
	// (fit_on, listwise, null member → null, singular or N* ≤ p → every
	// score null).
	ATTR_FACTOR_SCORE AttributeType = "ATTR_FACTOR_SCORE"

	// ATTR_SEM_SCORE emits each row's score on one latent variable
	// (params.latent) of a structural equation model fitted inline on
	// the fit rows — MAT_SEM's fit and knobs (params.model,
	// meanstructure, group, group_equal, …) over the p members of
	// Vector / Fields matched by label — scored by params.method
	// "regression" (the default) or "bartlett" (lavaan's lavPredict).
	// With a mean structure the scores sit on the latent-mean scale;
	// without one they are centered at the fit rows. Under params.group
	// a row is scored with its own group's parameters (an unseen level
	// or a null group scores null). Two-pass with the fitted-attribute
	// rules (fit_on, listwise, null member → null, a fit that fails →
	// every score null).
	ATTR_SEM_SCORE AttributeType = "ATTR_SEM_SCORE"

	// ATTR_SCALE_SCORE emits each row's rating-battery score across the
	// p members of Vector / Fields: the mean of the row's valid items
	// (params.stat "mean", the default) or the PRORATED sum, that mean ×
	// p ("sum"). params.min_valid (default p) is the fewest valid items a
	// score needs — below it the score is null, never 0. params.reverse
	// names reverse-keyed member labels, scored lo + hi − x on the
	// declared range (params.scale_min / scale_max, else the kind "scale"
	// vector's bounds; a reverse list with neither is refused). With a
	// declared range an out-of-range value is MISSING (never clamped),
	// counted and warned (PULSE_ATTR_SCALE_OUT_OF_RANGE). Row-local:
	// single pass, streamable; the weight is irrelevant.
	ATTR_SCALE_SCORE AttributeType = "ATTR_SCALE_SCORE"

	// ATTR_PROFILE_SIMILARITY emits each row's similarity to a
	// reference across the p members of Vector / Fields: params.metric
	// (a similarity-direction metric — correlation, cosine, jaccard,
	// dice, overlap; default from the vector's kind), params.reference
	// (a literal array in member order, a label map, {table, key} or
	// {table, key_field} over Extensions.ReferenceVectors, matched by
	// label) and params.min_valid (fewest complete pairs, default 1).
	// key_field is a categorical label or an unsigned integer's base-10
	// value. A row whose key_field is null or has no profile is null,
	// counted (n_reference_missing) and warned
	// (PULSE_VECTOR_REFERENCE_MISSING); a domain violation likewise
	// (PULSE_VECTOR_METRIC_DOMAIN). Row-local: single pass, streamable; the
	// weight is irrelevant. A distance metric is refused with a pointer
	// to ATTR_VEC_DISTANCE.
	ATTR_PROFILE_SIMILARITY AttributeType = "ATTR_PROFILE_SIMILARITY"

	// ATTR_VEC_DISTANCE emits each row's distance to a reference across
	// the p members of Vector / Fields: ATTR_PROFILE_SIMILARITY's params
	// with a distance-direction metric (the row-local ones, plus the
	// fitted std_euclidean and mahalanobis, whose per-member SDs /
	// covariance come from ATTR_MAHALANOBIS's fit pass — two-pass,
	// params.fit_on, weighted when a weight is in force). A similarity
	// metric is refused with a pointer to ATTR_PROFILE_SIMILARITY.
	ATTR_VEC_DISTANCE AttributeType = "ATTR_VEC_DISTANCE"

	// ATTR_PREVIOUS_STATE emits, per record of a long panel, the state
	// its subject held at the subject's previous time point: Field (a
	// categorical_* or packed_bool state) read on the subject's
	// (SubjectField) record ordered just before this one by OrderField
	// (a date or datetime, ordered by instant — never by local time).
	// The output is the earlier record's state code (its dictionary
	// index, or 0 / 1), null on the subject's first time point, on a
	// row whose subject or order value is null (such a row is never a
	// predecessor either), and when the previous state is null (no
	// carry-forward). Rows tied on (subject, order) each take the state
	// at the last earlier distinct time point; the time point after a
	// tie reads the tied state when every tied state agrees, else null,
	// counted and warned (PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE; an error
	// under Strict). Buffered only: it reads every record of a subject.
	ATTR_PREVIOUS_STATE AttributeType = "ATTR_PREVIOUS_STATE"
)

// AllAttributeTypes returns all defined attribute types.
func AllAttributeTypes() []AttributeType {
	return []AttributeType{
		ATTR_CODE_IN,
		ATTR_DATE_PART,
		ATTR_FACTOR_SCORE,
		ATTR_FORMULA,
		ATTR_MAHALANOBIS,
		ATTR_NORMALIZED,
		ATTR_PC_SCORE,
		ATTR_PERCENTILE,
		ATTR_PREVIOUS_STATE,
		ATTR_PROFILE_SIMILARITY,
		ATTR_REG_FITTED,
		ATTR_REG_LEVERAGE,
		ATTR_REG_PREDICT,
		ATTR_REG_RESIDUAL,
		ATTR_SCALE_SCORE,
		ATTR_SEM_SCORE,
		ATTR_SET_HAS,
		ATTR_SET_POPCOUNT,
		ATTR_TSCORE,
		ATTR_VEC_DISTANCE,
		ATTR_ZSCORE,
	}
}

// WindowType identifies a specific window operation.
type WindowType string

const (
	WIN_LAG             WindowType = "WIN_LAG"
	WIN_LEAD            WindowType = "WIN_LEAD"
	WIN_ROW_NUMBER      WindowType = "WIN_ROW_NUMBER"
	WIN_RANK            WindowType = "WIN_RANK"
	WIN_DENSE_RANK      WindowType = "WIN_DENSE_RANK"
	WIN_RUNNING_SUM     WindowType = "WIN_RUNNING_SUM"
	WIN_RUNNING_AVG     WindowType = "WIN_RUNNING_AVG"
	WIN_MOVING_AVG      WindowType = "WIN_MOVING_AVG"
	WIN_EWMA            WindowType = "WIN_EWMA"
	WIN_PCT_CHANGE      WindowType = "WIN_PCT_CHANGE"
	WIN_DELTA           WindowType = "WIN_DELTA"
	WIN_SHARE           WindowType = "WIN_SHARE"
	WIN_LIFT            WindowType = "WIN_LIFT"
	WIN_DISTRIBUTED_LAG WindowType = "WIN_DISTRIBUTED_LAG"
)

// AllWindowTypes returns all defined window types in alphabetical order.
func AllWindowTypes() []WindowType {
	return []WindowType{
		WIN_DELTA,
		WIN_DENSE_RANK,
		WIN_DISTRIBUTED_LAG,
		WIN_EWMA,
		WIN_LAG,
		WIN_LEAD,
		WIN_LIFT,
		WIN_MOVING_AVG,
		WIN_PCT_CHANGE,
		WIN_RANK,
		WIN_ROW_NUMBER,
		WIN_RUNNING_AVG,
		WIN_RUNNING_SUM,
		WIN_SHARE,
	}
}

// FeatureType identifies a specific ML feature engineering operator.
// Features run pre-filter and may emit one or more output columns.
type FeatureType string

const (
	FEAT_LOG              FeatureType = "FEAT_LOG"
	FEAT_SQRT             FeatureType = "FEAT_SQRT"
	FEAT_BUCKETIZE        FeatureType = "FEAT_BUCKETIZE"
	FEAT_ONE_HOT          FeatureType = "FEAT_ONE_HOT"
	FEAT_DATE_FEATURES    FeatureType = "FEAT_DATE_FEATURES"
	FEAT_FREQUENCY_ENCODE FeatureType = "FEAT_FREQUENCY_ENCODE"
	FEAT_TARGET_ENCODE    FeatureType = "FEAT_TARGET_ENCODE"
	FEAT_TRAIN_TEST_SPLIT FeatureType = "FEAT_TRAIN_TEST_SPLIT"
	FEAT_POLY             FeatureType = "FEAT_POLY"
)

// AllFeatureTypes returns every defined feature type in alphabetical order.
func AllFeatureTypes() []FeatureType {
	return []FeatureType{
		FEAT_BUCKETIZE,
		FEAT_DATE_FEATURES,
		FEAT_FREQUENCY_ENCODE,
		FEAT_LOG,
		FEAT_ONE_HOT,
		FEAT_POLY,
		FEAT_SQRT,
		FEAT_TARGET_ENCODE,
		FEAT_TRAIN_TEST_SPLIT,
	}
}

// TestType identifies a specific statistical test operation.
//
// Pulse splits statistical testing into two tiers, both expressed via this
// enum and both executed inside a single Process pipeline:
//
//   - Tier 1 (row tests): listed in Request.Tests, evaluated against the
//     raw row stream alongside aggregators. Online-moments tests reuse the
//     running mean/variance/n that aggregators already compute, so they add
//     near-zero cost when their inputs overlap with active aggregations.
//   - Tier 2 (post tests): listed in Request.PostTests, evaluated after the
//     window stage on the materialized result row set. Useful for ANOVA
//     across grouper buckets, post-hoc pairwise comparisons, and trend
//     tests over windowed series.
//
// Per-type semantics, required fields, and streamability are documented in
// skills/statistical-testing.md.
type TestType string

const (
	// TEST_T is a one-sample or two-sample Welch t-test on a numeric field.
	// When SplitBy is set, the test partitions Field by SplitBy and compares
	// the two resulting groups; otherwise it tests Field's mean against the
	// hypothesized value supplied in Params.
	TEST_T TestType = "TEST_T"

	// TEST_WELCH is an explicit two-sample Welch t-test alias used when the
	// caller wants to be unambiguous about the variant. Behaves identically
	// to TEST_T with SplitBy set; provided so requests document intent.
	TEST_WELCH TestType = "TEST_WELCH"

	// TEST_CHISQ is a chi-square independence test on a 2D contingency
	// table built from two categorical fields (Rows × Cols).
	TEST_CHISQ TestType = "TEST_CHISQ"

	// TEST_ANOVA_F is a one-way analysis of variance F-test comparing the
	// means of a numeric Field across k groups defined by a categorical
	// SplitBy field. k must be ≥ 2.
	TEST_ANOVA_F TestType = "TEST_ANOVA_F"

	// TEST_KS is a Kolmogorov-Smirnov two-sample distribution test. Not
	// streamable — requires sorted ECDFs.
	TEST_KS TestType = "TEST_KS"

	// TEST_TUKEY_HSD is a post-hoc pairwise comparison of group means using
	// Tukey's Honestly Significant Difference. Tier-2 only: consumes the
	// per-group means and counts produced by upstream aggregators.
	TEST_TUKEY_HSD TestType = "TEST_TUKEY_HSD"

	// TEST_TREND is a Mann-Kendall trend test over an ordered numeric
	// series. Tier-2 typical: runs over windowed result rows; tier-1
	// possible when the raw field is naturally ordered by an OrderBy key.
	TEST_TREND TestType = "TEST_TREND"

	// TEST_PEARSON_R is the parametric correlation test between two
	// numeric fields. Streams via an extended Welford recurrence that
	// also tracks the running cross-product. p-value via the t-statistic
	// r·√((n−2)/(1−r²)) with df = n − 2.
	TEST_PEARSON_R TestType = "TEST_PEARSON_R"

	// TEST_PAIRED_T is the paired-sample t-test on the per-row difference
	// d = Field − Field2. Streams via the same Welford machinery as the
	// one-sample TEST_T, driven by two-field reads.
	TEST_PAIRED_T TestType = "TEST_PAIRED_T"

	// TEST_PROP_Z is the two-proportion z-test on a categorical SplitBy
	// field where each row counts as a success or failure based on
	// Params.success (the dictionary value treated as a "success" on
	// Field). Streams via per-group success / total counts.
	TEST_PROP_Z TestType = "TEST_PROP_Z"

	// TEST_MANN_WHITNEY_U is the nonparametric two-sample alternative to
	// TEST_T. Buffered: ranks the combined value set under tie correction,
	// sums the ranks in group A → R_A, then computes
	//   U_A = R_A − n_A(n_A+1)/2
	// Two-sided p-value via the normal approximation with tie correction.
	TEST_MANN_WHITNEY_U TestType = "TEST_MANN_WHITNEY_U"

	// TEST_WILCOXON_SR is the Wilcoxon signed-rank test on the per-row
	// difference d = Field − Field2. Buffered: drops zero-diff pairs,
	// ranks |d| under tie correction, sums positive-sign ranks → W.
	// Two-sided p-value via the normal approximation with tie correction.
	TEST_WILCOXON_SR TestType = "TEST_WILCOXON_SR"

	// TEST_KRUSKAL_WALLIS is the nonparametric k-group alternative to
	// TEST_ANOVA_F. Buffered: ranks the combined value set under tie
	// correction, sums ranks per group, then
	//   H = (12/(N(N+1))) · Σ (R_i²/n_i) − 3(N+1)
	// p-value via chi-square survival with k−1 df.
	TEST_KRUSKAL_WALLIS TestType = "TEST_KRUSKAL_WALLIS"

	// TEST_SPEARMAN_R is the rank-based correlation between Field and
	// Field2 (monotonic association). Buffered: mid-ranks each column
	// under tie correction, then runs Pearson on the ranks. p-value via
	//   t = ρ · √((n−2)/(1−ρ²))   df = n − 2
	TEST_SPEARMAN_R TestType = "TEST_SPEARMAN_R"

	// TEST_KENDALL_TAU is concordance-based correlation between Field
	// and Field2. Buffered O(n²) pair count under tie correction. Two-
	// sided p-value via normal approximation with the standard variance
	// adjustment for ties in either column.
	TEST_KENDALL_TAU TestType = "TEST_KENDALL_TAU"

	// TEST_ANOVA_WELCH is the heteroscedasticity-robust one-way ANOVA.
	// Streams via the same per-group Welford buckets as TEST_ANOVA_F.
	// Statistic uses per-group weights w_i = n_i / s²_i, with the
	// Welch-Satterthwaite denominator correction for unequal variances.
	TEST_ANOVA_WELCH TestType = "TEST_ANOVA_WELCH"

	// TEST_ANOVA_RM is the repeated-measures one-way ANOVA. Buffered:
	// requires SubjectField to pivot rows into the wide subject ×
	// condition table. Decomposes SS into between-subjects, treatment,
	// and error components; F = MS_treatment / MS_error.
	TEST_ANOVA_RM TestType = "TEST_ANOVA_RM"

	// TEST_BROWN_FORSYTHE is a homogeneity-of-variance test. Replaces
	// each value with its absolute deviation from the group median, then
	// runs one-way ANOVA on the deviations. Buffered: per-group medians
	// require a sort.
	TEST_BROWN_FORSYTHE TestType = "TEST_BROWN_FORSYTHE"

	// TEST_FISHER_EXACT is the exact two-sided p-value for a 2×2
	// contingency table. The canonical small-sample alternative to
	// TEST_CHISQ when any expected cell count is below 5. Buffered:
	// requires the full contingency table.
	TEST_FISHER_EXACT TestType = "TEST_FISHER_EXACT"

	// TEST_HOTELLING_T2 is Hotelling's T² test on a mean VECTOR: the
	// members of Vector / Fields (p numeric columns, listwise). With no
	// SplitBy it is the one-sample test of the mean vector against
	// params.mu (length p, default zeros), F = (n−p)/(p(n−1))·T² on
	// (p, n−p); with a SplitBy of exactly two levels it is the
	// two-sample test of equal mean vectors under a pooled covariance,
	// exact F on (p, n₁+n₂−p−1). Streams via one co-moment accumulator
	// per level.
	TEST_HOTELLING_T2 TestType = "TEST_HOTELLING_T2"

	// TEST_MANOVA is the one-way MANOVA of a mean VECTOR (the members
	// of Vector / Fields, listwise) across the levels of a categorical
	// SplitBy (≥ 2 levels): within SSCP E = Σ per-level co-moments,
	// between SSCP H from the level means, and the four statistics on
	// the eigenvalues of E⁻¹H. Statistic / DF / PValue are Pillai's
	// trace; Details carry Pillai, Wilks (Rao F), Hotelling–Lawley and
	// Roy (an upper bound), each with F, df1, df2 and p. Streams via one
	// co-moment accumulator per level.
	TEST_MANOVA TestType = "TEST_MANOVA"

	// TEST_BARTLETT_SPHERICITY is Bartlett's test that the correlation
	// matrix of a VECTOR (the members of Vector / Fields, listwise) is
	// the identity — whether the members are correlated enough to
	// factor. χ² = −ln|R|·(N* − 1 − (2p + 5)/6) on p(p − 1)/2 df, the
	// shared core MAT_PCA's bartlett_* scalars read, so the two agree
	// bit for bit on the same rows. No SplitBy. Streams via one
	// co-moment accumulator.
	TEST_BARTLETT_SPHERICITY TestType = "TEST_BARTLETT_SPHERICITY"

	// TEST_BOX_M is Box's M test that a VECTOR's covariance matrix (the
	// members of Vector / Fields, listwise) is equal across the levels
	// of a categorical SplitBy (≥ 2 levels) — the MANOVA / Hotelling
	// equal-covariance assumption check. Statistic / DF / PValue are
	// Box's χ² approximation; Details carry M and the F approximation.
	// Streams via one co-moment accumulator per level.
	TEST_BOX_M TestType = "TEST_BOX_M"

	// TEST_MARDIA is Mardia's test of multivariate normality on a
	// VECTOR (the members of Vector / Fields, listwise): multivariate
	// skewness b1p as a χ² on p(p + 1)(p + 2)/6 df (small-sample
	// corrected when n < 20) and kurtosis b2p as a z. Params.statistic
	// picks the headline ("skewness" default, or "kurtosis"); both ride
	// Details. No SplitBy. Buffered: every row's Mahalanobis product
	// needs the fitted mean and covariance first.
	TEST_MARDIA TestType = "TEST_MARDIA"

	// TEST_SHAPIRO_WILK is the normality test on Field, computed in the
	// Shapiro-Francia (W′) form. When SplitBy is set the test runs
	// per-group; otherwise it runs over the full filtered set. Buffered:
	// requires the ordered values. The p-value is calibrated for
	// 5 ≤ n ≤ 5000; outside that range the result still carries a
	// p-value plus an advisory warning.
	TEST_SHAPIRO_WILK TestType = "TEST_SHAPIRO_WILK"

	// TEST_Z_TWO_SAMPLE is the two-sample z-test on the means of a
	// numeric Field across two groups defined by a categorical SplitBy.
	// Mathematically identical to TEST_WELCH for the test statistic and
	// standard error, but the p-value is computed from the standard
	// normal CDF Φ rather than the Student-t CDF T_df. The variant
	// matches large-sample survey conventions (e.g. weighted-proportion
	// tests already computed as means with an n denominator) where the
	// Student-t correction is not desired. For small n the divergence
	// from TEST_WELCH is non-trivial; predict surfaces no warning, so
	// callers choose intentionally. Streams via the same per-group
	// Welford buckets as TEST_T / TEST_WELCH.
	TEST_Z_TWO_SAMPLE TestType = "TEST_Z_TWO_SAMPLE"
)

// AllTestTypes returns every defined statistical test type in alphabetical
// order.
func AllTestTypes() []TestType {
	return []TestType{
		TEST_ANOVA_F,
		TEST_ANOVA_RM,
		TEST_ANOVA_WELCH,
		TEST_BARTLETT_SPHERICITY,
		TEST_BOX_M,
		TEST_BROWN_FORSYTHE,
		TEST_CHISQ,
		TEST_FISHER_EXACT,
		TEST_HOTELLING_T2,
		TEST_KENDALL_TAU,
		TEST_KRUSKAL_WALLIS,
		TEST_KS,
		TEST_MANN_WHITNEY_U,
		TEST_MANOVA,
		TEST_MARDIA,
		TEST_PAIRED_T,
		TEST_PEARSON_R,
		TEST_PROP_Z,
		TEST_SHAPIRO_WILK,
		TEST_SPEARMAN_R,
		TEST_T,
		TEST_TREND,
		TEST_TUKEY_HSD,
		TEST_WELCH,
		TEST_WILCOXON_SR,
		TEST_Z_TWO_SAMPLE,
	}
}

// Test defines a single statistical test to evaluate during a Process run.
// Tests appear in two request slots: Request.Tests (tier 1, row-level) and
// Request.PostTests (tier 2, result-level). The shape is shared because
// most fields are common across tiers; per-test validation rules live in
// the registry and predict layer.
type Test struct {
	// Type is the statistical test to perform.
	Type TestType `json:"type"`

	// Field is the primary numeric field under test. Required for TEST_T,
	// TEST_WELCH, TEST_ANOVA_F, TEST_KS, and TEST_TREND. Omitted for
	// TEST_CHISQ (uses Rows × Cols instead).
	Field string `json:"field,omitempty"`

	// Field2 is the secondary numeric field reference used by paired or
	// bivariate tests (TEST_PEARSON_R, TEST_PAIRED_T). For TEST_PAIRED_T
	// the difference d = Field − Field2 is tested. For TEST_PEARSON_R
	// the correlation between Field and Field2 is reported. Empty for
	// every other test.
	Field2 string `json:"field2,omitempty"`

	// Vector names the Request.Vectors entry whose members a
	// vector-input test (TEST_HOTELLING_T2, TEST_MANOVA,
	// TEST_BARTLETT_SPHERICITY, TEST_BOX_M, TEST_MARDIA) reads as its p numeric
	// columns. Exactly one of Vector and Fields is set on a vector-input
	// test and neither on any other test (SERVICE_VALIDATION); a Vector
	// no Request.Vectors entry defines is PULSE_VECTOR_UNKNOWN. Members
	// resolve by the VectorSpec rules, the MatrixSpec.Vector / Fields
	// mirror, and missing data is listwise only (params.missing
	// "pairwise" is refused).
	Vector string `json:"vector,omitempty"`

	// Fields lists a vector-input test's inline members (literal names
	// and / or glob entries, the VectorSpec.Fields rules). See Vector.
	Fields []string `json:"fields,omitempty"`

	// Vector2 names the Request.Vectors entry of a vector-input test's
	// SECOND member set (the paired form); exactly one of Vector2 and
	// Fields2 at most, only beside Vector / Fields, and resolving to
	// the same member count p. A test type that reads no second set
	// refuses it (SERVICE_VALIDATION).
	Vector2 string `json:"vector2,omitempty"`

	// Fields2 lists the second member set inline. See Vector2.
	Fields2 []string `json:"fields2,omitempty"`

	// SplitBy is a categorical field whose distinct values partition Field
	// into groups. Required for two-sample TEST_T / TEST_WELCH and for
	// TEST_ANOVA_F. Two-sample tests expect exactly 2 groups; ANOVA expects
	// ≥ 2.
	SplitBy string `json:"split_by,omitempty"`

	// Rows is the row-axis categorical field for TEST_CHISQ contingency.
	Rows string `json:"rows,omitempty"`

	// Cols is the column-axis categorical field for TEST_CHISQ contingency.
	Cols string `json:"cols,omitempty"`

	// SubjectField identifies the within-subject grouping for
	// repeated-measures designs (TEST_ANOVA_RM). Each distinct value
	// represents one subject contributing one observation per condition
	// (SplitBy). Empty for every other test.
	SubjectField string `json:"subject_field,omitempty"`

	// Alpha is the significance level. Defaults to 0.05 when zero.
	// Must lie in the open interval (0, 1).
	Alpha float64 `json:"alpha,omitempty"`

	// Label is the output alias for the test result. When empty, defaults
	// to "<TYPE>" or "<TYPE>_<field>" depending on the operator.
	Label string `json:"label,omitempty"`

	// OrderBy supplies ordering keys for tests that need a sorted series
	// (TEST_TREND, TEST_KS when run as a series test). Tier-2 trend tests
	// typically reference an output column produced by an upstream window
	// or grouper.
	OrderBy []OrderKey `json:"order_by,omitempty"`

	// Params holds operator-specific configuration as raw JSON.
	//   TEST_T (one-sample): {"mu": <hypothesized mean>}
	//   TEST_T / TEST_WELCH (two-sample): {"variant": "welch"} (default)
	//   TEST_TUKEY_HSD: {"ms_within": <f64>, "df_within": <f64>}
	//   TEST_TREND: {"variant": "mann_kendall"} (default)
	//   TEST_HOTELLING_T2 (one-sample): {"mu": [<p numbers>]} (default zeros)
	//   TEST_MARDIA: {"statistic": "skewness" | "kurtosis"} (default skewness)
	//   vector-input tests: {"missing": "listwise"} (the only mode)
	Params json.RawMessage `json:"params,omitempty"`

	// Weight is the per-slot weight override: absent inherits
	// Request.Weight, then pulse.Options.DefaultWeight; `null` opts the
	// slot out (it runs unweighted); a field-name string or a
	// {field, kind} object sets the slot's own weight. See SlotWeight.
	Weight SlotWeight `json:"weight,omitzero"`

	// Multiplicity is the test's own multiple-comparison correction
	// block: its method and family override the request's, then
	// pulse.Options.DefaultMultiplicity; a test's family is `request`
	// (default) or `compose`. Alpha is refused here — the adjusted flag
	// reads the test's own Alpha. Nil inherits. See Multiplicity.
	Multiplicity *Multiplicity `json:"multiplicity,omitempty"`
}

// TestResult is the per-test outcome embedded in Response.Tests and
// Response.PostTests. Common statistics live as top-level fields; operator-
// specific payload (contingency tables, pairwise comparisons, effect sizes,
// per-group moments) lives in Details so the result shape stays predictable
// across the catalog.
type TestResult struct {
	// Label echoes the request Label or the operator's synthesized default.
	Label string `json:"label,omitempty"`

	// Type is the test that produced this result.
	Type TestType `json:"type"`

	// GroupKey identifies the group bucket a tier-1 test ran over when
	// the request is grouped: one result per (test x composite group
	// key), test-major, groups in the final Response.Data row order —
	// the per-grouper tuple of the bucket's Data row (one element for a
	// single grouper), the MatrixResult.GroupKey shape. On a post-test
	// over two or more groupers it is the PARTITION key — the leading
	// partition_by key columns' values (every key but the last by
	// default) — one result per (post-test x partition), test-major,
	// partitions in first-appearance Data order. Omitted on an ungrouped
	// (or unpartitioned) request, which stays byte-identical.
	GroupKey AxisKey `json:"group_key,omitempty"`

	// Variant names the specific algorithm when a test supports more than
	// one (e.g. "welch_two_sample", "mann_kendall", "independence").
	Variant string `json:"variant,omitempty"`

	// Statistic is the test statistic (t, F, χ², KS D, Mann-Kendall S, etc.).
	Statistic float64 `json:"statistic"`

	// DF is the degrees of freedom. Tests with multiple DF values (e.g.
	// ANOVA's between/within) report the numerator DF here and surface the
	// remainder in Details.
	DF float64 `json:"df,omitempty"`

	// PValue is the two-sided p-value under the null hypothesis.
	PValue float64 `json:"p_value"`

	// Alpha is the significance threshold applied to RejectNull.
	Alpha float64 `json:"alpha"`

	// RejectNull is true when PValue < Alpha.
	RejectNull bool `json:"reject_null"`

	// PAdjusted is PValue after the multiple-comparison correction of
	// the test's family (see Multiplicity); nil when no correction ran
	// over this test. Points at NaN (JSON null) when PValue is
	// undefined — such a p is excluded from the family size. PValue
	// and RejectNull are never modified.
	PAdjusted *float64 `json:"p_adjusted,omitempty"`

	// SignificantAdjusted is PAdjusted < Alpha (the test's own alpha);
	// nil when no correction ran or PAdjusted is undefined.
	SignificantAdjusted *bool `json:"significant_adjusted,omitempty"`

	// Multiplicity echoes the correction that produced PAdjusted:
	// method, family, the alpha SignificantAdjusted reads (the test's
	// own Alpha) and the family size m. Nil when no correction ran.
	Multiplicity *AppliedMultiplicity `json:"multiplicity,omitempty"`

	// Details holds operator-specific payload (per-group n/mean/variance,
	// contingency table, pairwise comparisons, confidence intervals,
	// effect-size measures). Marshals naturally as a nested JSON object.
	Details map[string]any `json:"details,omitempty"`

	// Warnings carries per-test diagnostics (low N, near-zero variance,
	// expected-count thresholds, etc.) so the envelope-level Warnings list
	// is not the only signal.
	Warnings []string `json:"warnings,omitempty"`

	// Error is the data-dependent refusal that failed this test slot
	// alone (PULSE_TEST_VARIANCE_ZERO, _INSUFFICIENT_N, …) while the rest
	// of the response survived; see SlotError. A result carrying one has
	// Label / Type / GroupKey and the error only: the statistic, p-value
	// and every other figure are absent on the wire (in Go, Statistic and
	// PValue are NaN). A grouped run's one sparse GROUP keeps its
	// null-statistics result and per-group warning instead.
	Error *SlotError `json:"error,omitempty"`
}

// Feature defines a feature engineering operation. Features run pre-filter
// (before any FILTER_* predicate) and may produce one or more derived
// columns. Global-pass features (TARGET_ENCODE, FREQUENCY_ENCODE) require a
// stats sweep before per-row write; per-row features compute one row at a
// time.
type Feature struct {
	// Type is the feature operator to perform.
	Type FeatureType `json:"type"`

	// Field is the source field name. Required by every operator except
	// FEAT_TRAIN_TEST_SPLIT (which reads no field by default — params may
	// optionally name a stratify field).
	Field string `json:"field,omitempty"`

	// Label is an output column name (single-output operators) or output
	// column prefix (multi-output operators). When empty, the operator
	// derives a default — typically "<TYPE>_<field>".
	Label string `json:"label,omitempty"`

	// Params holds operator-specific parameters as raw JSON. See the
	// feature-engineering skill for the per-operator schema.
	Params json.RawMessage `json:"params,omitempty"`

	// TimeZone is the per-slot IANA zone override (`"UTC"` or an
	// `Area/Location` name). Empty inherits Request.TimeZone, then
	// pulse.Options.DefaultTimeZone, then UTC. Accepted only on
	// zone-capable operators (see the manifest operator `zone` key).
	TimeZone string `json:"tz,omitempty"`

	// TimeZoneField names a categorical cohort field whose dictionary
	// entries are IANA zone names: each row is read in its own zone (a
	// per-row zone). Precedence: slot `tz` → slot `tz_field` →
	// Request.TimeZoneField → Request.TimeZone →
	// pulse.Options.DefaultTimeZone → UTC. Setting both `tz` and
	// `tz_field` on one slot is refused; accepted only where `tz` is
	// (zone-capable operators over a `datetime` field).
	TimeZoneField string `json:"tz_field,omitempty"`

	// ForEach, when set, fans this item out over parameter axes before
	// validation (see ForEach). Gated by capability:for_each.
	ForEach *ForEach `json:"for_each,omitempty"`
}

// OrderKey specifies an ordering key for a window's ORDER BY clause.
type OrderKey struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// FrameSpec specifies the window frame bounds.
// Mode is "rows" — only frame mode supported in v1.
// Preceding nil means UNBOUNDED PRECEDING; Following nil means UNBOUNDED FOLLOWING.
// Following==0 with Preceding==0 selects the current row only.
type FrameSpec struct {
	Mode      string `json:"mode"`
	Preceding *int   `json:"preceding,omitempty"`
	Following *int   `json:"following,omitempty"`
}

// Window defines a window operation.
type Window struct {
	// Type is the window operation to perform.
	Type WindowType `json:"type"`

	// Field is the source field name. Required for all operators except
	// ROW_NUMBER, RANK, and DENSE_RANK.
	Field string `json:"field,omitempty"`

	// Label is the output column name. When empty, defaults to "<TYPE>_<field>"
	// or "<TYPE>" for ROW_NUMBER/RANK/DENSE_RANK.
	Label string `json:"label,omitempty"`

	// PartitionBy is the list of fields whose distinct values define
	// independent partitions for window evaluation. Empty means a single
	// partition over all rows.
	PartitionBy []string `json:"partition_by,omitempty"`

	// OrderBy is the list of order keys. Required (≥1) for every window operator.
	OrderBy []OrderKey `json:"order_by"`

	// Frame is the window frame specification. Required for RUNNING_*, MOVING_AVG,
	// and EWMA. Rejected for LAG, LEAD, ROW_NUMBER, RANK, DENSE_RANK, PCT_CHANGE.
	Frame *FrameSpec `json:"frame,omitempty"`

	// Params holds operator-specific parameters as raw JSON.
	// LAG/LEAD: {"offset": 1, "default": null}
	// EWMA: {"alpha": 0.5}
	// PCT_CHANGE: {"periods": 1}
	Params json.RawMessage `json:"params,omitempty"`

	// ForEach, when set, fans this item out over parameter axes before
	// validation (see ForEach). Gated by capability:for_each.
	ForEach *ForEach `json:"for_each,omitempty"`
}

// Aggregation defines a single aggregation operation to apply to a field.
type Aggregation struct {
	// Type is the aggregation operation to perform.
	Type AggregationType `json:"type"`

	// Field is the name of the data field to aggregate. Empty on a
	// vector aggregator (AGG_VEC_*), which reads Vector or Fields.
	Field string `json:"field"`

	// Vector names the Request.Vectors entry whose members a vector
	// aggregator (AGG_VEC_MEAN / AGG_VEC_SUM / AGG_VEC_SD) aggregates element by
	// element; exactly one of Vector and Fields. Any other aggregator
	// refuses it.
	Vector string `json:"vector,omitempty"`

	// Fields lists a vector aggregator's members inline (literal names
	// and / or globs, resolved by the Request.Vectors rules); exactly
	// one of Vector and Fields.
	Fields []string `json:"fields,omitempty"`

	// Label is an optional output label for the aggregation result.
	Label string `json:"label,omitempty"`

	// Params holds type-specific configuration as raw JSON.
	// Used by aggregation types that require additional parameters (e.g., AGG_PERCENTILE).
	Params json.RawMessage `json:"params,omitempty"`

	// Weight is the per-slot weight override: absent inherits
	// Request.Weight, then pulse.Options.DefaultWeight; `null` opts the
	// slot out (it runs unweighted); a field-name string or a
	// {field, kind} object sets the slot's own weight. See SlotWeight.
	Weight SlotWeight `json:"weight,omitzero"`
}

// Filterer defines a filter to apply to records before processing.
type Filterer struct {
	// Type is the filter operation to perform.
	Type FiltererType `json:"type"`

	// Field is the name of the data field to filter on.
	// Not required for FILTER_EXPRESSION.
	Field string `json:"field,omitempty"`

	// Values is a list of values for include/exclude/range filters.
	Values []string `json:"values,omitempty"`

	// Expression is a runtime expression for FILTER_EXPRESSION type.
	Expression string `json:"expression,omitempty"`

	// Params holds type-specific configuration as raw JSON. Used by
	// filterers whose configuration cannot ride Values []string — e.g.
	// FILTER_DATE_RANGES, which carries a structured inline `ranges`
	// array of {label, start, end} objects.
	Params json.RawMessage `json:"params,omitempty"`

	// TimeZone is the per-slot IANA zone override (`"UTC"` or an
	// `Area/Location` name). Empty inherits Request.TimeZone, then
	// pulse.Options.DefaultTimeZone, then UTC. Accepted only on
	// zone-capable operators (see the manifest operator `zone` key).
	TimeZone string `json:"tz,omitempty"`

	// TimeZoneField names a categorical cohort field whose dictionary
	// entries are IANA zone names: each row is read in its own zone (a
	// per-row zone). Precedence: slot `tz` → slot `tz_field` →
	// Request.TimeZoneField → Request.TimeZone →
	// pulse.Options.DefaultTimeZone → UTC. Setting both `tz` and
	// `tz_field` on one slot is refused; accepted only where `tz` is
	// (zone-capable operators over a `datetime` field).
	TimeZoneField string `json:"tz_field,omitempty"`
}

// Group defines a grouping operation to partition results.
type Group struct {
	// Type is the grouping operation to perform.
	Type GroupType `json:"type"`

	// Field is the name of the data field to group by. A vector-input
	// grouper (GROUP_KMEANS) reads Vector / Fields instead and refuses
	// Field; its key column is named "<TYPE>_<Vector>" (or
	// "<TYPE>_<first Fields entry as written>").
	Field string `json:"field"`

	// Vector names the Request.Vectors entry whose members a
	// vector-input grouper (GROUP_KMEANS) reads as its p numeric
	// columns. Exactly one of Vector and Fields is set on a vector-input
	// grouper and neither on any other grouper (SERVICE_VALIDATION); a
	// Vector no Request.Vectors entry defines is PULSE_VECTOR_UNKNOWN.
	// Members resolve by the VectorSpec rules (the MatrixSpec, Test and
	// Attribute Vector / Fields mirror), against source columns only;
	// a row with any null member is skipped (listwise).
	Vector string `json:"vector,omitempty"`

	// Fields lists a vector-input grouper's inline members (literal
	// names and / or glob entries, the VectorSpec.Fields rules). See
	// Vector.
	Fields []string `json:"fields,omitempty"`

	// Interval is used by GROUP_ROUNDED and GROUP_RANGE to define the bucket width.
	Interval float64 `json:"interval,omitempty"`

	// Params holds type-specific configuration as raw JSON.
	// Used by group types that require additional parameters (e.g., GROUP_DATE).
	Params json.RawMessage `json:"params,omitempty"`

	// Include optionally restricts the grouper to a fixed set of bucket
	// keys. Rows whose computed key (or, for per-element groupers, each
	// individual key produced by a single row) is not present in this
	// list are skipped — identical to the null-key handling path, no
	// bucket is created for the rejected key. Supported by:
	//
	//   - GROUP_CATEGORY        — matches the categorical label string.
	//   - GROUP_SET_VALUE       — matches the sorted, pipe-joined
	//                             composite bucket key (e.g. "MC|VISA").
	//   - GROUP_SET_PER_ELEMENT — matches each fan-out label
	//                             independently; the row contributes
	//                             only to surviving labels.
	//
	// A non-empty Include is order-significant: output buckets emit in
	// the order values are listed here — across both the plain grouped
	// payload (Response.Data rows and Response.Components buckets) and
	// each crosstab axis independently (an axis without Include keeps
	// its prior alphabetical / dict-index order). Include values that
	// match zero records are still dropped, never emitted as empty
	// buckets.
	//
	// Empty / nil Include means "no inclusion filter" and preserves the
	// prior alphabetical bucket order (dict-index order for
	// GROUP_SET_PER_ELEMENT) — byte-identical to the pre-Include output.
	// Other grouper types ignore Include — predict / runtime gates flag
	// misuse.
	Include []string `json:"include,omitempty"`

	// TimeZone is the per-slot IANA zone override (`"UTC"` or an
	// `Area/Location` name). Empty inherits Request.TimeZone, then
	// pulse.Options.DefaultTimeZone, then UTC. Accepted only on
	// zone-capable operators (see the manifest operator `zone` key).
	TimeZone string `json:"tz,omitempty"`

	// TimeZoneField names a categorical cohort field whose dictionary
	// entries are IANA zone names: each row is read in its own zone (a
	// per-row zone). Precedence: slot `tz` → slot `tz_field` →
	// Request.TimeZoneField → Request.TimeZone →
	// pulse.Options.DefaultTimeZone → UTC. Setting both `tz` and
	// `tz_field` on one slot is refused; accepted only where `tz` is
	// (zone-capable operators over a `datetime` field).
	TimeZoneField string `json:"tz_field,omitempty"`

	// Weight is the per-slot weight override, carried by a grouper
	// mainly to opt OUT: GROUP_QUANTILE's cutpoints have no weighted
	// form yet, so any weight in force refuses it unless the slot sets
	// `null` (it then cuts unweighted). Absent inherits Request.Weight,
	// then pulse.Options.DefaultWeight. GROUP_KMEANS reads a frequency
	// weight (each row is w coincident points; a probability weight is
	// refused). No other grouper reads a weight.
	// See SlotWeight.
	Weight SlotWeight `json:"weight,omitzero"`
}

// Attribute defines a derived attribute computation.
type Attribute struct {
	// Type is the attribute computation to perform.
	Type AttributeType `json:"type"`

	// Field is the name of the source data field. Optional for the
	// regression attribute family (ATTR_REG_FITTED / RESIDUAL / LEVERAGE)
	// which read Target + Predictors instead; when empty the output Label
	// defaults to "<TYPE>_<Target>". A vector-input attribute
	// (ATTR_MAHALANOBIS, ATTR_PC_SCORE, ATTR_FACTOR_SCORE) reads Vector /
	// Fields and refuses Field.
	Field string `json:"field"`

	// Vector names the Request.Vectors entry whose members a
	// vector-input attribute (ATTR_MAHALANOBIS, ATTR_PC_SCORE,
	// ATTR_FACTOR_SCORE) reads as its p numeric
	// columns. Exactly one of Vector and Fields is set on a vector-input
	// attribute and neither on any other attribute (SERVICE_VALIDATION);
	// a Vector no Request.Vectors entry defines is PULSE_VECTOR_UNKNOWN.
	// Members resolve by the VectorSpec rules (the MatrixSpec.Vector /
	// Fields and Test.Vector / Fields mirror), against source columns
	// only, and missing data is listwise only. The default Label is
	// "<TYPE>_<Vector>" (ATTR_PC_SCORE appends "_PC<component>",
	// ATTR_FACTOR_SCORE "_F<factor>").
	Vector string `json:"vector,omitempty"`

	// Fields lists a vector-input attribute's inline members (literal
	// names and / or glob entries, the VectorSpec.Fields rules). The
	// default Label is "<TYPE>_<first entry as written>". See Vector.
	Fields []string `json:"fields,omitempty"`

	// Label is the output name for the derived attribute.
	Label string `json:"label,omitempty"`

	// Expression is a runtime expression for ATTR_FORMULA type.
	Expression string `json:"expression,omitempty"`

	// Params holds type-specific configuration as raw JSON.
	// Each attribute type defines its own params schema. The fitted
	// vector attributes (ATTR_MAHALANOBIS, ATTR_PC_SCORE,
	// ATTR_FACTOR_SCORE) share two keys:
	//   fit_on: "filtered" (default: fit on every filtered row) or an
	//     inline array of Filterer objects selecting the subset of the
	//     filtered rows the fit folds; every filtered row is scored.
	//   missing: "listwise" (the only mode).
	// ATTR_MAHALANOBIS adds output: "d2" (default) or "p". ATTR_PC_SCORE
	// adds component (1-based, required), on ("correlation" default or
	// "covariance"), rotation ("none" default or MAT_PCA's orthogonal
	// criteria), epsilon (geomin_orthogonal) and components (the k
	// rotated within; required with a rotation). ATTR_FACTOR_SCORE adds
	// factor (1-based, required), method ("regression" default or
	// "bartlett") and MAT_FACTOR's factors, extraction, rotation, kappa,
	// gamma and epsilon.
	Params json.RawMessage `json:"params,omitempty"`

	// --- Regression-attribute fields (Option A: each ATTR_REG_* carries
	// its own RegressionSpec-shaped slice and refits internally during
	// the prepass). Ignored by every non-regression attribute type.

	// Target is the dependent variable for ATTR_REG_FITTED / RESIDUAL /
	// LEVERAGE. Required for those types.
	Target string `json:"target,omitempty"`

	// Predictors lists the independent variables for the regression-
	// attribute family. At least one predictor is required.
	Predictors []string `json:"predictors,omitempty"`

	// Penalty selects the OLS regularization scheme for ATTR_REG_FITTED /
	// ATTR_REG_RESIDUAL. One of "" (unpenalized), "l1" (lasso), "l2"
	// (ridge), or "elasticnet". ATTR_REG_LEVERAGE rejects any non-empty
	// Penalty (penalized leverage and GLM leverage are deferred to a
	// later phase).
	Penalty string `json:"penalty,omitempty"`

	// Alpha is the regularization strength for the regression-attribute
	// family when Penalty is non-empty. Must be > 0 in that case.
	Alpha float64 `json:"alpha,omitempty"`

	// L1Ratio is the elastic-net mixing parameter for the regression-
	// attribute family when Penalty == "elasticnet" (0 < L1Ratio < 1).
	L1Ratio float64 `json:"l1_ratio,omitempty"`

	// SubjectField names the cohort field whose value identifies a
	// record's subject for ATTR_PREVIOUS_STATE (any scalar type but a
	// set_* or a vector). Read only by ATTR_PREVIOUS_STATE; refused on
	// every other attribute type.
	SubjectField string `json:"subject_field,omitempty"`

	// OrderField names the date or datetime cohort field that orders a
	// subject's records for ATTR_PREVIOUS_STATE (by instant: epoch days
	// or epoch seconds). Read only by ATTR_PREVIOUS_STATE; refused on
	// every other attribute type.
	OrderField string `json:"order_field,omitempty"`

	// TimeZone is the per-slot IANA zone override (`"UTC"` or an
	// `Area/Location` name). Empty inherits Request.TimeZone, then
	// pulse.Options.DefaultTimeZone, then UTC. Accepted only on
	// zone-capable operators (see the manifest operator `zone` key).
	TimeZone string `json:"tz,omitempty"`

	// TimeZoneField names a categorical cohort field whose dictionary
	// entries are IANA zone names: each row is read in its own zone (a
	// per-row zone). Precedence: slot `tz` → slot `tz_field` →
	// Request.TimeZoneField → Request.TimeZone →
	// pulse.Options.DefaultTimeZone → UTC. Setting both `tz` and
	// `tz_field` on one slot is refused; accepted only where `tz` is
	// (zone-capable operators over a `datetime` field).
	TimeZoneField string `json:"tz_field,omitempty"`

	// Weight is the per-slot weight override: absent inherits
	// Request.Weight, then pulse.Options.DefaultWeight; `null` opts the
	// slot out (it runs unweighted); a field-name string or a
	// {field, kind} object sets the slot's own weight. See SlotWeight.
	Weight SlotWeight `json:"weight,omitzero"`

	// ForEach, when set, fans this item out over parameter axes before
	// validation (see ForEach). Gated by capability:for_each.
	ForEach *ForEach `json:"for_each,omitempty"`
}

// Output configures how processing results are formatted.
type Output struct {
	// Format is the output format (e.g., "json", "csv").
	Format string `json:"format"`

	// Filename is an optional output file path.
	Filename string `json:"filename,omitempty"`

	// Pretty enables indented/formatted output.
	Pretty bool `json:"pretty,omitempty"`

	// IncludeNil controls whether nil/null values appear in output.
	IncludeNil bool `json:"include_nil,omitempty"`
}

// Cohort identifies a .pulse data file for processing.
type Cohort struct {
	// Filename is the name of the .pulse file.
	Filename string `json:"filename"`

	// DataDir is the directory containing the cohort file.
	DataDir string `json:"data_dir,omitempty"`
}

// Request is the primary processing request type.
// It specifies a cohort, filters, aggregations, attributes, groups, and output config.
type Request struct {
	// Label is an optional caller-supplied alias used by Compose-only
	// overlay kinds to resolve sibling references across slots in a
	// ComposedRequest (e.g. ComposeOverlaySpec.Reference / Targets).
	// Empty is the zero value and the additive contract holds — the
	// `omitempty` tag keeps the JSON wire shape byte-identical to
	// pre-Label callers, including the CanonicalHash output (see
	// TestCanonicalHash_RequestLabelEmptyByteIdentical).
	//
	// Inside a Compose batch the engine synthesizes `request_<index+1>`
	// (1-based) for every slot that arrives with Label empty, before
	// dispatching the slot — the synthesis happens against an in-memory
	// clone so the caller's *Request pointer is not mutated. Two slots
	// resolving to the same final Label are rejected with
	// PULSE_COMPOSE_LABEL_COLLISION at the same validate hook.
	//
	// Outside Compose (a standalone pulse.Process call) Label is
	// retained verbatim but has no behavioural effect today; future
	// stories may light it up for downstream reference resolution.
	Label string `json:"label,omitempty"`

	// Cohort identifies the .pulse file to process.
	Cohort *Cohort `json:"cohort,omitempty"`

	// Filterers is the list of filters to apply before processing.
	Filterers []*Filterer `json:"filterers,omitempty"`

	// Aggregations is the list of aggregation operations.
	Aggregations []*Aggregation `json:"aggregations,omitempty"`

	// Attributes is the list of derived attribute computations.
	Attributes []*Attribute `json:"attributes,omitempty"`

	// Groups is the list of grouping operations.
	Groups []*Group `json:"groups,omitempty"`

	// Outputs configures result formatting.
	Outputs []*Output `json:"outputs,omitempty"`

	// Windows is the list of window operations evaluated after aggregation.
	Windows []*Window `json:"windows,omitempty"`

	// Features is the list of pre-filter feature engineering operators.
	// Each operator may add one or more derived columns to the working
	// schema before filters and downstream stages run.
	Features []*Feature `json:"features,omitempty"`

	// Sort orders response rows by the listed keys. Applied last in the
	// pipeline (after windows). Each key field must reference a schema
	// field, an aggregation/attribute/group/window output label, or any
	// column produced by upstream stages. Stable sort; nulls last
	// regardless of direction.
	Sort []OrderKey `json:"sort,omitempty"`

	// Tests is the list of tier-1 statistical tests evaluated alongside
	// aggregators against the raw row stream. Online-moments tests
	// (TEST_T, TEST_WELCH, TEST_CHISQ, TEST_ANOVA_F) execute in the
	// streaming Process path; sort-required tests (TEST_KS) force the
	// buffered path. Results land in Response.Tests in the same order.
	// On a grouped request every test runs once per composite group key
	// (buffered), so Response.Tests holds len(Tests) x groups entries,
	// test-major, each carrying its TestResult.GroupKey.
	Tests []*Test `json:"tests,omitempty"`

	// PostTests is the list of tier-2 statistical tests evaluated after
	// the window stage on the materialized result row set. Typical uses:
	// ANOVA across grouper buckets, Tukey HSD post-hoc on per-group
	// means, trend tests over windowed series. Always buffered (the
	// result row set must be materialized before tier-2 runs). Results
	// land in Response.PostTests in the same order.
	PostTests []*Test `json:"post_tests,omitempty"`

	// Regressions is the list of regression-modeling operators (REG_OLS,
	// REG_GLM, REG_BAYES_LINEAR) evaluated against the filtered record
	// set. Each spec produces one RegressionResult in Response.Regressions
	// in matching order. Streamability follows RegressionSpec.Streamable
	// — closed-form OLS/Bayes stream over sufficient statistics; GLM,
	// Resample, and Selection variants force the buffered path.
	Regressions []*RegressionSpec `json:"regressions,omitempty"`

	// Vectors names batteries of numeric columns once per request
	// (virtual vectors) for the matrix operators to reference by name.
	// Resolved against the schema before any record is read; predict
	// echoes the resolved member lists. A vector no operator references
	// is reported with a PULSE_VECTOR_UNREFERENCED warning. Gated by
	// capability:matrices. See VectorSpec.
	Vectors []VectorSpec `json:"vectors,omitempty"`

	// Matrices is the list of matrix operators (MAT_COVARIANCE,
	// MAT_CORRELATION)
	// evaluated against the filtered record set over a virtual vector
	// or an inline member list. Each spec produces one MatrixResult in
	// Response.Matrices, in matching order. Every built-in matrix
	// operator streams; its result is emitted at finalize. Gated by
	// capability:matrices. See MatrixSpec.
	Matrices []MatrixSpec `json:"matrices,omitempty"`

	// Models loads saved model files (a regression `save`) by name, into
	// the one bound-model namespace a Compose `fits` list also binds:
	// ATTR_REG_PREDICT and coef() resolve a loaded name exactly like a
	// fit of the same name. Every file is read, version-checked and
	// decoded before any record is read; a name already bound (another
	// entry, or a Compose fit) is PULSE_MODEL_NAME_COLLISION. Gated by
	// capability:models. See ModelRef.
	Models []ModelRef `json:"models,omitempty"`

	// Populations loads saved population files (a Compose vector fit's
	// `save`) by name, into the same bound-model namespace as vector
	// fits: a fitted attribute's or MAT_* slot's params.fit resolves a
	// loaded name exactly like a Compose vector fit of the same name.
	// Every file is read, version-checked and decoded before any record
	// is read; a name already bound (a `models` entry, another entry, or
	// a Compose fit) is PULSE_MODEL_NAME_COLLISION. Gated by
	// capability:models. See PopulationRef.
	Populations []PopulationRef `json:"populations,omitempty"`

	// Joins describes pushdown hash-join legs attached to the primary
	// cohort. v1 supports exactly one inner join per Request; multi-
	// join chains and the left/outer/anti kinds land in a follow-up.
	// When set, the orchestrator opens each Right cohort, builds a
	// hash table over the smaller side's join keys, and streams the
	// larger side as the probe — joined records flow through the
	// standard filter / attribute / group / aggregator pipeline.
	Joins []*JoinSpec `json:"joins,omitempty"`

	// Labels rewrites or augments categorical output values using
	// embedder-registered label tables. Each binding pairs a
	// categorical field with a label-table name and a mode (replace
	// or augment). Labels are display-only: filters, formulas, sort
	// keys, and group keys still see raw values. Tables are sourced
	// from pulse.Options.Extensions.LabelTables.
	Labels []*LabelBinding `json:"labels,omitempty"`

	// Crosstab is an optional cross-tabulation directive. When set,
	// the engine composes the cell aggregation across the row × column
	// grouper grid, computes the requested margins via sibling Compose
	// requests, applies the configured normalization, and returns the
	// result either as a structured matrix on Response.Crosstab
	// (shape=matrix, default) or as long-form rows on Response.Data
	// (shape=long). Mutually exclusive with top-level Groups /
	// Aggregations — see PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS.
	Crosstab *CrosstabSpec `json:"crosstab,omitempty"`

	// Overlays is the list of overlay-layer specifications that
	// decorate the primary result with derived projections — index-
	// vs-margin scores, sibling comparisons, baseline lifts, etc.
	// Each spec produces one OverlayLayer in Response.Overlays in
	// matching order. Per-kind semantics, required Ref fields, and
	// scope compatibility live in the overlay catalog (see
	// types/overlay.go).
	Overlays []OverlaySpec `json:"overlays,omitempty"`

	// DisableComponents overrides the engine-level
	// pulse.Options.DisableComponents setting for this single request.
	// nil (the default) inherits the engine setting. Explicit `true`
	// suppresses Response.Components regardless of the engine default;
	// explicit `false` forces components on regardless of the engine
	// default. Use a pointer so the unset state is distinguishable from
	// `false` — the standard tri-state override pattern.
	//
	// When the resolved decision is "disabled", Response.Components stays
	// nil and the wire form is byte-identical to the pre-Components
	// baseline; format_version is not bumped. The gate sits at the
	// processor's attach helpers, so the MetaAggregator.Components and
	// MetaGrouper.Components construction work is skipped — not
	// built-then-discarded.
	DisableComponents *bool `json:"disable_components,omitempty"`

	// TimeZone is the request-level IANA zone (`"UTC"` or an
	// `Area/Location` name) inherited by every zone-capable slot that
	// sets no `tz` of its own. Empty inherits
	// pulse.Options.DefaultTimeZone, then UTC.
	TimeZone string `json:"time_zone,omitempty"`

	// TimeZoneField is the request-level per-row zone: a categorical
	// cohort field of IANA zone names inherited by every zone-capable
	// slot that sets neither `tz` nor `tz_field` of its own. It takes
	// precedence over TimeZone for those slots; expressions (to_day)
	// read TimeZone only.
	TimeZoneField string `json:"time_zone_field,omitempty"`

	// Weight is the request-level row weight inherited by every slot
	// that sets no `weight` of its own (a slot's `null` opts it out).
	// Nil inherits pulse.Options.DefaultWeight; with neither, the
	// request runs unweighted. See .claude/reference/weighting.md.
	Weight *WeightSpec `json:"weight,omitempty"`

	// Multiplicity is the request-level multiple-comparison correction
	// block every test, post-test and overlay inherits field by field
	// when its own block leaves a field empty. Nil inherits
	// pulse.Options.DefaultMultiplicity; with neither, nothing is
	// corrected. See Multiplicity.
	Multiplicity *Multiplicity `json:"multiplicity,omitempty"`

	// Return shapes the Response: a preset, include / exclude paths and
	// a wire float precision. Nil returns the whole Response. See
	// Return.
	Return *Return `json:"return,omitempty"`

	// Params names constants this request's expressions read,
	// read-only, as params.<name>: every ATTR_FORMULA, FILTER_EXPRESSION,
	// total() argument, regression holdout and OVERLAY_FORMULA of the
	// request. Values are typed, never spliced into an expression's
	// text. Nil declares none. Gated by capability:params. See Params.
	Params Params `json:"params,omitempty"`
}

// ResponseMetadata holds metadata about a processing result.
type ResponseMetadata struct {
	// TotalRows is the total number of records in the cohort.
	TotalRows int64 `json:"total_rows"`

	// FilteredRows is the number of records after filtering.
	FilteredRows int64 `json:"filtered_rows"`

	// CohortFile is the filename of the processed cohort.
	CohortFile string `json:"cohort_file,omitempty"`
}

// ResponseWarning is the envelope-ready projection of a cross-cutting
// diagnostic emitted during a processing run (today: label-display
// resolver warnings). Codes match the errors.Code namespace so a
// CLI / MCP envelope wrapper can promote them verbatim.
type ResponseWarning struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Response is the processing result type.
type Response struct {
	// Label echoes the Compose slot this response answers: the slot's
	// caller-supplied Request.Label, or the synthesized
	// `request_<index+1>` default. Set by Compose / ComposeParallel
	// only — a standalone Process leaves it empty and absent on the
	// wire. An ordinary `return` path, kept by the standard and
	// minimal presets.
	Label string `json:"label,omitempty"`

	// Data holds the result rows as key-value maps.
	Data []map[string]any `json:"data,omitempty"`

	// Metadata holds information about the processing run.
	Metadata *ResponseMetadata `json:"metadata,omitempty"`

	// Tests holds tier-1 statistical test results, one per entry in
	// Request.Tests and in the same order. A grouped request holds one
	// per (test x group): test-major, the groups in the final Data row
	// order, each carrying GroupKey — entry k belongs to
	// Request.Tests[k / groups]. A slot a `return` plan skips is nil at
	// each of its positions; a test that failed on its data keeps its
	// entry with an Error (SlotError) — see Regressions.
	Tests []*TestResult `json:"tests,omitempty"`

	// PostTests holds tier-2 statistical test results, one per entry in
	// Request.PostTests and in the same order.
	PostTests []*TestResult `json:"post_tests,omitempty"`

	// Regressions holds the per-spec regression fits, one entry per
	// Request.Regressions in the same order. Engines never partially
	// populate a result: a fit that fails on its data (separation,
	// non-convergence, a rank-deficient design, too few rows) keeps its
	// entry with Name / Type and an Error (SlotError) — never a nil
	// entry, which means the slot was skipped by `return` — while every
	// other slot survives, and a PULSE_SLOT_FAILED warning names it.
	// Under Options.Strict the run fails with the fit's own code
	// instead. A configuration fault stays fatal at validation.
	Regressions []*RegressionResult `json:"regressions,omitempty"`

	// Matrices holds the matrix operator results, one entry per
	// Request.Matrices spec in the same order (ungrouped). Undefined
	// cells and scalars are null on the wire; a matrix that failed on
	// its data keeps its entry with an Error (SlotError) — see
	// Regressions. See MatrixResult.
	Matrices []MatrixResult `json:"matrices,omitempty"`

	// Warnings carries the run's coded run-time diagnostics: label
	// display (PULSE_LABEL_COLLISION, PULSE_LABEL_LOOKUP_MISS,
	// PULSE_LABEL_NOT_APPLIED), target-encode leakage, vectors, fitted
	// attributes, PULSE_RETURN_PATH_UNMATCHED, …. The `pulse api` --json
	// leaves (process, compose per slot, process-chain per stage) also
	// write each entry to the envelope's top-level warnings, keeping
	// this copy; ProcessStream iterators expose them through a
	// Warnings() method and ProcessStreamResult on its terminator. An
	// MCP result has no envelope: this slot is its warnings.
	Warnings []*ResponseWarning `json:"warnings,omitempty"`

	// Crosstab carries the structured matrix payload when the originating
	// Request set Crosstab and Shape=matrix (the default). Nil otherwise.
	// For Shape=long the cell rows land on Data verbatim — the existing
	// grouped-tuple format — so any consumer of long-form rows keeps
	// working unchanged.
	Crosstab *CrosstabResult `json:"crosstab,omitempty"`

	// Overlays carries the executed overlay layers, one entry per
	// Request.Overlays spec in matching order. Each layer holds its
	// derived payload (scalar / series / matrix) plus optional
	// renderer-friendly summary metadata. Omitted entirely when the
	// originating Request had no overlays.
	Overlays []OverlayLayer `json:"overlays,omitempty"`

	// Components carries the constituent-parts metadata emitted by each
	// operator during a processing run — per-aggregation N / NNull,
	// per-grouper bucket inventory, per-filterer N-in / N-out, crosstab
	// cell components, and run-wide totals.
	//
	// Naming-collision note: Response.Metadata (above) holds runtime
	// facts about the orchestrator pass — total / filtered row counts,
	// cohort filename — that are produced by service-layer wiring and
	// are independent of any specific operator. Response.Components
	// holds the computational components emitted BY the operators
	// themselves (the AGG_*, GROUP_*, FILTER_*, crosstab pipeline) and
	// keyed back to their slot identity. The two are deliberately
	// distinct slots: Metadata is "what the run did", Components is
	// "what each operator contributed". Consumers wanting both pull
	// from both slots; renderer-friendly summaries (cell components for
	// the parity overlays, etc.) live on Components.
	//
	// Additive omitempty contract: the slot is `*ResponseComponents` so
	// an unpopulated run marshals to no `components` key at all —
	// byte-identical to the pre-Components wire form. Every nested
	// slice / pointer / map is `omitempty` for the same reason; an
	// AGG-only run with no groupers omits `groupers`, a no-filter run
	// omits `filterers`, etc. format_version stays at "1.0" because
	// the slot is additive.
	Components *ResponseComponents `json:"components,omitempty"`

	// Returned marks a response shaped by a non-identity `return`
	// block: which preset it resolved from ("custom" for an explicit
	// include / exclude without one), the plan digest and the wire
	// precision. Nil — and absent on the wire — for an unshaped
	// response, so a request without `return` (or with one that
	// resolves to the full response) is byte-identical. It cannot be
	// excluded.
	Returned *ReturnedMarker `json:"returned,omitempty"`

	// plan is the resolved selection the response was shaped under;
	// its MarshalJSON honours it, so a plain json.Marshal of a shaped
	// response writes the shaped form. Set only by the facade.
	plan *returnplan.Plan
}

// ResponseComponents carries the constituent-parts metadata emitted by
// each operator slot during a processing run. Every nested field is
// `omitempty` so partially populated runs (an AGG-only request, a
// filter-free request, etc.) marshal to byte-identical wire output
// against the pre-Components baseline. See Response.Components for the
// runtime-facts vs computational-components naming-collision note.
//
// Field-level semantics:
//   - Aggregations: one entry per Request.Aggregations spec in matching
//     declared order. Slot identity rides on Label (mirrors
//     Aggregation.Label). The universal floor (N, NNull) is a typed
//     field on every entry; per-operator keys ride inside Operator.
//   - Groupers: one entry per Request.Groups spec in matching declared
//     order. Slot identity rides on Field. Per-grouper schema (bucket
//     edges, dict mappings, etc.) rides inside Operator.
//   - Crosstab: populated when the originating Request set Crosstab.
//   - Filterers: one entry per Request.Filterers spec in matching
//     declared order. Slot identity rides on Label.
//   - Run: optional run-wide totals (cohort record count, filtered
//     count, null count, shard count, partial-cohort reason). Distinct
//     from Response.Metadata which holds orchestrator-pass facts.
//   - Matrices: one entry per Response.Matrices result, in its order —
//     the floor {n, n_null, n_listwise_dropped}, pairwise
//     {min_pair_n, max_pair_n}, the weighted floor keys and the
//     operator's own keys (MatrixComponents).
//   - Attributes: one entry per Request.Attributes slot whose operator
//     declares a ComponentSchema, in declared order — NOT index-aligned
//     with Request.Attributes; Label + Type key it back. The attribute
//     floor {n, n_null} counts the rows the attribute scored, by
//     output presence (AttributeComponents).
type ResponseComponents struct {
	// Aggregations carries one AggregationComponents entry per
	// Request.Aggregations slot in matching declared order.
	Aggregations []AggregationComponents `json:"aggregations,omitempty"`

	// Groupers carries one GrouperComponents entry per Request.Groups
	// slot in matching declared order.
	Groupers []GrouperComponents `json:"groupers,omitempty"`

	// Crosstab carries the crosstab-level components (cell matrix +
	// margins). Populated only when the originating Request set
	// Crosstab; nil otherwise.
	Crosstab *CrosstabComponents `json:"crosstab,omitempty"`

	// Filterers carries one FiltererComponents entry per
	// Request.Filterers slot in matching declared order.
	Filterers []FiltererComponents `json:"filterers,omitempty"`

	// Run carries run-wide totals — cohort record count, filtered
	// count, null count, shard count, partial-cohort reason. Distinct
	// from Response.Metadata which holds orchestrator-pass facts about
	// the request execution (file name, etc.).
	Run *RunComponents `json:"run,omitempty"`

	// Matrices carries one MatrixComponents entry per Response.Matrices
	// result, in the same order (keyed back by Name, and GroupKey on a
	// grouped result).
	Matrices []MatrixComponents `json:"matrices,omitempty"`

	// Attributes carries one AttributeComponents entry per
	// Request.Attributes slot whose operator declares a ComponentSchema
	// (ATTR_SCALE_SCORE, ATTR_PROFILE_SIMILARITY, ATTR_VEC_DISTANCE; an
	// extension attribute registered with one),
	// in declared order. A slot whose operator declares none has no
	// entry, so the slice is keyed back by Label (and Type), never by
	// index.
	Attributes []AttributeComponents `json:"attributes,omitempty"`

	// plan is the resolved `return` selection a STANDALONE components
	// payload (a streamed chunk's) was shaped under, rooted at the
	// Response's `components` key; its MarshalJSON honours it so the
	// chunk's wire form equals the buffered shaped response's
	// `components`. Never set on the Components of a Response — the
	// Response's own plan reaches them there. Set only by the facade.
	plan *returnplan.Plan
}

// AggregationComponents carries per-aggregator constituent-parts
// metadata. The universal floor — N (records observed) and NNull
// (records skipped due to null input) — is a typed field on every
// entry; per-operator keys (mean / variance / mode_count / range_min,
// etc.) ride inside Operator. Slot identity rides on Label, which
// mirrors the originating Aggregation.Label so consumers can join the
// components entry back to the request slot without index arithmetic.
type AggregationComponents struct {
	// Label mirrors the originating Aggregation.Label so callers can
	// join the components entry back to its request slot.
	Label string `json:"label,omitempty"`

	// N is the number of NON-NULL input values the aggregator observed
	// (post-filter, after the null skip): the records counted in NNull
	// are not in N, so N + NNull is the post-filter record count. On a
	// paired-input slot (AGG_MAPE / AGG_RMSE / AGG_MAE, AGG_AUC)
	// presence is judged on Field alone. Universal floor — emitted by every
	// aggregator.
	N int `json:"n"`

	// NNull is the number of records the aggregator skipped because
	// the source field was null. Universal floor — emitted by every
	// aggregator.
	NNull int `json:"n_null"`

	// SumWeights is Σw over the rows whose value was present and whose
	// weight was valid — set only when a row weight is APPLIED to the
	// slot, so its absence is the per-slot "unweighted" signal. N and
	// NNull keep their value-presence meaning under a weight.
	SumWeights *float64 `json:"sum_weights,omitempty"`

	// NEff is Kish's effective sample size (Σw)² / Σw² over the same
	// rows — set only on a slot weighted with kind probability.
	NEff *float64 `json:"n_eff,omitempty"`

	// NWeightInvalid counts the rows whose value was present but whose
	// weight was invalid (null, negative, NaN / ±Inf, or non-integer
	// under kind frequency) and was therefore excluded — set only on a
	// weighted slot.
	NWeightInvalid *int `json:"n_weight_invalid,omitempty"`

	// Operator carries the per-aggregator schema-declared keys. Key
	// set is governed by the operator's ComponentSchema declaration in
	// internal/descriptor/capabilities_aggregators.go. Values are JSON-compatible
	// scalars or nested maps.
	Operator map[string]any `json:"operator,omitempty"`

	// Groups carries one AggregationGroupComponents entry per Data row
	// of a grouped (Request.Groups, non-crosstab) run, in the final
	// Response.Data order — Request.Sort included. Absent on an
	// ungrouped run. On a grouped run the slot-level N / NNull (and the
	// weighted floor keys) are the cohort-wide totals over every
	// filter-passing record, Operator is omitted, and each bucket's
	// figures ride its entry here.
	Groups []AggregationGroupComponents `json:"groups,omitempty"`
}

// AggregationGroupComponents is one bucket's figures for one
// aggregation slot of a grouped run: the universal floor, the weighted
// floor keys and the operator's own keys, exactly as an ungrouped run
// over that bucket's records would report them. GroupKey identifies
// the bucket (the same key the Data row carries under the grouper's
// field), mirroring MatrixComponents.GroupKey.
type AggregationGroupComponents struct {
	// GroupKey is the bucket's key tuple (one entry per grouper).
	GroupKey AxisKey `json:"group_key"`

	// N counts the bucket's records whose source field was present.
	N int `json:"n"`

	// NNull counts the bucket's records whose source field was null.
	NNull int `json:"n_null"`

	// SumWeights is Σw over the bucket's value-present, valid-weight
	// rows — set only on a weighted slot.
	SumWeights *float64 `json:"sum_weights,omitempty"`

	// NEff is the bucket's Kish effective sample size — set only on a
	// slot weighted with kind probability.
	NEff *float64 `json:"n_eff,omitempty"`

	// NWeightInvalid counts the bucket's value-present rows excluded
	// for an invalid weight — set only on a weighted slot.
	NWeightInvalid *int `json:"n_weight_invalid,omitempty"`

	// Operator carries the per-aggregator schema-declared keys for the
	// bucket, from the bucket aggregator's own components hook.
	Operator map[string]any `json:"operator,omitempty"`
}

// GrouperComponents carries per-grouper constituent-parts metadata —
// bucket inventory, edge values, dictionary mappings. The universal
// floor — TotalN (records partitioned, post-filter) and NNull (records
// that landed in a null / skip path) — is a typed field on every
// entry; per-operator keys (bucket arrays, edges, dict size,
// granularity, etc.) ride inside Operator. Slot identity rides on
// Field, which mirrors the originating Group.Field, plus Label, which
// mirrors the originating Group.Label when present so consumers can
// join the components entry back to its request slot without index
// arithmetic.
//
// For single-key groupers (CATEGORY / RANGE / ROUNDED / QUANTILE /
// DATE / SET_VALUE), TotalN equals the sum of bucket counts. For
// multi-key streaming groupers (GROUP_SET_PER_ELEMENT), a single
// record may contribute to multiple buckets, so the sum of bucket
// counts will exceed TotalN — TotalN remains row count, not emission
// count.
type GrouperComponents struct {
	// Field mirrors the originating Group.Field so callers can join
	// the components entry back to its request slot.
	Field string `json:"field,omitempty"`

	// TotalN is the number of records partitioned by the grouper
	// (post-filter). Universal floor — emitted by every grouper.
	// Zero-valued ints stay absent from JSON via omitempty so a
	// grouper entry with only operator keys does not leak a noisy
	// `total_n: 0` onto the wire.
	TotalN int `json:"total_n,omitempty"`

	// NNull is the number of records that landed in a null / skip
	// path (null field value, empty set, missing key). Universal
	// floor — emitted by every grouper. Zero-valued ints stay absent
	// from JSON via omitempty.
	NNull int `json:"n_null,omitempty"`

	// Operator carries the per-grouper schema-declared keys (bucket
	// edges, dict mappings, range_min / range_max, etc.). Key set is
	// governed by the operator's ComponentSchema declaration in
	// internal/descriptor/capabilities_groupers.go.
	Operator map[string]any `json:"operator,omitempty"`

	// Label mirrors the originating Group.Label so callers can join
	// the components entry back to its request slot when multiple
	// groupers share the same Field.
	Label string `json:"label,omitempty"`
}

// CrosstabComponents is declared in types/crosstab.go alongside
// MatrixPayload. The struct carries the per-cell components matrix,
// per-axis margin counts + components, grand-total counts +
// components, axis-key grouper components, and include / exclude
// record sanity counters — mirroring MatrixPayload coordinate-for-
// coordinate so consumers can index components by the same (r, c).

// AttributeComponents carries one attribute slot's constituent-parts
// metadata: the attribute floor plus the operator's own
// ComponentSchema keys. Attributes are cohort-wide on every arm — they
// run over the filter-passing records before any grouping — so a
// grouped or crosstab run carries one entry per slot, never per
// bucket. Attributes are weight-irrelevant here: no weighted floor
// keys ride the entry.
type AttributeComponents struct {
	// Label is the slot's EFFECTIVE output label — Attribute.Label, or
	// the default label the engine wrote the column under when the
	// request left it empty — so it always names the Data column.
	Label string `json:"label"`

	// Type is the slot's operator (Attribute.Type).
	Type AttributeType `json:"type"`

	// N is the number of filter-passing rows whose attribute output is
	// PRESENT (non-null). Attribute floor. A before-filters attribute
	// (ATTR_PREVIOUS_STATE) scores every decoded record, so its N and
	// NNull count the pre-filter rows.
	N int `json:"n"`

	// NNull is the number of filter-passing rows whose attribute
	// output is null. N + NNull is the post-filter record count the
	// attribute scored (the pre-filter count for a before-filters
	// attribute). Attribute floor.
	NNull int `json:"n_null"`

	// Operator carries the operator's schema-declared keys (the
	// manifest's components_schemas.attributes entry). Absent when the
	// operator emitted none.
	Operator map[string]any `json:"operator,omitempty"`
}

// FiltererComponents carries per-filterer constituent-parts metadata —
// records in (post-prior-stage), records out (post-this-filter), and
// records dropped due to null input on the filter field. Slot identity
// rides on Label, which mirrors the originating Filterer.Label so
// consumers can join the components entry back to its request slot.
type FiltererComponents struct {
	// Label mirrors the originating Filterer.Label so callers can join
	// the components entry back to its request slot.
	Label string `json:"label,omitempty"`

	// NIn is the number of records the filterer observed (post-prior-
	// stage, pre-this-filter).
	NIn int `json:"n_in"`

	// NOut is the number of records the filterer admitted to the next
	// stage (post-this-filter).
	NOut int `json:"n_out"`

	// NNullInput is the number of records the filterer dropped because
	// the source field was null.
	NNullInput int `json:"n_null_input"`
}

// RunComponents carries run-wide constituent-parts metadata — cohort
// totals, filtered counts, null counts, shard count, partial-cohort
// reason. Distinct from Response.Metadata which holds orchestrator-
// pass facts (cohort filename, etc.); RunComponents is the
// computational-component view of the same run.
//
// Metadata-vs-Run coexistence (see CLAUDE.md Output Format Contract):
// Response.Metadata.TotalRows and Components.Run.TotalRecords carry the
// same value by construction at every orchestrator exit. Metadata
// retains the non-numerical run facts (cohort filename); Run carries
// the typed counters consumers compute against (joins, ratios,
// partial-cohort diagnostics). Both slots are populated on every
// successful Process call so consumers can rely on either side.
type RunComponents struct {
	// TotalRecords is the total number of records in the underlying
	// cohort (pre-filter).
	TotalRecords int64 `json:"total_records"`

	// FilteredRecords is the number of records that survived the
	// filter chain.
	FilteredRecords int64 `json:"filtered_records"`

	// NullRecords is the number of records dropped due to null input
	// somewhere in the pipeline.
	NullRecords int64 `json:"null_records"`

	// ShardCount is the number of shards processed when the cohort
	// resolved to a shard archive. Zero for single-file cohorts.
	ShardCount int `json:"shard_count,omitempty"`

	// PartialCohortReason carries a free-form diagnostic explaining
	// why the run consumed less than the full cohort (e.g. a shard
	// failed to open). Empty when the run consumed the full cohort.
	PartialCohortReason string `json:"partial_cohort_reason,omitempty"`
}

// FileRequest identifies a file for operations like inspect.
type FileRequest struct {
	// Filename is the name of the file.
	Filename string `json:"filename"`

	// DataDir is the directory containing the file.
	DataDir string `json:"data_dir,omitempty"`
}

// FileResponse describes a file's metadata.
type FileResponse struct {
	// Filename is the name of the file.
	Filename string `json:"filename"`

	// RecordCount is the number of records in the file.
	RecordCount int64 `json:"record_count"`

	// Fields is the list of field names in the file.
	Fields []string `json:"fields,omitempty"`
}

// ComposedRequest bundles multiple requests for batch execution.
type ComposedRequest struct {
	// Requests is the list of individual requests to execute.
	Requests []*Request `json:"requests"`

	// Overlays is the optional list of Compose-only overlay layer
	// specifications. Each spec is a cross-Request overlay that
	// decorates one slot's result with a comparison against another
	// slot's result inside the same ComposedRequest — the slots are
	// addressed by their per-Request Label field (with empty Labels
	// auto-defaulted to `request_<index+1>` before reference lookup,
	// before reference lookup). Produces one OverlayLayer in
	// ComposedResponse.Overlays in matching index order.
	//
	// Forward-compat: a ComposedRequest without overlays produces
	// byte-identical JSON to the same request with `Overlays: nil`
	// because the slot is `omitempty` — nil / empty slices marshal to
	// no key at all. The canonical-hash routine inherits that contract
	// via the data-driven JSON walk in types/hash.go (see
	// TestComposedRequest_OverlayFreeByteIdentity in
	// types/hash_test.go).
	Overlays []ComposeOverlaySpec `json:"overlays,omitempty"`

	// Multiplicity is the Compose-level multiple-comparison correction
	// block: every slot request inherits it after its own block, and
	// every Compose-host overlay after its own. Family `compose` pools
	// the members across slots. Nil inherits
	// pulse.Options.DefaultMultiplicity. See Multiplicity.
	Multiplicity *Multiplicity `json:"multiplicity,omitempty"`

	// Return shapes the TOP-LEVEL Compose overlays: paths root at
	// ComposedResponse and only `overlays…` paths are valid (a slot is
	// shaped by its own Requests[i].Return). Applied after the overlay
	// fold, so the layers are computed from the unshaped slot
	// responses. Nil leaves the overlays whole; no instance default
	// applies at this level. See Return.
	Return *Return `json:"return,omitempty"`

	// Sweep is the optional parameter sweep: one request body expanded
	// over its axes into compose slots appended after Requests, before
	// any slot runs. Gated by capability:compose_sweep. Nil is absent —
	// the slot is `omitempty`, so a sweep-free request is byte-identical
	// on the wire and under Hash. See SweepSpec.
	Sweep *SweepSpec `json:"sweep,omitempty"`

	// Fits are the batch's named fits (regressions or vector
	// batteries — FitSpec): each runs once,
	// before any slot (in parallel among themselves under
	// ComposeParallel), and every slot — sweep-expanded ones included —
	// receives the bound models by name. Gated by capability:compose_fits.
	// Nil is absent — the slot is `omitempty`, so a fit-free request is
	// byte-identical on the wire and under Hash. See FitSpec.
	Fits []FitSpec `json:"fits,omitempty"`

	// Banner is the optional banner table: sections × column groups
	// (plus a Total block) expanded into crosstab slots appended after
	// Requests and any sweep slots, before any slot runs, and stitched
	// back into ComposedResponse.Banner. Gated by
	// capability:compose_banner. Nil is absent — the slot is
	// `omitempty`, so a banner-free request is byte-identical on the
	// wire and under Hash. See BannerSpec.
	Banner *BannerSpec `json:"banner,omitempty"`

	// Params names constants every sub-request of the batch inherits:
	// each slot (sweep- and banner-expanded ones included) and each fit
	// request reads them as params.<name> beside its own, a name the
	// sub-request declares itself winning. The merge happens before any
	// slot runs, so the normalized request echoes the merged params on
	// every sub-request. The Compose-host OVERLAY_FORMULA specs read
	// these top-level params. Nil declares none. Gated by
	// capability:params. See Params.
	Params Params `json:"params,omitempty"`
}

// ComposedResponse is the structured response shape for ComposedRequest
// execution. It carries the per-slot Response objects emitted by
// internal/service.Compose / internal/service.ComposeParallel alongside the
// Compose-level Overlays slice — one OverlayLayer per
// ComposedRequest.Overlays spec in matching index order.
//
// Scope note: the type lives here so the request-side
// ComposeOverlaySpec catalog has a typed response sibling to write
// against. The pulse.Pulse.Compose facade now returns
// *ComposedResponse and the runtime populates the Overlays slot here
// at the post-slot barrier.
//
// Forward-compat: every ComposedResponse marshalled before any overlay
// landed produces byte-identical JSON to the same shape with
// `Overlays: nil` because the slot is `omitempty` — nil / empty slices
// marshal to no key at all. See TestComposedResponse_OverlayFreeByteIdentical
// in types/types_test.go for the lock.
type ComposedResponse struct {
	// Responses is the per-slot list of Response objects, one entry per
	// ComposedRequest.Requests slot in matching order.
	Responses []*Response `json:"responses"`

	// Overlays carries the executed Compose-level overlay layers, one
	// entry per ComposedRequest.Overlays spec in matching order. Each
	// layer holds its derived payload (scalar / series / matrix) plus
	// optional renderer-friendly summary metadata — reuses the
	// OverlayLayer shape so renderers handle Compose layers and
	// per-Request layers with the same machinery. Omitted entirely when
	// the originating ComposedRequest had no Overlays.
	Overlays []OverlayLayer `json:"overlays,omitempty"`

	// Returned marks a ComposedResponse whose top-level overlays were
	// shaped by a non-identity ComposedRequest.Return. Nil (absent) for
	// an unshaped one; each slot carries its own Response.Returned.
	Returned *ReturnedMarker `json:"returned,omitempty"`

	// Ranking is the sweep rank: one entry per ranked sweep slot in rank
	// order (ComposedRequest.Sweep.Rank). Omitted when the request set no
	// rank. Gated by capability:compose_sweep with the `sweep` slot.
	Ranking []RankEntry `json:"ranking,omitempty"`

	// Fits carries each ComposedRequest.Fits entry's result, keyed by
	// the fit's name (JSON keys sort by name): a regression fit's
	// RegressionResult or a vector fit's VectorFitResult (FitResult — a
	// regression entry's wire form is the regression-only one). Omitted
	// when the request declared no fits. Like `ranking` it sits outside
	// the Compose-level `return`: always emitted, never rounded. Gated
	// by capability:compose_fits with the `fits` request slot.
	Fits map[string]*FitResult `json:"fits,omitempty"`

	// Banner carries the stitched banner tables, one per
	// ComposedRequest.Banner section (BannerResult), read off the
	// finished, multiplicity-folded, still-unshaped banner slots. Omitted
	// when the request set no banner. Like `ranking` it sits outside the
	// Compose-level `return`: always emitted, never rounded. Gated by
	// capability:compose_banner with the `banner` request slot.
	Banner *BannerResult `json:"banner,omitempty"`

	// Warnings carries the batch-level diagnostics no slot owns: today
	// a fit's PULSE_MODEL_OVERWRITTEN (a `fits` save — or a save on the
	// fit request's regression — replaced an existing model file), with
	// details.fit locating the entry. A `warnings` keep path: the
	// Compose-level `return` (overlays only) never drops it. Omitted when
	// empty, so a warning-free batch is byte-identical on the wire.
	Warnings []*ResponseWarning `json:"warnings,omitempty"`

	// NormalizedRequest is the effective request the batch ran: a
	// sweep's expanded `requests` (explicit slots, then one labelled
	// slot per combination) with the sweep overlays appended and no
	// `sweep`; a sweep-free request as passed. Populated only when
	// pulse.Options.EchoRequest is true, nil otherwise. Never on the
	// wire: the CLI publishes it as the envelope's `request` echo.
	NormalizedRequest *ComposedRequest `json:"-"`

	// plan is the resolved Compose-level selection; MarshalJSON honours
	// it. Set only by the facade.
	plan *returnplan.Plan
}

// VersionResponse provides build and version information.
type VersionResponse struct {
	// Version is the semantic version string.
	Version string `json:"version"`

	// BuildDate is the build timestamp.
	BuildDate string `json:"build_date,omitempty"`

	// GoVersion is the Go compiler version used.
	GoVersion string `json:"go_version,omitempty"`
}
