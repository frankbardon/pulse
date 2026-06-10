package types

// Streamable reports whether this aggregation type supports the single-pass
// streaming execution path. Streamable aggregators implement
// processing.OnlineAggregator (UpdateRow + Finalize) and produce a result
// with O(1) or O(unique) state per row.
//
// Source of truth for predict.Streamable; cross-checked at test time against
// the processing registry by TestRegistryStreamabilityMatchesTypes.
//
// Default branch returns false so newly-added aggregator types must opt in
// explicitly.
func (t AggregationType) Streamable() bool {
	switch t {
	case AGG_COUNT, AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX,
		AGG_STDDEV, AGG_VARIANCE, AGG_RANGE,
		AGG_FREQUENCY, AGG_MODE,
		AGG_SKEWNESS, AGG_KURTOSIS,
		AGG_DISTINCT_COUNT,
		AGG_NULL_COUNT,
		AGG_WEIGHTED_MEAN, AGG_RATIO,
		AGG_CI_LOWER, AGG_CI_UPPER,
		AGG_SET_UNION, AGG_SET_INTERSECTION, AGG_SET_FREQUENCY,
		AGG_SET_CARDINALITY_SUM, AGG_SET_CARDINALITY_AVG,
		AGG_SET_DISTINCT_VALUES:
		return true
	case AGG_MEDIAN, AGG_PERCENTILE, AGG_ZSCORE:
		return false
	}
	return false
}

// Mergeable reports whether this aggregation type's running state can
// be combined across partitions of the input via an associative+
// commutative merge (count/sum/min/max/null_count), a parallel-friendly
// recurrence (Welford-mean / variance / stddev), or a union of per-
// value count maps (frequency / mode / distinct_count). The per-shard
// parallel reducer in service/shard_reduce.go consults this method
// (mirrored by processing.CanMergeRequest) to decide whether to fan
// out shard processing across a bounded worker pool.
//
// Mergeable implies Streamable — a buffered-only aggregator cannot
// expose mergeable state. The default branch returns false so newly-
// added aggregator types must opt in explicitly. AGG_MEDIAN /
// AGG_PERCENTILE / AGG_ZSCORE require a sorted view of every value
// and stay non-mergeable; AGG_SKEWNESS / AGG_KURTOSIS rely on M3/M4
// recurrences whose parallel-merge formula is non-trivial and is
// deferred to a follow-up — they fall through to the serial path.
func (t AggregationType) Mergeable() bool {
	switch t {
	case AGG_COUNT, AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX,
		AGG_RANGE, AGG_VARIANCE, AGG_STDDEV,
		AGG_FREQUENCY, AGG_MODE, AGG_DISTINCT_COUNT,
		AGG_NULL_COUNT,
		AGG_WEIGHTED_MEAN, AGG_RATIO,
		AGG_CI_LOWER, AGG_CI_UPPER,
		AGG_SET_UNION, AGG_SET_INTERSECTION, AGG_SET_FREQUENCY,
		AGG_SET_CARDINALITY_SUM, AGG_SET_CARDINALITY_AVG,
		AGG_SET_DISTINCT_VALUES:
		return true
	case AGG_MEDIAN, AGG_PERCENTILE, AGG_ZSCORE,
		AGG_SKEWNESS, AGG_KURTOSIS:
		return false
	}
	return false
}

