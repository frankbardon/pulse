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
// parallel reducers do (5,000 keys, 1 and 4 rows a key, weighted and
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
	// cell: one crosstab cell's (or margin slot's) finalize charge for
	// this measure beside its state (U51 E5-S9) — CrosstabSlotBytes.
	cell cellBytes
}

// cellBytes is a crosstab measure's per-cell finalize charge: the
// matrix value, the cell's components entry (its floor and operator
// map) and the finalize's per-cell scratch (the value, presence and
// count maps keyed by the cell, alive while the matrix is laid out) —
// on with components on (onWeighted under a weight: the weighted floor
// keys), off with them off (the value and the scratch). Measured as the
// peak live heap a cell adds beyond its folded state, every cell
// populated, on a grid just past a map growth point (the per-cell
// finalize maps at their sparsest), and set at least 20% above the
// largest sampled peak (15% for a buffered-only measure) — the sampling
// varies ~8% run to run (internal/service
// TestCrosstabMemory_CellChargeTable).
type cellBytes struct {
	on, onWeighted, off int64
}

// Per-slot components-off output: a scalar column costs a Data-row map
// slot and its boxed value — at most 91 B a column, measured just past
// the row map's growth points (14, 28, 56, 112 columns:
// TestGroupMemory_FinalizedRowBytes).
const aggOutputOffBytes = 96

