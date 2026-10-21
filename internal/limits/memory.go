package limits

import "github.com/frankbardon/pulse/encoding"

// The memory model behind MaxEstimatedMemory. EstimateMemory is the
// one formula predict (internal/descriptor.RequestLimitFindings) and
// the process pre-flight both evaluate, so a breach is refused with
// identical details on both sides. Every term is an UPPER bound — the
// model cannot see filter selectivity, field projection or which
// dictionary entries the data actually carries — calibrated against
// the measured peak heap (runtime.MemStats.HeapAlloc high-water mark,
// garbage between GC cycles included) of buildWideCohort runs: see
// internal/service TestMemoryEstimate_Calibration and the "Resource
// limits" paragraph of .claude/reference/predict-inspect.md for the
// figures.

// MemoryArm is the execution arm a request runs on, which decides the
// shape of its resident state.
type MemoryArm string

const (
	// ArmBuffered materialises every record before processing
	// (non-streamable Process, the buffered crosstab): O(records).
	ArmBuffered MemoryArm = "buffered"
	// ArmStreaming folds records into per-bucket state as they decode:
	// O(buckets).
	ArmStreaming MemoryArm = "streaming"
	// ArmFusedCrosstab folds records into the crosstab grid in the
	// decode loop: O(cells).
	ArmFusedCrosstab MemoryArm = "fused_crosstab"
)

// MergeBlockSize mirrors linalg.MergeBlockSize (the blocked-merge
// block length in records; a run holds one CoMoment per populated
// block per bucket). The leaf may not import linalg, so the value is
// restated here and pinned equal by TestMergeBlockSizeMatchesLinalg.
const MergeBlockSize = 4096