// MarginReducibility classifies how a crosstab margin for this
// aggregator can be computed. Three classes:
//
//   - MarginSummable — margin = sum of cells (e.g. AGG_COUNT, AGG_SUM,
//     AGG_NULL_COUNT). The reshape pass can derive the margin cheaply
//     from the long-form cell table without re-aggregating.
//   - MarginMeanReducible — margin derivable only when each cell also
//     carries its observation count (e.g. AGG_AVERAGE = Σ(cellMean·cellN)
//     / ΣcellN). Pulse does not yet emit per-cell counts in long form,
//     so v1 routes these through the recompute path; the classification
//     is preserved for future optimization.
//   - MarginRecompute — margin cannot be derived from cells and must be
//     recomputed over the raw rows (every order- or distribution-
//     dependent aggregator: AGG_MEDIAN, AGG_PERCENTILE, AGG_STDDEV,
//     AGG_VARIANCE, AGG_MODE, etc.).
//
// The default branch returns MarginRecompute so any newly-added
// aggregator that forgets to opt in computes the correct margin (slower
// but right) rather than silently producing the wrong margin.
//
// service/crosstab.go consults this method to decide whether the
// margin can be derived from cell sums (when MarginSummable + no
// normalization round-off concern) or must be recomputed via a sibling
// Compose request. In v1 every margin is recomputed (see
// skills/crosstab-guide.md "Margin computation"); the classification
// drives the manifest capability block and future fast-path work.
func (t AggregationType) MarginReducibility() MarginReducibility {
	switch t {
	case AGG_COUNT, AGG_SUM, AGG_NULL_COUNT, AGG_DISTINCT_COUNT,
		AGG_FREQUENCY,
		// Set unions, popcount sums, and per-element frequency
		// histograms all reduce by addition across cells.
		AGG_SET_UNION, AGG_SET_FREQUENCY,
		AGG_SET_CARDINALITY_SUM, AGG_SET_DISTINCT_VALUES:
		// FREQUENCY is summable per category: each cell's per-value
		// counts merge by key union (same logic the per-shard reducer
		// uses); classified as summable for that reason. The reshape
		// pass treats it as recompute today because the long-form
		// emitter writes a map, not a scalar.
		return MarginSummable
	case AGG_AVERAGE, AGG_WEIGHTED_MEAN, AGG_RATIO,
		AGG_SET_CARDINALITY_AVG:
		return MarginMeanReducible
	case AGG_MIN, AGG_MAX, AGG_RANGE,
		AGG_STDDEV, AGG_VARIANCE,
		AGG_MEDIAN, AGG_PERCENTILE, AGG_MODE,
		AGG_ZSCORE, AGG_SKEWNESS, AGG_KURTOSIS,
		AGG_CI_LOWER, AGG_CI_UPPER,
		// AND across cells ≠ AND across all rows in general — recompute
		// from raw rows.
		AGG_SET_INTERSECTION:
		return MarginRecompute
	}
	return MarginRecompute
}

// MarginReducibility classifies how a crosstab margin can be derived
// from per-cell aggregations. See AggregationType.MarginReducibility.
type MarginReducibility string

const (
	// MarginSummable means the margin equals the sum of cell values
	// (count, sum, null_count, distinct_count, frequency).
	MarginSummable MarginReducibility = "summable"
	// MarginMeanReducible means the margin is derivable only when each
	// cell also carries its observation count (average, ratio).
	MarginMeanReducible MarginReducibility = "mean_reducible"
	// MarginRecompute means the margin cannot be derived from cells and
	// must be recomputed over the raw filter-passing rows (median,
	// stddev, percentile, mode, ...).
	MarginRecompute MarginReducibility = "recompute"
)

// Mergeable reports whether this group type's per-key state can be
// combined across partitions of the input. CATEGORY and RANGE (online)
// derive their key purely from the row's value so per-shard buckets
// merge by key-union; QUANTILE/DATE depend on the full set or a
// finalize-time bucketization that the parallel reducer cannot
// replicate piecewise. ROUNDED could be mergeable in principle but is
// deferred — the parallel orchestrator only opts in on combinations
// we exercise in goldens today.
func (t GroupType) Mergeable() bool {
	switch t {
	case GROUP_CATEGORY, GROUP_RANGE,
		GROUP_SET_VALUE, GROUP_SET_PER_ELEMENT:
		return true
	case GROUP_ROUNDED, GROUP_QUANTILE, GROUP_DATE:
		return false
	}
	return false
}

// Streamable reports whether this attribute type can be computed in a
// streaming path. Three tiers exist at runtime:
//
//   - Row-local: FORMULA, DATE_PART implement processing.RowLocalAttribute
//     and execute inline with no PrePass.
//   - Two-pass: ZSCORE, TSCORE, NORMALIZED implement
//     processing.TwoPassAttribute and need a PrePass over filter-passing
//     records, Finalize, then per-row Row() in pass 2 (iter.Reset()).
//   - Buffered-only: PERCENTILE needs a sorted view of every value;
//     no streaming algorithm preserves exact rank semantics.
//
// Streamable() returns true for the first two tiers since both routes
// avoid materializing the full record set in memory.
func (t AttributeType) Streamable() bool {
	switch t {
	case ATTR_FORMULA, ATTR_DATE_PART,
		ATTR_ZSCORE, ATTR_TSCORE, ATTR_NORMALIZED,
		ATTR_REG_FITTED, ATTR_REG_RESIDUAL, ATTR_REG_LEVERAGE,
		ATTR_SET_POPCOUNT, ATTR_SET_HAS:
		return true
	case ATTR_PERCENTILE:
		return false
	}
	return false
}