var aggregatorBytesTable = map[types.AggregationType]aggregatorBytes{
	types.AGG_COUNT:               {state: 96, weighted: 576, outputOff: aggOutputOffBytes, cell: cellBytes{976, 1104, 512}},
	types.AGG_SUM:                 {state: 128, weighted: 576, outputOff: aggOutputOffBytes, cell: cellBytes{1088, 1200, 512}},
	types.AGG_AVERAGE:             {state: 144, weighted: 576, outputOff: aggOutputOffBytes, cell: cellBytes{1088, 1664, 512}},
	types.AGG_MIN:                 {state: 96, weighted: 96, outputOff: aggOutputOffBytes, cell: cellBytes{848, 880, 352}},
	types.AGG_MAX:                 {state: 96, weighted: 96, outputOff: aggOutputOffBytes, cell: cellBytes{848, 912, 352}},
	types.AGG_STDDEV:              {state: 128, weighted: 576, outputOff: aggOutputOffBytes, cell: cellBytes{976, 1248, 352}},
	types.AGG_RANGE:               {state: 128, weighted: 128, outputOff: aggOutputOffBytes, cell: cellBytes{848, 848, 352}},
	types.AGG_MODE_COUNT:          {state: 352, weighted: 544, outputOff: aggOutputOffBytes, cell: cellBytes{832, 912, 384}},
	types.AGG_VARIANCE:            {state: 128, weighted: 576, outputOff: aggOutputOffBytes, cell: cellBytes{848, 880, 352}},
	types.AGG_MODE:                {state: 352, weighted: 544, outputOff: aggOutputOffBytes, cell: cellBytes{848, 1232, 352}},
	types.AGG_SKEWNESS:            {state: 144, weighted: 368, outputOff: aggOutputOffBytes, cell: cellBytes{848, 1232, 352}},
	types.AGG_KURTOSIS:            {state: 160, weighted: 368, outputOff: aggOutputOffBytes, cell: cellBytes{864, 1248, 352}},
	types.AGG_DISTINCT_COUNT:      {state: 320, weighted: 320, outputOff: aggOutputOffBytes, cell: cellBytes{832, 832, 320}},
	types.AGG_FREQUENCY:           {state: 160, weighted: 432, outputOff: aggOutputOffBytes, cell: cellBytes{944, 1072, 400}},
	types.AGG_NULL_COUNT:          {state: 96, weighted: 96, outputOff: aggOutputOffBytes, cell: cellBytes{960, 976, 400}},
	types.AGG_WEIGHTED_MEAN:       {state: 576, weighted: 576, outputOff: aggOutputOffBytes, cell: cellBytes{1648, 1648, 512}},
	types.AGG_RATIO:               {state: 288, weighted: 464, outputOff: aggOutputOffBytes, cell: cellBytes{1120, 1232, 512}},
	types.AGG_DISTINCT_SUM:        {state: 448, weighted: 448, outputOff: aggOutputOffBytes, cell: cellBytes{752, 752, 320}},
	types.AGG_CI_LOWER:            {state: 256, weighted: 432, outputOff: aggOutputOffBytes, cell: cellBytes{864, 1248, 352}},
	types.AGG_CI_UPPER:            {state: 256, weighted: 432, outputOff: aggOutputOffBytes, cell: cellBytes{864, 1248, 352}},
	types.AGG_WELFORD:             {state: 128, weighted: 560, outputOff: 128, cell: cellBytes{864, 1248, 352}},
	types.AGG_MAPE:                {state: 208, weighted: 384, outputOff: 480, cell: cellBytes{848, 1232, 352}},
	types.AGG_RMSE:                {state: 208, weighted: 384, outputOff: 480, cell: cellBytes{864, 1248, 352}},
	types.AGG_MAE:                 {state: 208, weighted: 384, outputOff: 480, cell: cellBytes{848, 1232, 352}},
	types.AGG_SET_UNION:           {state: 176, weighted: 176, outputOff: 176, cell: cellBytes{1280, 1296, 496}},
	types.AGG_SET_INTERSECTION:    {state: 192, weighted: 192, outputOff: 176, cell: cellBytes{1040, 1040, 416}},
	types.AGG_SET_FREQUENCY:       {state: 192, weighted: 576, outputOff: aggOutputOffBytes, cell: cellBytes{848, 1008, 352}},
	types.AGG_SET_CARDINALITY_SUM: {state: 96, weighted: 432, outputOff: aggOutputOffBytes, cell: cellBytes{1056, 1184, 496}},
	types.AGG_SET_CARDINALITY_AVG: {state: 128, weighted: 432, outputOff: aggOutputOffBytes, cell: cellBytes{1072, 1200, 496}},
	types.AGG_SET_DISTINCT_VALUES: {state: 592, weighted: 592, outputOff: aggOutputOffBytes, cell: cellBytes{1168, 1168, 368}},
	// Vector aggregators: the per-slot figures here are the fixed part
	// (its member and label lists, per-row scratch and the fragment map)
	// before the per-member slope vecStateMemberBytes; each merge-block
	// fragment the slot holds is charged apart (VectorAggregationBytes).
	types.AGG_VEC_MEAN: {state: 768, weighted: 960, outputOff: aggOutputOffBytes, cell: cellBytes{1280, 1792, 960}},
	types.AGG_VEC_SUM:  {state: 768, weighted: 960, outputOff: aggOutputOffBytes, cell: cellBytes{1280, 1792, 960}},
	types.AGG_VEC_SD:   {state: 768, weighted: 960, outputOff: aggOutputOffBytes, cell: cellBytes{1008, 1456, 400}},
	// Order statistics and AUC run on the buffered arm only.
	types.AGG_MEDIAN:     {state: aggStateBytes, weighted: weightedAggStateBytes, outputOff: aggOutputBytes, buffered: true, cell: cellBytes{848, 864, 352}},
	types.AGG_PERCENTILE: {state: aggStateBytes, weighted: weightedAggStateBytes, outputOff: aggOutputBytes, buffered: true, cell: cellBytes{880, 1248, 352}},
	types.AGG_ZSCORE:     {state: aggStateBytes, weighted: weightedAggStateBytes, outputOff: aggOutputBytes, buffered: true, cell: cellBytes{864, 864, 352}},
	types.AGG_AUC:        {state: aggStateBytes, weighted: weightedAggStateBytes, outputOff: aggOutputBytes, buffered: true, cell: cellBytes{1232, 1872, 352}},
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

// Crosstab charges (U51 E5-S9). Every crosstab cell — and every margin
// slot — holds one accumulator per measure (crosstab.cell, or each
// crosstab.cells entry: a multi-measure crosstab runs one grid per
// measure over the same records) and adds its finalize output; each
// axis key is interned once per measure. A margin slot also holds each
// margin_aggregations entry, with components on (the auxiliary figures
// live only in the components). Pinned by internal/service
// TestCrosstabMemory_CellChargeTable on both crosstab arms.
const (
	// crosstabAxisKeyBytes / crosstabAxisKeyOffBytes per interned axis
	// key per measure, components on / off: the interner's map entry,
	// key string and tuple, and the key's emitted header, long row and
	// (on) its axis-key components entry — measured up to 626 B on /
	// 448 B off a key on a 4,000 x 1 grid (just past a map growth
	// point: the interner and finalize maps at their sparsest), set 20%
	// over.
	crosstabAxisKeyBytes    = 784
	crosstabAxisKeyOffBytes = 544
	// crosstabAuxSlotBytes per margin_aggregations entry per margin slot
	// beyond its accumulator and cell-sized output: the slot's struct
	// (the accumulator, its {n, n_null} counters and weighted floor) and
	// the MarginAggregationFigure it emits.
	crosstabAuxSlotBytes = 256
	// crosstabCellOutputCeilingBytes: an aggregator the table does not
	// hold (an embedder extension) is charged this per cell for its
	// output, components on or off — its value is opaque. At least every
	// built-in entry (the widest: AGG_AUC weighted, 1,872 B).
	crosstabCellOutputCeilingBytes = 2048
	// The buffered crosstab's partitions, beside its cells' charge
	// (measured on the buffered arm, components on and off):
	// crosstabBufferedRecordBytes per materialised row (x fan-out) — its
	// slots in the filtered set and the row / column / per-row column
	// partitions, and the own-margin membership sets (~115 B a row at
	// 400 rows a cell); crosstabBufferedCellBytes per cell per measure —
	// its per-row column partition entry, record list and the measure
	// run's per-cell scratch (~410 B a cell a measure at one row a
	// cell); crosstabBufferedAxisKeyBytes per axis key per measure — its
	// partition (~325 B a row key beyond the fused arm's figure).
	crosstabBufferedRecordBytes  = 128
	crosstabBufferedCellBytes    = 448
	crosstabBufferedAxisKeyBytes = 416
)

// CrosstabSlotBytes is one crosstab cell's — or one margin slot's —
// charge for one measure of type t: its state (AggregationStateBytes,
// weighted when a weight is in force on the slot) plus its finalize
// output (the table's cell entry: components on, weighted or not, or
// off), and per set member an AGG_SET_FREQUENCY slot counts
// setMemberStateBytes + setMemberOutputBytes. An extension type is
// charged the state ceilings and crosstabCellOutputCeilingBytes.
// members is the vector member count a vector slot reads (1 for an
// element-selecting slot: the only one a crosstab admits).
func CrosstabSlotBytes(t types.AggregationType, weighted, componentsOff bool, members, setMembers int64) int64 {
	n := AggregationStateBytes(t, weighted, members)
	out := int64(crosstabCellOutputCeilingBytes)
	if b, ok := aggregatorBytesTable[t]; ok {
		switch {
		case componentsOff:
			out = b.cell.off
		case weighted:
			out = b.cell.onWeighted
		default:
			out = b.cell.on
		}
	}
	n = addSat(n, out)
	return addSat(n, mulSat(max(setMembers, 0), setMemberStateBytes+setMemberOutputBytes))
}

// CrosstabAuxSlotBytes is one margin_aggregations entry of type t in
// one margin slot: a cell-sized charge (CrosstabSlotBytes, components
// on) plus crosstabAuxSlotBytes. 0 with components off — the auxiliary
// figures live in the components only, and neither arm accumulates
// them then.
func CrosstabAuxSlotBytes(t types.AggregationType, weighted, componentsOff bool, members, setMembers int64) int64 {
	if componentsOff {
		return 0
	}
	return addSat(CrosstabSlotBytes(t, weighted, false, members, setMembers), crosstabAuxSlotBytes)
}

// CrosstabAxisKeyBytes is one interned crosstab axis key's charge per
// measure, components on or off.
func CrosstabAxisKeyBytes(componentsOff bool) int64 {
	if componentsOff {
		return crosstabAxisKeyOffBytes
	}
	return crosstabAxisKeyBytes
}

// The crosstab's dense grid (U51 E5-S11). Both arms emit a
// rows x cols matrix per measure — every position, empty or not, holds
// a MatrixCell and, components on, a CellCounts int and a
// CellComponents map pointer — and the fused arm also folds into a
// dense grid: per position a cell-accumulator interface, its count and
// null count, and (weighted) its weight-floor pointer, each row grown
// by append. A populated cell's position is inside its CrosstabSlotBytes
// (measured on fully populated grids), so the grid term charges only
// the positions past the populated-cell bound. Pinned against the Go
// layout by internal/service TestCrosstabMemory_GridPositionBytes and
// against the heap by TestCrosstabMemory_CellChargeTable's sparse cases.
const (
	// crosstabGridMatrixCellBytes: types.MatrixCell (an interface value
	// and a bool, padded).
	crosstabGridMatrixCellBytes = 24
	// crosstabGridComponentsBytes: a CellCounts int and a CellComponents
	// map pointer, components on.
	crosstabGridComponentsBytes = 16
	// crosstabGridFoldBytes: the fused fold's per-position slots — the
	// cell accumulator interface (16), its count (8) and null count (8);
	// crosstabGridWeightBytes adds the weight-floor pointer when a
	// weight is in force on the cell.
	crosstabGridFoldBytes   = 32
	crosstabGridWeightBytes = 8
	// crosstabGridGrowth bounds the fused rows' append slack: a row
	// grown one column at a time holds at most twice its length.
	crosstabGridGrowth = 2
	// crosstabGridHeadroomNum / Den: the figure is set 25% over the
	// layout — allocator size-class rounding (up to 12.5%) and the
	// sampled-peak gate's run-to-run variance.
	crosstabGridHeadroomNum = 5
	crosstabGridHeadroomDen = 4
)

// CrosstabGridPositionBytes is one empty dense-grid position's charge
// per measure (U51 E5-S11): the emitted matrix's MatrixCell, plus a
// CellCounts / CellComponents entry with components on, plus on the
// fused arm the fold's slots at their append-growth bound (with the
// weight-floor pointer when weighted) — set crosstabGridHeadroom over
// the layout. Fused: 130 B on, 110 B off (150 / 130 weighted);
// buffered: 50 B on, 30 B off.
func CrosstabGridPositionBytes(fused, weighted, componentsOff bool) int64 {
	n := int64(crosstabGridMatrixCellBytes)
	if !componentsOff {
		n += crosstabGridComponentsBytes
	}
	if fused {
		fold := int64(crosstabGridFoldBytes)
		if weighted {
			fold += crosstabGridWeightBytes
		}
		n += crosstabGridGrowth * fold
	}
	return (n*crosstabGridHeadroomNum + crosstabGridHeadroomDen - 1) / crosstabGridHeadroomDen
}