// Calibrated model constants, in bytes. Each is rounded UP from the
// figure it models so the sum stays an upper bound.
const (
	// runBaseBytes covers the per-run fixed cost — the parsed schema and
	// dictionaries, iterator and decode buffers, the response — and the
	// garbage collector's overshoot past its heap goal.
	runBaseBytes = 16 << 20
	// fileCopyFactor x stride per record: the cohort payload and the
	// collector's headroom over it. A single file on a filesystem the
	// decoder cannot mmap is read whole onto the heap (afero.ReadFile),
	// and the per-record garbage a scan produces (a joined record per
	// row, decode scratch) lets the heap run to the collector's goal —
	// twice the live heap under the default GOGC=100 — and past it
	// while a concurrent mark is in flight: measured up to 2.9x the
	// payload (a joined stream). A shard archive is never read whole
	// (U51): its shards stream through a bounded window one at a time,
	// so the factor applies to its LARGEST shard's payload
	// (MaxShardRecords), never to the archive's bytes.
	fileCopyFactor = 3
	// directoryCopyFactor x a shard archive's central-directory bytes:
	// the parsed directory a scan holds (a zip.File per entry with its
	// name, the entry table and the by-name map) plus the cohort's shard
	// list — measured ~6x the raw directory bytes (short shard names,
	// the fixed per-entry cost dominating), rounded up
	// (TestArchiveMemory_DirectoryTermBoundsOpen, internal/service).
	directoryCopyFactor = 8
	// buildCopyFactor x record bytes per join build-side row: the
	// materialised right record plus the per-row copy garbage its build
	// churns (the value maps it is copied out of).
	buildCopyFactor = 2
	// recordHeaderBytes: one buffered *Record — the struct (size class
	// 112), its []*Record slot with append growth (24), the bit planes'
	// slice header share.
	recordHeaderBytes = 160
	// slotBytes per storage slot (a float64 value, a set-mask or
	// group-index word) plus 25% allocator size-class slack.
	slotBytes = 10
	// planeBytes per 64 slots: the three presence / null / wide bit
	// planes, one word each.
	planeBytes = 24
	// auxBytes + decimalBytes per decimal128 field: the sparse aux
	// block a record allocates once it carries a wide decimal.
	auxBytes     = 96
	decimalBytes = 16
	// derivedColumnBytes per record per attribute / feature / window
	// output on the buffered arm: an off-schema overflow map entry.
	derivedColumnBytes = 128
	// bufferedValueBytes per record per aggregation on the buffered
	// arm: the value copy an order statistic sorts.
	bufferedValueBytes = 16
	// A group bucket is charged in two halves (U51 E2-S4, #358), both
	// measured at 800,000 one-row groups (darwin/arm64, go1.26; live heap
	// after GC for the state, the peak of a whole Process for the
	// output; internal/service TestGroupMemory_*). The STATE is what a
	// bucket holds while records fold — on every grouped arm, and in
	// every per-shard partition (PartitionBuckets) — and the OUTPUT what
	// finalize adds per merged bucket: the Data row, the grouper's
	// components bucket entry and the groups[] Components entry.
	//
	// bucketBaseBytes: a bucket's resident key state — its map slot (an
	// 8-byte *GroupBucket value, paid at the table's 7/16..7/8 fill),
	// the GroupBucket, the key string and the grouper's live components
	// entry: measured 126-215 B across 300k-1.3M keys.
	bucketBaseBytes = 256
	// extraGrouperStateBytes per bucket per grouper past the first: the
	// composite key's longer string and its per-grouper tuple entry —
	// measured ~105 B a grouper (three groupers 355 B a bucket against
	// one grouper's 145 B; U51 E5-S7, TestGroupMemory_FinalizedRowBytes).
	extraGrouperStateBytes = 128
	// bucketOutputBytes: a merged bucket's finalize output beside its
	// aggregations' — the Data row map, its key column, the grouper's
	// components bucket and the groups[] entry's key: measured 744 B
	// retained, 1,080 B at the run's peak with no aggregation.
	bucketOutputBytes = 1024
	// bucketOutputOffBytes: the same with components off (U51 E5-S7) —
	// the Data row map and its key column alone: measured 344-416 B
	// retained up to seven columns (the row map's first group), one to
	// three groupers (TestGroupMemory_FinalizedRowBytes).
	bucketOutputOffBytes = 512
	// aggStateBytes per bucket per unweighted aggregation the built-in
	// table (aggregatorBytesTable) does not hold — an embedder
	// extension's: one accumulator, its slot in the bucket and its floor
	// tally. The built-ins measure 80-534 B (the table).
	aggStateBytes = 256
	// weightedAggStateBytes per bucket per WEIGHTED aggregation the
	// table does not hold: the shared weighted accumulator (three binned
	// sums and the Welford moments, 320 B), its slot, and its floor with
	// the 128-byte weighted floor tally: 496-518 B measured on the
	// built-ins.
	weightedAggStateBytes = 512
	// aggOutputBytes per merged bucket per aggregation with components
	// on: the row's value and the groups[] Components entry with its
	// operator map — measured 539 B a column just past the row map's
	// growth points (28, 56, 112 AGG_MAX columns), at most 464 B a
	// column below them. U51 E5-S7 raised it from 512, which those
	// widths exceeded (the over-charged state had hidden it).
	aggOutputBytes = 576
	// setMemberStateBytes / setMemberOutputBytes per bucket per member
	// of the set field an AGG_SET_FREQUENCY slot counts: a per-member
	// counter (8 B; weighted a binned Σw, 56 B) and, at finalize, the
	// label map the frozen result and the Data row each carry —
	// measured 121 B (unweighted) / 170 B (weighted) a member at the
	// peak over a set_u256 field with every member selected.
	setMemberStateBytes  = 64
	setMemberOutputBytes = 128
	// valueEntryBytes per distinct value an aggregator keeps
	// (distinct / mode / frequency families): a map entry. The distinct
	// (bucket, value) pairs of one aggregation never exceed the record
	// count, so the term is records x valueEntryBytes per such slot.
	valueEntryBytes = 96
	// dictEntryDecodeBytes per dictionary entry a schema decode
	// allocates beyond its label: the []string slot and the presized
	// lookup-map entry with its slack (measured ~100 a entry).
	dictEntryDecodeBytes = 128
	// dictBufferBytesPerEntry: encoding.Dictionary.ReadFrom's shared
	// label buffer starts at this many bytes a entry (min 256) and
	// doubles until the labels fit — DictionaryDecodeBytes replays that
	// growth.
	dictBufferBytesPerEntry = 64
	// fieldDecodeBytes per field descriptor a schema decode allocates:
	// the Field, its name and description.
	fieldDecodeBytes = 256
	// fanOutSlotBytes per record per EXTRA bucket a fan-out grouper
	// (GROUP_SET_PER_ELEMENT) adds it to on the buffered arm: one more
	// []*Record slot with append growth — the slot recordHeaderBytes
	// charges for the first bucket.
	fanOutSlotBytes = 24
	// testStateBytes per tier-1 test instance (one per test per group
	// bucket; one ungrouped): the instance, its moments or counters and
	// its result.
	testStateBytes = 1024
	// testLevelBytes per split level / contingency cell a test instance
	// keys (split_by, rows x cols, subject_field): a map entry and the
	// per-level state (moments or a rank sample's header).
	testLevelBytes = 256
	// testValueBytes per value a buffered (rank-based / order) test
	// keeps per admitted row and column (value, second field, weight,
	// vector member): the 8-byte value, append growth and the copy its
	// Finalize sorts.
	testValueBytes = 32
)

