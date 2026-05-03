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
// single pass. Every attribute today is buffered (population stats need a
// first pass); the method exists so future row-local attributes can opt in
// without changing the shape of the streamability check.
func (t AttributeType) Streamable() bool {
	switch t {
	case ATTR_ZSCORE, ATTR_TSCORE, ATTR_NORMALIZED,
		ATTR_FORMULA, ATTR_PERCENTILE, ATTR_DATE_PART:
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
