package processing

import (
	"github.com/frankbardon/pulse/types"
)

// aggregatorRegistry maps aggregation types to their factory functions.
var aggregatorRegistry = map[types.AggregationType]AggregatorFactory{
	types.AGG_COUNT:     newCountAggregator,
	types.AGG_SUM:       newSumAggregator,
	types.AGG_AVERAGE:   newAverageAggregator,
	types.AGG_MIN:       newMinAggregator,
	types.AGG_MAX:       newMaxAggregator,
	types.AGG_STDDEV:    newStdDevAggregator,
	types.AGG_RANGE:     newRangeAggregator,
	types.AGG_FREQUENCY: newFrequencyAggregator,
	types.AGG_ZSCORE:    newZScoreAggregator,
}

// attributeRegistry maps attribute types to their factory functions.
var attributeRegistry = map[types.AttributeType]AttributeFactory{
	types.ATTR_ZSCORE:     newZScoreAttribute,
	types.ATTR_TSCORE:     newTScoreAttribute,
	types.ATTR_NORMALIZED: newNormalizedAttribute,
	types.ATTR_FORMULA:    newFormulaAttribute,
	types.ATTR_PERCENTILE: newPercentileAttribute,
	types.ATTR_RANK:       newRankAttribute,
	types.ATTR_DATE_PART:  newDatePartAttribute,
}

// filtererRegistry maps filterer types to their factory functions.
var filtererRegistry = map[types.FiltererType]FiltererFactory{
	types.FILTER_INCLUDE:    newIncludeFilterer,
	types.FILTER_EXCLUDE:    newExcludeFilterer,
	types.FILTER_RANGE:      newRangeFilterer,
	types.FILTER_EXPRESSION: newExpressionFilterer,
}

// grouperRegistry maps group types to their factory functions.
var grouperRegistry = map[types.GroupType]GrouperFactory{
	types.GROUP_CATEGORY: newCategoryGrouper,
	types.GROUP_ROUNDED:  newRoundedGrouper,
}