// ValueEntryBytes is the per-value-state charge a value-state
// aggregation pays per record it can see (valueEntryBytes): the
// per-aggregator gates add it to a value-state slot's bucket figures.
const ValueEntryBytes = valueEntryBytes

// ShardMergeWindow is how many shards the per-shard parallel reducer
// keeps dispatched but not yet merged on workers goroutines: shard j is
// handed out only once j < next + window, where next is the first shard
// the incremental merge has not folded (internal/service
// scanAndMergeShards). The window bounds the live partials — at most
// window of them beside the merged state, whatever the shard count —
// and predict charges exactly that many per-shard partitions
// (descriptor EstimateMemory). Twice the workers leaves a finished
// worker a shard to take while an earlier, slower shard holds the merge
// back. At least 1.
func ShardMergeWindow(workers int) int {
	return max(2*workers, 1)
}

// ShardMergeWindowWithin is ShardMergeWindow(workers) shrunk to fit a
// memory budget (U51 E5-S4): the largest window w in
// [1, ShardMergeWindow(workers)] whose run estimate (estimate(w): the
// whole-run EstimateMemory with w shards' partials alive) is at most
// budget. The full window when the budget is Unlimited (MaxEstimatedMemory
// unset), when the estimate is unknown, or when it already fits; 1 when
// even one live partial does not fit (the pre-flight then refuses with
// that smallest estimate). The window bounds only how many shards are
// in flight — the merge order, and so every answer, is the same at any
// window. predict (descriptor ShardMergeWindow) and the runtime
// (scanAndMergeShards) both read it through the same estimate.
func ShardMergeWindowWithin(workers int, budget int64, estimate func(window int) (int64, bool)) int {
	full := ShardMergeWindow(workers)
	if IsUnlimited(budget) || estimate == nil {
		return full
	}
	for w := full; w > 1; w-- {
		if n, ok := estimate(w); !ok || n <= budget {
			return w
		}
	}
	return 1
}

// ParallelDecodeRecordThreshold is the smallest single-file record
// count the parallel-decode reducer fans out over (Options.
// DecodeWorkers): below it the engine stays serial whatever the worker
// cap. The runtime gate (internal/service) and predict's twin
// (descriptor DecodeFanOut) both read it.
const ParallelDecodeRecordThreshold = 100_000

