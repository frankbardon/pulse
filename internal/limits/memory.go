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
	// collector's headroom over it. A filesystem the decoder cannot mmap
	// is read whole onto the heap (afero.ReadFile), and the per-record
	// garbage a scan produces (a joined record per row, decode scratch)
	// lets the heap run to the collector's goal — twice the live heap
	// under the default GOGC=100 — and past it while a concurrent mark
	// is in flight: measured up to 2.9x the payload (a joined stream).
	fileCopyFactor = 3
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
	// bucketBaseBytes per group bucket: the key string, the map entry,
	// the output row.
	bucketBaseBytes = 512
	// aggStateBytes per bucket per aggregation: one accumulator.
	aggStateBytes = 256
	// valueEntryBytes per distinct value an aggregator keeps
	// (distinct / mode / frequency families): a map entry. The distinct
	// (bucket, value) pairs of one aggregation never exceed the record
	// count, so the term is records x valueEntryBytes per such slot.
	valueEntryBytes = 96
	// cellBytes per crosstab cell: the cell accumulator, both axis-key
	// interner entries and the margin share.
	cellBytes = 512
)

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
	// ValueStateAggregations is how many of them keep per-value state
	// (distinct / mode / frequency families).
	ValueStateAggregations int64
	// DerivedColumns is the number of attribute, feature and window
	// outputs added to every record.
	DerivedColumns int64
	// Cells is the crosstab grid bound (0 without a crosstab).
	Cells int64
	// JoinRightRows is a join build side's record count (0 without a
	// join); each is a materialised record of RecordBytes plus its
	// payload.
	JoinRightRows int64
	// MatrixBytes is the matrices' merge-block state (MatrixStateBytes,
	// summed over every matrix) plus a buffered matrix's row store
	// (MatrixRowBufferBytes).
	MatrixBytes int64
}

// EstimateMemory is the upper-bound resident footprint of one run, in
// bytes, per arm:
//
//	every arm  base + 3 x records x stride (the payload + GC headroom)
//	           + join right rows x (3 x stride + 2 x record bytes)
//	           + matrix merge-block state
//	buffered   + records x (record bytes + 16 x aggs + 128 x derived)
//	           + buckets x (512 + 256 x aggs) + cells x 512
//	streaming  + buckets x (512 + 256 x aggs)
//	fused      + cells x 512
//
// plus records x 96 per value-state aggregation on every arm. Saturates
// at math.MaxInt64 rather than wrapping.
func EstimateMemory(in MemoryInputs) int64 {
	total := int64(runBaseBytes)
	add := func(a, b int64) { total = addSat(total, mulSat(a, b)) }
	add(in.Records, fileCopyFactor*in.Stride)
	add(in.JoinRightRows, addSat(fileCopyFactor*in.Stride, buildCopyFactor*in.RecordBytes))
	total = addSat(total, in.MatrixBytes)
	add(in.Records, mulSat(in.ValueStateAggregations, valueEntryBytes))
	bucketState := addSat(bucketBaseBytes, mulSat(in.Aggregations, aggStateBytes))
	switch in.Arm {
	case ArmBuffered:
		per := addSat(in.RecordBytes, addSat(mulSat(in.Aggregations, bufferedValueBytes), mulSat(in.DerivedColumns, derivedColumnBytes)))
		add(in.Records, per)
		add(in.Buckets, bucketState)
		add(in.Cells, cellBytes)
	case ArmStreaming:
		add(in.Buckets, bucketState)
	case ArmFusedCrosstab:
		add(in.Cells, cellBytes)
	}
	return total
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

// MergeBlocks is the number of merge blocks a run over the given
// per-shard record counts populates: ceil(n / MergeBlockSize) per
// shard, since block numbering restarts per shard. A single-file
// cohort passes one count.
func MergeBlocks(shardRecords ...int64) int64 {
	var blocks int64
	for _, n := range shardRecords {
		if n > 0 {
			blocks += (n + MergeBlockSize - 1) / MergeBlockSize
		}
	}
	return blocks
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
