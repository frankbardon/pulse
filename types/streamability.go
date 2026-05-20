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
		AGG_CI_LOWER, AGG_CI_UPPER:
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
		AGG_CI_LOWER, AGG_CI_UPPER:
		return true
	case AGG_MEDIAN, AGG_PERCENTILE, AGG_ZSCORE,
		AGG_SKEWNESS, AGG_KURTOSIS:
		return false
	}
	return false
}

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
	case GROUP_CATEGORY, GROUP_RANGE:
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
		ATTR_REG_FITTED, ATTR_REG_RESIDUAL, ATTR_REG_LEVERAGE:
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
		FILTER_NULL:
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
	case GROUP_CATEGORY, GROUP_ROUNDED, GROUP_RANGE:
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
		TEST_ANOVA_WELCH:
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