// MemoryInputs are the figures EstimateMemory reads. Counts are
// non-negative; a figure the caller cannot know makes the estimate
// unknowable, and the caller reports no finding instead of guessing.
type MemoryInputs struct {
	// Arm is the execution arm the request runs on.
	Arm MemoryArm
	// Records is the record count the arm reads (a join's left side).
	Records int64
	// Stride is the schema's physical record stride
	// (encoding.Schema.RecordByteSize) — the payload bytes per record.
	Stride int64
	// RecordBytes is the resident size of one buffered record over the
	// schema (RecordBytes).
	RecordBytes int64
	// Buckets is the group-bucket bound (1 when ungrouped).
	Buckets int64
	// Aggregations is the number of aggregator slots (the crosstab
	// cell counts as one).
	Aggregations int64
	// AggStateBytes is, summed over those slots, each slot's per-bucket
	// state (AggregationStateBytes: a built-in's measured entry, its
	// weighted one when a weight is in force on the slot — its own, else
	// the request's, else the instance default; an extension's ceiling).
	AggStateBytes int64
	// ComponentsOff reports the request runs with components disabled
	// (the request's disable_components, else Options.DisableComponents):
	// a merged bucket's finalize output is then bucketOutputOffBytes +
	// AggOutputOffBytes, never the components-on figure.
	ComponentsOff bool
	// AggOutputOffBytes is, summed over the slots, each slot's
	// components-off output (AggregationOutputOffBytes).
	AggOutputOffBytes int64
	// ExtraGroupers is the groupers past the first (a composite key):
	// each adds extraGrouperStateBytes to a bucket's state.
	ExtraGroupers int64
	// VectorBytes is the vector aggregators' merge-block fragments and
	// buffered rows (VectorAggregationBytes, summed over the slots),
	// charged once on every arm.
	VectorBytes int64
	// SetMembers is, summed over the AGG_SET_FREQUENCY slots, the member
	// count of the set field each counts (its dictionary size, capped at
	// the set address space): per bucket, a counter and an output label
	// per member.
	SetMembers int64
	// ValueStateAggregations is how many of them keep per-value state
	// (distinct / mode / frequency families).
	ValueStateAggregations int64
	// DerivedColumns is the number of attribute, feature and window
	// outputs added to every record.
	DerivedColumns int64
	// Cells is the crosstab grid bound (0 without a crosstab).
	Cells int64
	// CellBytes is one crosstab cell's charge, summed over the measures
	// (CrosstabSlotBytes: each measure's state and finalize output) —
	// U51 E5-S9; it replaced a flat 512 B a cell.
	CellBytes int64
	// AxisKeys is the crosstab's axis-key bound, rows + columns; each
	// costs AxisKeyBytes (CrosstabAxisKeyBytes a measure).
	AxisKeys     int64
	AxisKeyBytes int64
	// MarginSlots is the crosstab's margin slots — a row slot per row
	// key with row margins, a column slot per column key with column
	// margins, one grand slot — plus the normalize denominators (cells
	// under normalize_within, rows + columns under normalize_level);
	// each costs MarginSlotBytes (the measures' CrosstabSlotBytes).
	MarginSlots     int64
	MarginSlotBytes int64
	// GridPositions is the crosstab's dense-grid positions past the
	// populated-cell bound (U51 E5-S11): rows bound x cols bound, capped
	// by MaxCrosstabCells, less Cells — the empty positions both arms'
	// rows x cols matrices hold; each costs GridPositionBytes (the
	// measures' CrosstabGridPositionBytes on the run's arm).
	GridPositions     int64
	GridPositionBytes int64
	// CrosstabMeasures is the crosstab's measure count (crosstab.cell
	// is one, else len(crosstab.cells)): the buffered arm lays out its
	// cell and axis partitions once per measure.
	CrosstabMeasures int64
	// AuxSlots is the margin slots the margin_aggregations entries fill
	// (row, column and grand, as emitted); each costs AuxSlotBytes (the
	// entries' CrosstabAuxSlotBytes, once per measure).
	AuxSlots     int64
	AuxSlotBytes int64
	// JoinRightRows is a join build side's record count (0 without a
	// join); each is a materialised record of RecordBytes plus its
	// payload.
	JoinRightRows int64
	// MatrixBytes is the matrices' merge-block state (MatrixStateBytes,
	// summed over every matrix) plus a buffered matrix's row store
	// (MatrixRowBufferBytes).
	MatrixBytes int64
	// GrouperBytes is a buffered vector grouper's point store
	// (KMeansBufferBytes, GROUP_KMEANS) — 0 without one.
	GrouperBytes int64
	// MaxShardRecords is a shard archive's largest per-shard record
	// count; 0 for a single file or an anchor-opened shard. Open reads
	// only the central directory and each shard's header, and the scan
	// streams one shard at a time through a bounded read window (U51
	// E1-S1) — the archive is never resident — so the payload term
	// charges this one shard's records x stride in place of Records:
	// the per-shard working set and the collector's headroom over it.
	MaxShardRecords int64
	// DirectoryBytes is a shard archive's central-directory bytes
	// (encoding Archive.DirectoryBytes: the tail past the last entry's
	// data); 0 for a single file or an anchor-opened shard. The scan
	// holds the parsed directory, charged directoryCopyFactor a byte.
	DirectoryBytes int64
	// SchemaDecodes is how many schema blocks the run decodes, with
	// their dictionaries, beyond the cohort's own (an archive: 1, the
	// canonical entry — Open's record-count peek and the scan read
	// every shard's layout-only and allocate no dictionary); 0 for a
	// single file or an anchor-opened shard. Each costs SchemaBytes.
	SchemaDecodes int64
	// SchemaBytes is what one decode of the cohort's schema allocates
	// (SchemaDecodeBytes).
	SchemaBytes int64
	// PartitionBuckets is the bucket states the parallel reducers'
	// partials hold beside the merged state: on the per-shard arm the
	// sum of the shard bucket bounds (1 per shard ungrouped) over the
	// merge window's LARGEST shards (ShardMergeWindow, shrunk by
	// ShardMergeWindowWithin under a set MaxEstimatedMemory) — the merge folds each
	// shard as soon as every earlier one is done and releases it, and
	// dispatches no shard past the window (U51 E2-S4) — and on the
	// parallel-decode arm one partition per worker; 0 off both. Each
	// costs a bucket's STATE (no output: a partition never finalizes).
	PartitionBuckets int64
	// PartitionRecords is the records whose per-value state those
	// partials hold beside the merged state (the window's shards'
	// records on the per-shard arm, the run's records on the decode
	// arm, else 0): charged per value-state aggregation like Records.
	PartitionRecords int64
	// FanOut is the most group buckets one record lands in
	// (FanOutMultiplicity: the product of every fan-out grouper's key
	// bound, GROUP_SET_PER_ELEMENT's set dictionary size); 0 or 1 when
	// no grouper fans out. It scales the per-value state (distinct
	// (bucket, value) pairs can reach records x FanOut) and, on the
	// buffered arm, the partition slots of the extra buckets.
	FanOut int64
	// TestInstances is the tier-1 test instances the run holds until
	// finalize: tests x group buckets (every test runs once per group),
	// tests ungrouped.
	TestInstances int64
	// TestLevels is the split levels / contingency cells the test
	// instances key, summed over tests — per test the smaller of its
	// instances x schema-known levels and the rows it admits (records x
	// FanOut).
	TestLevels int64
	// TestValues is the values buffered (non-streamable) tests keep:
	// per such test records x FanOut x its value columns.
	TestValues int64
	// JoinedRows is the rows a join's probe emits into the processor
	// when they can outnumber the left side (a right key that repeats:
	// left x the right key's multiplicity); 0 means Records. It replaces
	// Records in every per-row processing term — per-value state, the
	// buffered arm's materialised records and fan-out slots — while the
	// payload term stays on Records (the left scan).
	JoinedRows int64
}

