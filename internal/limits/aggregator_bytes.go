package limits

import "github.com/frankbardon/pulse/types"

// Per-aggregator bucket charges (U51 E5-S7). A built-in aggregator's
// per-bucket state is charged its own measured ceiling from
// aggregatorBytesTable instead of the one-size aggStateBytes /
// weightedAggStateBytes, and its components-off finalize output its own
// output column; an aggregator the table does not hold (an embedder
// extension) keeps those ceilings. Every entry is a MEASURED CEILING,
// pinned by internal/service TestGroupMemory_AggregatorStateTable: the
// gate builds a grouped partition of each built-in exactly as the
// parallel reducers do (40,000 keys, 1 and 4 rows a key, weighted and
// not, components on and off), merges two of them and finalizes, and
// asserts each entry is at least the live heap the slot adds to a bucket
// — so a new built-in without an entry fails it.
//
// Figures (darwin/arm64, go1.27): a slot's state share is the bucket's
// live heap less the same case's with no aggregation (~145 B): the
// accumulator, its slot in the bucket and its floor tally (32 B; 160 B
// weighted). Each entry rounds the largest share it measured up by at
// least 9% (AGG_FREQUENCY weighted: 392 B against 432), and the widest
// bucket — every streaming built-in in one, weighted — holds 86% of its
// entries' sum.

// aggregatorBytes is one built-in aggregator's measured charges.
type aggregatorBytes struct {
	// state / weighted: per bucket per slot, unweighted and with a
	// weight in force (a weight-unaware aggregator measures the same).
	state, weighted int64
	// outputOff: per merged bucket per slot with components off — the
	// Data row's column (a map slot, paid at the row map's fill) and its
	// value.
	outputOff int64
	// buffered: no per-bucket accumulator while records fold (the
	// buffered arm keeps the records and aggregates each bucket at
	// finalize), so nothing to measure: the entry keeps the ceilings.
	buffered bool
}

// Per-slot components-off output: a scalar column costs a Data-row map
// slot and its boxed value — at most 91 B a column, measured just past
// the row map's growth points (14, 28, 56, 112 columns:
// TestGroupMemory_FinalizedRowBytes).
const aggOutputOffBytes = 96

var aggregatorBytesTable = map[types.AggregationType]aggregatorBytes{
	types.AGG_COUNT:               {state: 96, weighted: 576, outputOff: aggOutputOffBytes},
	types.AGG_SUM:                 {state: 128, weighted: 576, outputOff: aggOutputOffBytes},
	types.AGG_AVERAGE:             {state: 144, weighted: 576, outputOff: aggOutputOffBytes},
	types.AGG_MIN:                 {state: 96, weighted: 96, outputOff: aggOutputOffBytes},
	types.AGG_MAX:                 {state: 96, weighted: 96, outputOff: aggOutputOffBytes},
	types.AGG_STDDEV:              {state: 128, weighted: 576, outputOff: aggOutputOffBytes},
	types.AGG_RANGE:               {state: 128, weighted: 128, outputOff: aggOutputOffBytes},
	types.AGG_MODE_COUNT:          {state: 352, weighted: 544, outputOff: aggOutputOffBytes},
	types.AGG_VARIANCE:            {state: 128, weighted: 576, outputOff: aggOutputOffBytes},
	types.AGG_MODE:                {state: 352, weighted: 544, outputOff: aggOutputOffBytes},
	types.AGG_SKEWNESS:            {state: 144, weighted: 368, outputOff: aggOutputOffBytes},
	types.AGG_KURTOSIS:            {state: 160, weighted: 368, outputOff: aggOutputOffBytes},
	types.AGG_DISTINCT_COUNT:      {state: 320, weighted: 320, outputOff: aggOutputOffBytes},
	types.AGG_FREQUENCY:           {state: 160, weighted: 432, outputOff: aggOutputOffBytes},
	types.AGG_NULL_COUNT:          {state: 96, weighted: 96, outputOff: aggOutputOffBytes},
	types.AGG_WEIGHTED_MEAN:       {state: 576, weighted: 576, outputOff: aggOutputOffBytes},
	types.AGG_RATIO:               {state: 288, weighted: 464, outputOff: aggOutputOffBytes},
	types.AGG_DISTINCT_SUM:        {state: 448, weighted: 448, outputOff: aggOutputOffBytes},
	types.AGG_CI_LOWER:            {state: 256, weighted: 432, outputOff: aggOutputOffBytes},
	types.AGG_CI_UPPER:            {state: 256, weighted: 432, outputOff: aggOutputOffBytes},
	types.AGG_WELFORD:             {state: 128, weighted: 560, outputOff: 128},
	types.AGG_MAPE:                {state: 208, weighted: 384, outputOff: 480},
	types.AGG_RMSE:                {state: 208, weighted: 384, outputOff: 480},
	types.AGG_MAE:                 {state: 208, weighted: 384, outputOff: 480},
	types.AGG_SET_UNION:           {state: 176, weighted: 176, outputOff: 176},
	types.AGG_SET_INTERSECTION:    {state: 192, weighted: 192, outputOff: 176},
	types.AGG_SET_FREQUENCY:       {state: 192, weighted: 576, outputOff: aggOutputOffBytes},
	types.AGG_SET_CARDINALITY_SUM: {state: 96, weighted: 432, outputOff: aggOutputOffBytes},
	types.AGG_SET_CARDINALITY_AVG: {state: 128, weighted: 432, outputOff: aggOutputOffBytes},
	types.AGG_SET_DISTINCT_VALUES: {state: 592, weighted: 592, outputOff: aggOutputOffBytes},
	// Vector aggregators: the per-slot figures here are the fixed part
	// (its member and label lists, per-row scratch and the fragment map)
	// before the per-member slope vecStateMemberBytes; each merge-block
	// fragment the slot holds is charged apart (VectorAggregationBytes).
	types.AGG_VEC_MEAN: {state: 768, weighted: 960, outputOff: aggOutputOffBytes},
	types.AGG_VEC_SUM:  {state: 768, weighted: 960, outputOff: aggOutputOffBytes},
	types.AGG_VEC_SD:   {state: 768, weighted: 960, outputOff: aggOutputOffBytes},
	// Order statistics and AUC run on the buffered arm only.
	types.AGG_MEDIAN:     {state: aggStateBytes, weighted: weightedAggStateBytes, outputOff: aggOutputBytes, buffered: true},
	types.AGG_PERCENTILE: {state: aggStateBytes, weighted: weightedAggStateBytes, outputOff: aggOutputBytes, buffered: true},
	types.AGG_ZSCORE:     {state: aggStateBytes, weighted: weightedAggStateBytes, outputOff: aggOutputBytes, buffered: true},
	types.AGG_AUC:        {state: aggStateBytes, weighted: weightedAggStateBytes, outputOff: aggOutputBytes, buffered: true},
}

