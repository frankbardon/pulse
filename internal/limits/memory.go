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
	// GrouperBytes is a buffered vector grouper's point store
	// (KMeansBufferBytes, GROUP_KMEANS) — 0 without one.
	GrouperBytes int64
	// ArchiveBytes is a shard archive's byte length — every byte Open
	// and the scan read onto the heap (afero.ReadFile, once each),
	// whatever the filesystem; 0 for a single file. It replaces records
	// x stride in the payload term when larger: every shard's header,
	// schema block and dictionaries, the canonical _schema.pulse entry
	// and the zip directory ride with the payload.
	ArchiveBytes int64
	// SchemaDecodes is how many schema blocks the run decodes, with
	// their dictionaries, beyond the cohort's own (an archive: 1, the
	// canonical entry — Open's record-count peek and the scan read
	// every shard's layout-only and allocate no dictionary); 0 for a
	// single file or an anchor-opened shard. Each costs SchemaBytes.
	SchemaDecodes int64
	// SchemaBytes is what one decode of the cohort's schema allocates
	// (SchemaDecodeBytes).
	SchemaBytes int64
	// PartitionBuckets is the bucket states the per-shard parallel
	// reducer's partials hold until the merge — the sum over shards of
	// each shard's bucket bound (1 per shard ungrouped); 0 off that arm.
	// Each costs what a merged bucket costs; the merge folds into one
	// partial while the others are still live.
	PartitionBuckets int64
	// PartitionRecords is the records whose per-value state the
	// per-shard partials hold beside the merged state (the run's record
	// count on the per-shard parallel arm, else 0): charged per
	// value-state aggregation like Records.
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
//	every arm  base + 3 x max(records x stride, archive bytes)
//	             (the payload + GC headroom; an archive's every byte)
//	           + schema decodes x schema bytes, on an archive
//	           + join right rows x (3 x stride + 2 x record bytes)
//	           + matrix merge-block state + vector-grouper point store
//	           + partition buckets x (512 + 256 x aggs), per-shard arm
//	           + test instances x 1024 + test levels x 256
//	           + buffered-test values x 32
//	buffered   + rows x (record bytes + 16 x aggs + 128 x derived)
//	           + rows x (fan-out - 1) x 24
//	           + buckets x (512 + 256 x aggs) + cells x 512
//	streaming  + buckets x (512 + 256 x aggs)
//	fused      + cells x 512
//
// plus (rows + partition records) x fan-out x 96 per value-state
// aggregation on every arm. Saturates at math.MaxInt64 rather than
// wrapping.
func EstimateMemory(in MemoryInputs) int64 {
	total := int64(runBaseBytes)
	add := func(a, b int64) { total = addSat(total, mulSat(a, b)) }
	add(max(mulSat(in.Records, in.Stride), in.ArchiveBytes), fileCopyFactor)
	add(in.SchemaDecodes, in.SchemaBytes)
	add(in.JoinRightRows, addSat(fileCopyFactor*in.Stride, buildCopyFactor*in.RecordBytes))
	total = addSat(total, in.MatrixBytes)
	total = addSat(total, in.GrouperBytes)
	rows := in.Records
	if in.JoinedRows > 0 {
		rows = in.JoinedRows
	}
	fanOut := max(in.FanOut, 1)
	add(mulSat(addSat(rows, in.PartitionRecords), fanOut), mulSat(in.ValueStateAggregations, valueEntryBytes))
	add(in.TestInstances, testStateBytes)
	add(in.TestLevels, testLevelBytes)
	add(in.TestValues, testValueBytes)
	bucketState := addSat(bucketBaseBytes, mulSat(in.Aggregations, aggStateBytes))
	add(in.PartitionBuckets, bucketState)
	switch in.Arm {
	case ArmBuffered:
		per := addSat(in.RecordBytes, addSat(mulSat(in.Aggregations, bufferedValueBytes), mulSat(in.DerivedColumns, derivedColumnBytes)))
		add(rows, per)
		add(mulSat(rows, fanOut-1), fanOutSlotBytes)
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