// EstimateMemory is the upper-bound resident footprint of one run, in
// bytes, per arm, where rows is JoinedRows when set (a fan-out join),
// else records:
//
//	every arm  base + 3 x payload records x stride
//	             (the payload + GC headroom; payload records are the
//	             largest shard's on an archive, else records)
//	           + 8 x directory bytes + schema decodes x schema bytes,
//	             on an archive
//	           + join right rows x (3 x stride + 2 x record bytes)
//	           + matrix merge-block state + vector-grouper point store
//	           + vector aggregators' merge-block fragments
//	           + partition buckets x state, parallel arms
//	           + test instances x 1024 + test levels x 256
//	           + buffered-test values x 32
//	buffered   + rows x (record bytes + 16 x aggs + 128 x derived)
//	           + rows x (fan-out - 1) x 24
//	           + buckets x (state + output) + crosstab
//	           + rows x fan-out x 128 + measures x (cells x 448 +
//	             axis keys x 416), with a crosstab (its partitions)
//	streaming  + buckets x (state + output)
//	fused      + crosstab
//
// where a bucket's state and output are BucketBytes', crosstab is
// cells x cell bytes + empty grid positions x position bytes (U51
// E5-S11) + axis keys x axis-key bytes + margin slots x
// margin-slot bytes + auxiliary slots x auxiliary bytes (U51 E5-S9:
// per measure, from the per-aggregator table), plus (rows +
// partition records) x fan-out x 96 per value-state aggregation on
// every arm. Saturates at math.MaxInt64
// rather than wrapping.
func EstimateMemory(in MemoryInputs) int64 {
	total := int64(runBaseBytes)
	add := func(a, b int64) { total = addSat(total, mulSat(a, b)) }
	payloadRecords := in.Records
	if in.MaxShardRecords > 0 {
		payloadRecords = in.MaxShardRecords
	}
	add(mulSat(payloadRecords, in.Stride), fileCopyFactor)
	add(in.DirectoryBytes, directoryCopyFactor)
	add(in.SchemaDecodes, in.SchemaBytes)
	add(in.JoinRightRows, addSat(fileCopyFactor*in.Stride, buildCopyFactor*in.RecordBytes))
	total = addSat(total, in.MatrixBytes)
	total = addSat(total, in.GrouperBytes)
	total = addSat(total, in.VectorBytes)
	rows := in.Records
	if in.JoinedRows > 0 {
		rows = in.JoinedRows
	}
	fanOut := max(in.FanOut, 1)
	add(mulSat(addSat(rows, in.PartitionRecords), fanOut), mulSat(in.ValueStateAggregations, valueEntryBytes))
	add(in.TestInstances, testStateBytes)
	add(in.TestLevels, testLevelBytes)
	add(in.TestValues, testValueBytes)
	bucketState, bucketFinal := BucketBytes(in)
	add(in.PartitionBuckets, bucketState)
	switch in.Arm {
	case ArmBuffered:
		per := addSat(in.RecordBytes, addSat(mulSat(in.Aggregations, bufferedValueBytes), mulSat(in.DerivedColumns, derivedColumnBytes)))
		add(rows, per)
		add(mulSat(rows, fanOut-1), fanOutSlotBytes)
		add(in.Buckets, bucketFinal)
		total = addSat(total, crosstabBytes(in))
		if in.Cells > 0 {
			// The buffered crosstab's partitions (U51 E5-S9): the rows'
			// once, the cells' and axis keys' once per measure.
			measures := max(in.CrosstabMeasures, 1)
			add(mulSat(rows, fanOut), crosstabBufferedRecordBytes)
			add(mulSat(in.Cells, measures), crosstabBufferedCellBytes)
			add(mulSat(in.AxisKeys, measures), crosstabBufferedAxisKeyBytes)
		}
	case ArmStreaming:
		add(in.Buckets, bucketFinal)
	case ArmFusedCrosstab:
		total = addSat(total, crosstabBytes(in))
	}
	return total
}

