package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// aggregatorRegistry maps aggregation types to their factory functions.
var aggregatorRegistry = map[types.AggregationType]AggregatorFactory{
	types.AGG_COUNT:          newCountAggregator,
	types.AGG_SUM:            numericReader(newSumAggregator),
	types.AGG_AVERAGE:        numericReader(newAverageAggregator),
	types.AGG_MIN:            numericReader(newMinAggregator),
	types.AGG_MAX:            numericReader(newMaxAggregator),
	types.AGG_STDDEV:         numericReader(newStdDevAggregator),
	types.AGG_RANGE:          numericReader(newRangeAggregator),
	types.AGG_MODE_COUNT:     newModeCountAggregator,
	types.AGG_FREQUENCY:      newFrequencyAggregator,
	types.AGG_ZSCORE:         numericReader(newZScoreAggregator),
	types.AGG_MEDIAN:         numericReader(newMedianAggregator),
	types.AGG_VARIANCE:       numericReader(newVarianceAggregator),
	types.AGG_MODE:           newModeAggregator,
	types.AGG_SKEWNESS:       numericReader(newSkewnessAggregator),
	types.AGG_KURTOSIS:       numericReader(newKurtosisAggregator),
	types.AGG_DISTINCT_COUNT: newDistinctCountAggregator,
	types.AGG_PERCENTILE:     numericReader(newPercentileAggregator),
	types.AGG_NULL_COUNT:     newNullCountAggregator,
	types.AGG_WEIGHTED_MEAN:  numericReader(newWeightedMeanAggregator),
	types.AGG_RATIO:          newRatioAggregator,
	types.AGG_DISTINCT_SUM:   numericReader(newDistinctSumAggregator),
	types.AGG_CI_LOWER:       numericReader(newCIAggregator(ciLower)),
	types.AGG_CI_UPPER:       numericReader(newCIAggregator(ciUpper)),
	types.AGG_WELFORD:        newWelfordAggregator,
	types.AGG_MAPE:           newModelErrorAggregator(modelErrorMAPE),
	types.AGG_RMSE:           newModelErrorAggregator(modelErrorRMSE),
	types.AGG_MAE:            newModelErrorAggregator(modelErrorMAE),

	types.AGG_SET_UNION:           newSetUnionAggregator,
	types.AGG_SET_INTERSECTION:    newSetIntersectionAggregator,
	types.AGG_SET_FREQUENCY:       newSetFrequencyAggregator,
	types.AGG_SET_CARDINALITY_SUM: newSetCardinalitySumAggregator,
	types.AGG_SET_CARDINALITY_AVG: newSetCardinalityAvgAggregator,
	types.AGG_SET_DISTINCT_VALUES: newSetDistinctValuesAggregator,

	types.AGG_VEC_MEAN: newVecAggregator,
	types.AGG_VEC_SUM:  newVecAggregator,
	types.AGG_VEC_SD:   newVecAggregator,
}

// numericReader wraps the factory of an aggregator whose figure is
// arithmetic on its field's stored number with the accepted-types
// table's refusal (internal/fieldaccept): a categorical field (its
// dictionary index) or a set_* field (no number) is refused
// PULSE_FIELD_TYPE_UNSUPPORTED before the aggregator is built, on every
// path that builds one — buffered, streaming, grouped, crosstab cell and
// margins, shard partials — and predict reports the same refusal from
// the table. A field the schema does not carry (a derived column) and a
// nil schema (a registry probe) are not judged.
func numericReader(f AggregatorFactory) AggregatorFactory {
	return func(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
		if agg != nil {
			if cerr := fieldaccept.Check(string(agg.Type), fieldaccept.SlotField, agg.Field, schema); cerr != nil {
				return nil, cerr
			}
		}
		return f(agg, schema)
	}
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
	types.ATTR_MAHALANOBIS:  newMahalanobisAttribute,
	types.ATTR_PC_SCORE:     newPCScoreAttribute,
	types.ATTR_FACTOR_SCORE: newFactorScoreAttribute,
	types.ATTR_REG_PREDICT:  newRegPredictAttribute,

	types.ATTR_SET_POPCOUNT: newSetPopcountAttribute,
	types.ATTR_SET_HAS:      newSetHasAttribute,

	types.ATTR_CODE_IN: newCodeInAttribute,

	types.ATTR_SCALE_SCORE: newScaleScoreAttribute,

	types.ATTR_PROFILE_SIMILARITY: newComparisonAttribute,
	types.ATTR_VEC_DISTANCE:       newComparisonAttribute,
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
	types.GROUP_KMEANS:      newKMeansGrouper,
	types.GROUP_QUANTILE:    newQuantileGrouper,
	types.GROUP_RANGE:       newRangeGrouper,
	types.GROUP_ROUNDED:     newRoundedGrouper,

	types.GROUP_SET_VALUE:       newSetValueGrouper,
	types.GROUP_SET_PER_ELEMENT: newSetPerElementGrouper,
}
