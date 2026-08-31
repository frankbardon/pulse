package processing

import (
	"github.com/frankbardon/pulse/types"
)

// aggregatorRegistry maps aggregation types to their factory functions.
var aggregatorRegistry = map[types.AggregationType]AggregatorFactory{
	types.AGG_COUNT:          newCountAggregator,
	types.AGG_SUM:            newSumAggregator,
	types.AGG_AVERAGE:        newAverageAggregator,
	types.AGG_MIN:            newMinAggregator,
	types.AGG_MAX:            newMaxAggregator,
	types.AGG_STDDEV:         newStdDevAggregator,
	types.AGG_RANGE:          newRangeAggregator,
	types.AGG_FREQUENCY:      newFrequencyAggregator,
	types.AGG_ZSCORE:         newZScoreAggregator,
	types.AGG_MEDIAN:         newMedianAggregator,
	types.AGG_VARIANCE:       newVarianceAggregator,
	types.AGG_MODE:           newModeAggregator,
	types.AGG_SKEWNESS:       newSkewnessAggregator,
	types.AGG_KURTOSIS:       newKurtosisAggregator,
	types.AGG_DISTINCT_COUNT: newDistinctCountAggregator,
	types.AGG_PERCENTILE:     newPercentileAggregator,
	types.AGG_NULL_COUNT:     newNullCountAggregator,
	types.AGG_WEIGHTED_MEAN:  newWeightedMeanAggregator,
	types.AGG_RATIO:          newRatioAggregator,
	types.AGG_DISTINCT_SUM:   newDistinctSumAggregator,
	types.AGG_CI_LOWER:       newCIAggregator(ciLower),
	types.AGG_CI_UPPER:       newCIAggregator(ciUpper),
	types.AGG_WELFORD:        newWelfordAggregator,

	types.AGG_SET_UNION:           newSetUnionAggregator,
	types.AGG_SET_INTERSECTION:    newSetIntersectionAggregator,
	types.AGG_SET_FREQUENCY:       newSetFrequencyAggregator,
	types.AGG_SET_CARDINALITY_SUM: newSetCardinalitySumAggregator,
	types.AGG_SET_CARDINALITY_AVG: newSetCardinalityAvgAggregator,
	types.AGG_SET_DISTINCT_VALUES: newSetDistinctValuesAggregator,
}

// attributeRegistry maps attribute types to their factory functions.
var attributeRegistry = map[types.AttributeType]AttributeFactory{
	types.ATTR_ZSCORE:       newZScoreAttribute,
	types.ATTR_TSCORE:       newTScoreAttribute,
	types.ATTR_NORMALIZED:   newNormalizedAttribute,
	types.ATTR_FORMULA:      newFormulaAttribute,
	types.ATTR_PERCENTILE:   newPercentileAttribute,
	types.ATTR_DATE_PART:    newDatePartAttribute,
	types.ATTR_REG_FITTED:   newRegFittedAttribute,
	types.ATTR_REG_RESIDUAL: newRegResidualAttribute,
	types.ATTR_REG_LEVERAGE: newRegLeverageAttribute,

	types.ATTR_SET_POPCOUNT: newSetPopcountAttribute,
	types.ATTR_SET_HAS:      newSetHasAttribute,
}

// filtererRegistry maps filterer types to their factory functions.
var filtererRegistry = map[types.FiltererType]FiltererFactory{
	types.FILTER_INCLUDE:    newIncludeFilterer,
	types.FILTER_EXCLUDE:    newExcludeFilterer,
	types.FILTER_RANGE:      newRangeFilterer,
	types.FILTER_EXPRESSION: newExpressionFilterer,
	types.FILTER_NULL:       newNullFilterer,
	types.FILTER_TRUE:       newTrueFilterer,
	types.FILTER_FALSE:      newFalseFilterer,

	types.FILTER_DATE_RANGES: newDateRangesFilterer,

	types.FILTER_SET_CONTAINS_ANY:  newSetContainsAnyFilterer,
	types.FILTER_SET_CONTAINS_ALL:  newSetContainsAllFilterer,
	types.FILTER_SET_CONTAINS_NONE: newSetContainsNoneFilterer,
	types.FILTER_SET_EQUALS:        newSetEqualsFilterer,
}

// grouperRegistry maps group types to their factory functions.
var grouperRegistry = map[types.GroupType]GrouperFactory{
	types.GROUP_CATEGORY:    newCategoryGrouper,
	types.GROUP_DATE:        newDateGrouper,
	types.GROUP_DATE_RANGES: newDateRangesGrouper,
	types.GROUP_QUANTILE:    newQuantileGrouper,
	types.GROUP_RANGE:       newRangeGrouper,
	types.GROUP_ROUNDED:     newRoundedGrouper,

	types.GROUP_SET_VALUE:       newSetValueGrouper,
	types.GROUP_SET_PER_ELEMENT: newSetPerElementGrouper,
}