// crosstabBytes is a crosstab's grid, axis-key and margin charge
// (U51 E5-S9): cells x cell bytes + empty grid positions x position
// bytes (U51 E5-S11) + axis keys x axis-key bytes + margin slots x
// margin-slot bytes + auxiliary slots x auxiliary bytes.
func crosstabBytes(in MemoryInputs) int64 {
	n := mulSat(in.Cells, in.CellBytes)
	n = addSat(n, mulSat(in.GridPositions, in.GridPositionBytes))
	n = addSat(n, mulSat(in.AxisKeys, in.AxisKeyBytes))
	n = addSat(n, mulSat(in.MarginSlots, in.MarginSlotBytes))
	return addSat(n, mulSat(in.AuxSlots, in.AuxSlotBytes))
}

// BucketBytes is one group bucket's charge under in: state is what it
// holds while records fold (a parallel partition's bucket costs only
// this), final adds what finalize writes for a merged bucket (EstimateMemory's
// bucket terms): state = 256 + 128 x extra groupers + the slots'
// AggStateBytes + 64 x set members; the output adds 1024 + 576 x aggs
// with components on, 512 + the slots' AggOutputOffBytes with them off
// (U51 E5-S7), and 128 x set members either way.
func BucketBytes(in MemoryInputs) (state, final int64) {
	state = addSat(bucketBaseBytes, mulSat(max(in.ExtraGroupers, 0), extraGrouperStateBytes))
	state = addSat(state, max(in.AggStateBytes, 0))
	state = addSat(state, mulSat(in.SetMembers, setMemberStateBytes))
	if in.ComponentsOff {
		final = addSat(state, bucketOutputOffBytes)
		final = addSat(final, max(in.AggOutputOffBytes, 0))
	} else {
		final = addSat(state, bucketOutputBytes)
		final = addSat(final, mulSat(in.Aggregations, aggOutputBytes))
	}
	final = addSat(final, mulSat(in.SetMembers, setMemberOutputBytes))
	return state, final
}