// Vector aggregator (AGG_VEC_*) slopes, per member (p): the fixed state
// grows by its member and label entries and row scratch; each merge
// block fragment holds a block of p sums, weights and counts (plus the
// mean / m2 / w2 moments for AGG_VEC_SD); a row a shard buffers in a
// non-head fragment keeps its p values, presence flags and weight.
// Measured at p = 3, 16 and 64.
const (
	vecStateMemberBytes          = 80
	vecFragmentBytes             = 288
	vecFragmentMemberBytes       = 32
	vecMomentFragmentMemberBytes = 64
	vecBufferedRowBytes          = 32
	vecBufferedRowMemberBytes    = 24
)

// AggregationMeasured reports whether t has a measured entry in the
// built-in table (false for an embedder extension, which is charged
// the aggStateBytes / weightedAggStateBytes ceilings), and whether the
// entry is a buffered-only aggregator that keeps those ceilings.
func AggregationMeasured(t types.AggregationType) (measured, buffered bool) {
	b, ok := aggregatorBytesTable[t]
	return ok, b.buffered
}

// AggregationStateBytes is one aggregation slot's per-bucket state:
// its built-in entry (weighted when a weight is in force on the slot),
// plus members x vecStateMemberBytes for a vector aggregator over
// members members; aggStateBytes / weightedAggStateBytes for a type
// the table does not hold.
func AggregationStateBytes(t types.AggregationType, weighted bool, members int64) int64 {
	b, ok := aggregatorBytesTable[t]
	if !ok {
		if weighted {
			return weightedAggStateBytes
		}
		return aggStateBytes
	}
	n := b.state
	if weighted {
		n = b.weighted
	}
	if isVectorAggregation(t) {
		n = addSat(n, mulSat(max(members, 0), vecStateMemberBytes))
	}
	return n
}

// AggregationOutputOffBytes is one aggregation slot's per-bucket
// finalize output with components off: its built-in entry, plus 16
// bytes a member for a vector aggregator's array; aggOutputBytes (the
// components-on figure) for a type the table does not hold — an
// extension's value is opaque.
func AggregationOutputOffBytes(t types.AggregationType, members int64) int64 {
	b, ok := aggregatorBytesTable[t]
	if !ok {
		return aggOutputBytes
	}
	n := b.outputOff
	if isVectorAggregation(t) {
		n = addSat(n, mulSat(max(members, 0), 16))
	}
	return n
}

// VectorAggregationBytes is a vector aggregator slot's merge-block
// state beyond its per-bucket fixed state: fragments block partials of
// members elements each (AGG_VEC_SD adds the moments), and bufferedRows
// rows held in non-head fragments (a shard starting inside a block).
// fragments is bounded by min(merge blocks x buckets, rows x fan-out):
// every fragment holds at least one row. 0 for any other type.
func VectorAggregationBytes(t types.AggregationType, members, fragments, bufferedRows int64) int64 {
	if !isVectorAggregation(t) {
		return 0
	}
	members = max(members, 0)
	slope := int64(vecFragmentMemberBytes)
	if t == types.AGG_VEC_SD {
		slope = vecMomentFragmentMemberBytes
	}
	frag := addSat(vecFragmentBytes, mulSat(members, slope))
	row := addSat(vecBufferedRowBytes, mulSat(members, vecBufferedRowMemberBytes))
	return addSat(mulSat(max(fragments, 0), frag), mulSat(max(bufferedRows, 0), row))
}

func isVectorAggregation(t types.AggregationType) bool {
	return t == types.AGG_VEC_MEAN || t == types.AGG_VEC_SUM || t == types.AGG_VEC_SD
}
