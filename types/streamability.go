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
		AGG_DISTINCT_COUNT:
		return true
	case AGG_MEDIAN, AGG_PERCENTILE, AGG_ZSCORE,
		AGG_GEO_CENTROID, AGG_GEO_BBOX:
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
		ATTR_ZSCORE, ATTR_TSCORE, ATTR_NORMALIZED:
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
		FILTER_GEO_WITHIN, FILTER_GEO_WITHIN_RADIUS_M:
		return true
	}
	return false
}

// Streamable reports whether this group type can emit groups before the
// input is exhausted. CATEGORY/ROUNDED/RANGE/H3_CELL bucket per row;
// QUANTILE/DATE require finalize-time work over the full set.
//
// The streaming Process path does not currently emit grouped output even
// for streamable groupers — Request.Streamable returns false whenever
// groups are present. The method is wired through so a future grouped
// streaming iterator can flip the gate without re-deriving the rule.
func (t GroupType) Streamable() bool {
	switch t {
	case GROUP_CATEGORY, GROUP_ROUNDED, GROUP_RANGE, GROUP_H3_CELL:
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
		FEAT_TRAIN_TEST_SPLIT:
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
	case TEST_T, TEST_WELCH, TEST_CHISQ, TEST_ANOVA_F:
		return true
	case TEST_KS, TEST_TUKEY_HSD, TEST_TREND:
		return false
	}
	return false
}