// RecordBytes is the resident size, in bytes, of one buffered record
// decoded over schema at full width (a projected decode holds fewer
// slots, so this bounds it): the record header, one slot per field and
// per set-mask / parent-group word, the bit planes, and the aux block
// for decimal128 fields.
func RecordBytes(schema *encoding.Schema) int64 {
	if schema == nil {
		return recordHeaderBytes
	}
	n := int64(len(schema.Fields))
	words := int64(len(schema.Groups))
	var decimals int64
	for _, f := range schema.Fields {
		switch {
		case f.Type.IsSet():
			words += (int64(f.Type.MaxSetEntries()) + 63) / 64
		case f.Type.IsDecimal():
			decimals++
		}
	}
	b := recordHeaderBytes + slotBytes*(n+words) + planeBytes*((n+63)/64)
	if decimals > 0 {
		b += auxBytes + decimalBytes*decimals
	}
	return b
}

// SchemaDecodeBytes is what decoding schema's block allocates, in
// bytes: per field descriptor fieldDecodeBytes, per dictionary
// DictionaryDecodeBytes, per parent group its entry table. An upper
// bound calibrated on measured decodes
// (TestSchemaDecodeBytes_BoundsDecode).
func SchemaDecodeBytes(schema *encoding.Schema) int64 {
	if schema == nil {
		return 0
	}
	b := fieldDecodeBytes * int64(len(schema.Fields))
	for i := range schema.Fields {
		b = addSat(b, DictionaryDecodeBytes(schema.Fields[i].Dictionary))
	}
	for _, g := range schema.Groups {
		b = addSat(b, int64(len(g.Entries)))
	}
	return b
}

// DictionaryDecodeBytes is what encoding.Dictionary.ReadFrom allocates
// decoding d: per entry dictEntryDecodeBytes plus its label's string
// copy (rounded up a size class: +1/8 + 8), and the shared label
// buffer — dictBufferBytesPerEntry a entry (min 256), doubled until
// every label fits, each generation allocated. 0 for nil.
func DictionaryDecodeBytes(d *encoding.Dictionary) int64 {
	if d == nil || d.Count() == 0 {
		return 0
	}
	n := int64(d.Count())
	var labels, strings int64
	for id := range d.Count() {
		l := int64(len(d.Resolve(uint32(id))))
		labels += l
		strings += l + l/8 + 8
	}
	buf := max(n*dictBufferBytesPerEntry, 256)
	b := addSat(mulSat(n, dictEntryDecodeBytes), strings)
	for c := buf; ; c = mulSat(c, 2) {
		b = addSat(b, c)
		if c >= labels || c == maxInt64 {
			break
		}
	}
	return b
}