// Streamable reports whether this filterer type evaluates per-row without
// looking at other rows. All registered filterers are row-local today.
func (t FiltererType) Streamable() bool {
	switch t {
	case FILTER_INCLUDE, FILTER_EXCLUDE, FILTER_RANGE,
		FILTER_EXPRESSION,
		FILTER_NULL,
		FILTER_TRUE, FILTER_FALSE,
		FILTER_SET_CONTAINS_ANY, FILTER_SET_CONTAINS_ALL,
		FILTER_SET_CONTAINS_NONE, FILTER_SET_EQUALS:
		return true
	}
	return false
}

// Streamable reports whether this group type can emit groups before the
// input is exhausted. CATEGORY/ROUNDED/RANGE bucket per row;
// QUANTILE/DATE require finalize-time work over the full set.
//
// The streaming Process path does not currently emit grouped output even
// for streamable groupers — Request.Streamable returns false whenever
// groups are present. The method is wired through so a future grouped
// streaming iterator can flip the gate without re-deriving the rule.
func (t GroupType) Streamable() bool {
	switch t {
	case GROUP_CATEGORY, GROUP_ROUNDED, GROUP_RANGE,
		GROUP_SET_VALUE, GROUP_SET_PER_ELEMENT:
		return true
	case GROUP_QUANTILE, GROUP_DATE:
		return false
	}
	return false
}

// Streamable reports whether this window type can be computed without
// buffering. All window operators run over the post-aggregate row set in
// a final pass; none stream today.
func (t WindowType) Streamable() bool {
	return false
}

// Streamable reports whether this feature type can run in the
// pre-pass+finalize+emit streaming pipeline (feature.StreamingComputer).
//
// Source of truth is feature.IsStreamable(req.Features, schema) at runtime;
// this method mirrors the per-type capability used by predict.
func (t FeatureType) Streamable() bool {
	switch t {
	case FEAT_LOG, FEAT_SQRT, FEAT_BUCKETIZE,
		FEAT_ONE_HOT, FEAT_DATE_FEATURES,
		FEAT_FREQUENCY_ENCODE, FEAT_TARGET_ENCODE,
		FEAT_TRAIN_TEST_SPLIT, FEAT_POLY:
		return true
	}
	return false
}

// Streamable reports whether this test type can be evaluated in the
// streaming Process path as a tier-1 row test. Two tiers exist at runtime:
//
//   - Online-moments tests (TEST_T, TEST_WELCH, TEST_CHISQ, TEST_ANOVA_F)
//     reuse the running (mean, variance, n) and per-bucket counts already
//     produced by the streaming aggregator path. They consume zero extra
//     passes when their inputs overlap with an active aggregator.
//   - Buffered-only tests (TEST_KS, TEST_TUKEY_HSD, TEST_TREND) require a
//     sorted view, a finalized post-hoc matrix, or an ordered series and
//     cannot stream. Predict flags requests containing these tests as
//     non-streamable.
//
// Tier-2 PostTests are always buffered regardless of TestType: they
// execute over the materialized result row set, after windows. Streamable
// here reports tier-1 capability only.
//
// Default branch returns false so newly-added test types must opt in.
func (t TestType) Streamable() bool {
	switch t {
	case TEST_T, TEST_WELCH, TEST_CHISQ, TEST_ANOVA_F,
		TEST_PEARSON_R, TEST_PAIRED_T, TEST_PROP_Z,
		TEST_ANOVA_WELCH, TEST_Z_TWO_SAMPLE:
		return true
	case TEST_KS, TEST_TUKEY_HSD, TEST_TREND,
		TEST_MANN_WHITNEY_U, TEST_WILCOXON_SR,
		TEST_KRUSKAL_WALLIS, TEST_SPEARMAN_R, TEST_KENDALL_TAU,
		TEST_ANOVA_RM, TEST_BROWN_FORSYTHE,
		TEST_FISHER_EXACT, TEST_SHAPIRO_WILK:
		return false
	}
	return false
}