// MergeBlocks is the number of merge fragments a run over the given
// per-shard record counts (archive order) populates. Blocks key by the
// GLOBAL record ordinal, so a shard holds one fragment per global block
// its ordinal range touches: ceil(n / MergeBlockSize) on a single file,
// one more for a shard whose edges fall inside blocks. An upper bound
// on the per-fragment states held until finalize (a non-head fragment
// keeps rows instead: MergeBufferedRows).
func MergeBlocks(shardRecords ...int64) int64 {
	var blocks, start int64
	for _, n := range shardRecords {
		if n > 0 {
			blocks += (start+n-1)/MergeBlockSize - start/MergeBlockSize + 1
			start += n
		}
	}
	return blocks
}

// MergeBufferedRows is the rows a run over the given per-shard record
// counts (archive order) BUFFERS in non-head merge fragments: a shard
// that starts inside a block keeps that block's rows (up to the block
// end) for the finalize to replay on top of the head fragment. 0 on a
// single file.
func MergeBufferedRows(shardRecords ...int64) int64 {
	var rows, start int64
	for _, n := range shardRecords {
		if n <= 0 {
			continue
		}
		if off := start % MergeBlockSize; off != 0 {
			rows += min(n, MergeBlockSize-off)
		}
		start += n
	}
	return rows
}

// MatrixStateBytes is one matrix's merge-block state: blocks x buckets
// x accumulatorBytes — one linalg.CoMoment per populated merge block
// per bucket, held until finalize. Saturating.
func MatrixStateBytes(blocks, buckets, accumulatorBytes int64) int64 {
	return mulSat(mulSat(blocks, buckets), accumulatorBytes)
}

// MatrixRowBufferBytes is a buffered matrix's row store: records x
// rowBytes (vectors.Matrix.RowBytes) — every admitted row kept until
// finalize, records being the upper bound on admitted rows. Saturating;
// 0 for a streamable matrix (rowBytes 0).
func MatrixRowBufferBytes(records, rowBytes int64) int64 {
	return mulSat(records, rowBytes)
}

// matrixRowWorkFactor x a buffered matrix's row bytes
// (vectors.Matrix.RowBytes, 8·(p + 1)) per kept row copy: the row
// store with its append growth, and the rank finalize's working set —
// the member columns copied out of the store with their growth, the
// weights, each column's ranks, ties and sort scratch, and the garbage
// a grouped finalize leaves between collections. Measured (estimate
// calibration, p = 3 and p = 8, spearman): ~7.6x per row copy under a
// six-way fan-out, ~9.2x ungrouped.
const matrixRowWorkFactor = 10

// MatrixRowWorkBytes is what a buffered matrix's kept rows cost a run
// for the memory estimate: rows x rowBytes x the row-work factor — the
// row store (MatrixRowBufferBytes is its bare payload) plus the rank
// finalize's working set over it. rows is every kept row copy (records
// x the fan-out multiplicity). Saturating; 0 for a streamable matrix.
func MatrixRowWorkBytes(rows, rowBytes int64) int64 {
	return mulSat(mulSat(rows, rowBytes), matrixRowWorkFactor)
}

// KMeansBufferBytes is GROUP_KMEANS's resident point store: records x
// p x 8 (every row's member tuple, records the upper bound on clustered
// rows) plus k x p x 8 (the centroids). Saturating.
func KMeansBufferBytes(records, p, k int64) int64 {
	return addSat(mulSat(mulSat(records, p), 8), mulSat(mulSat(k, p), 8))
}

const maxInt64 = int64(^uint64(0) >> 1)

// mulSat multiplies two non-negative figures, saturating at
// math.MaxInt64.
func mulSat(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > maxInt64/b {
		return maxInt64
	}
	return a * b
}

// addSat adds two non-negative figures, saturating at math.MaxInt64.
func addSat(a, b int64) int64 {
	if a > maxInt64-b {
		return maxInt64
	}
	return a + b
}
