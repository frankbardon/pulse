package limits

import (
	"bytes"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/linalg"
)

// TestMergeBlockSizeMatchesLinalg pins the leaf's restated block
// length to linalg's — the leaf may not import linalg.
func TestMergeBlockSizeMatchesLinalg(t *testing.T) {
	if MergeBlockSize != linalg.MergeBlockSize {
		t.Fatalf("limits.MergeBlockSize = %d, linalg.MergeBlockSize = %d", MergeBlockSize, linalg.MergeBlockSize)
	}
}

func TestMergeBlocks(t *testing.T) {
	cases := []struct {
		shards []int64
		want   int64
	}{
		{nil, 0},
		{[]int64{0}, 0},
		{[]int64{1}, 1},
		{[]int64{MergeBlockSize}, 1},
		{[]int64{MergeBlockSize + 1}, 2},
		{[]int64{2*MergeBlockSize + 31}, 3},
		// Global ordinals: 4,097 + 5 rows touch blocks 0, 1 (shard 0)
		// and 1 (shard 1) — three fragments where one file of 4,102
		// holds two blocks.
		{[]int64{MergeBlockSize + 1, 5}, 3},
		{[]int64{MergeBlockSize + 6}, 2},
		// Aligned shards add no fragment.
		{[]int64{MergeBlockSize, MergeBlockSize, 3}, 3},
		// 1-row shards inside one block: one fragment each.
		{[]int64{1, 1, 0, 1}, 3},
		// A shard straddling a block edge touches two blocks.
		{[]int64{MergeBlockSize - 1, 2}, 3},
		{[]int64{-1, 3}, 1},
	}
	for _, c := range cases {
		if got := MergeBlocks(c.shards...); got != c.want {
			t.Errorf("MergeBlocks(%v) = %d, want %d", c.shards, got, c.want)
		}
	}
}

func TestMergeBufferedRows(t *testing.T) {
	cases := []struct {
		shards []int64
		want   int64
	}{
		{nil, 0},
		{[]int64{3 * MergeBlockSize}, 0},
		{[]int64{MergeBlockSize, MergeBlockSize, 3}, 0},
		{[]int64{MergeBlockSize + 1, 5}, 5},
		// Shard 1 starts 1 short of the edge: it buffers 1 row, the
		// rest of its rows fold in head fragments.
		{[]int64{MergeBlockSize - 1, 10}, 1},
		{[]int64{1, 1, 0, 1}, 2},
		{[]int64{-1, 3}, 0},
	}
	for _, c := range cases {
		if got := MergeBufferedRows(c.shards...); got != c.want {
			t.Errorf("MergeBufferedRows(%v) = %d, want %d", c.shards, got, c.want)
		}
	}
}

func TestMatrixStateBytes(t *testing.T) {
	if got := MatrixStateBytes(3, 4, 104); got != 3*4*104 {
		t.Fatalf("MatrixStateBytes = %d, want %d", got, 3*4*104)
	}
	if got := MatrixStateBytes(0, 4, 104); got != 0 {
		t.Fatalf("no blocks: %d, want 0", got)
	}
	if got := MatrixStateBytes(math.MaxInt64/2, 4, 104); got != math.MaxInt64 {
		t.Fatalf("saturation: %d", got)
	}
}

func TestRecordBytes(t *testing.T) {
	dict := encoding.NewDictionary()
	cases := []struct {
		name   string
		schema *encoding.Schema
		want   int64
	}{
		{"nil", nil, recordHeaderBytes},
		{"two numerics", &encoding.Schema{Fields: []encoding.Field{
			{Name: "a", Type: encoding.FieldTypeF64}, {Name: "b", Type: encoding.FieldTypeU8},
		}}, recordHeaderBytes + 2*slotBytes + planeBytes},
		// A set field takes a slot plus its mask words; a decimal the
		// aux block.
		{"set and decimal", &encoding.Schema{Fields: []encoding.Field{
			{Name: "s", Type: encoding.FieldTypeSetU256, Dictionary: dict},
			{Name: "d", Type: encoding.FieldTypeDecimal128},
		}}, recordHeaderBytes + (2+4)*slotBytes + planeBytes + auxBytes + decimalBytes},
	}
	for _, c := range cases {
		if got := RecordBytes(c.schema); got != c.want {
			t.Errorf("%s: RecordBytes = %d, want %d", c.name, got, c.want)
		}
	}
	// 65 fields span two bit-plane words.
	wide := &encoding.Schema{}
	for range 65 {
		wide.Fields = append(wide.Fields, encoding.Field{Type: encoding.FieldTypeU8})
	}
	if got, want := RecordBytes(wide), int64(recordHeaderBytes+65*slotBytes+2*planeBytes); got != want {
		t.Errorf("65 fields: %d, want %d", got, want)
	}
}

// TestEstimateMemory_PerArm pins each arm's formula term by term.
func TestEstimateMemory_PerArm(t *testing.T) {
	const (
		records = 1000
		stride  = 50
		rec     = 400
	)
	payload := int64(runBaseBytes + records*fileCopyFactor*stride)
	bucket := func(aggs int64) int64 { return bucketBaseBytes + aggs*aggStateBytes }
	cases := []struct {
		name string
		in   MemoryInputs
		want int64
	}{
		{"buffered ungrouped", MemoryInputs{Arm: ArmBuffered, Records: records, Stride: stride, RecordBytes: rec, Buckets: 1, Aggregations: 2},
			payload + records*(rec+2*bufferedValueBytes) + bucket(2)},
		{"buffered derived columns", MemoryInputs{Arm: ArmBuffered, Records: records, Stride: stride, RecordBytes: rec, Buckets: 1, Aggregations: 1, DerivedColumns: 3},
			payload + records*(rec+bufferedValueBytes+3*derivedColumnBytes) + bucket(1)},
		{"buffered crosstab", MemoryInputs{Arm: ArmBuffered, Records: records, Stride: stride, RecordBytes: rec, Buckets: 1, Aggregations: 1, Cells: 40},
			payload + records*(rec+bufferedValueBytes) + bucket(1) + 40*cellBytes},
		// Streaming holds no record: buckets only.
		{"streaming grouped", MemoryInputs{Arm: ArmStreaming, Records: records, Stride: stride, RecordBytes: rec, Buckets: 16, Aggregations: 2},
			payload + 16*bucket(2)},
		{"streaming value state", MemoryInputs{Arm: ArmStreaming, Records: records, Stride: stride, RecordBytes: rec, Buckets: 1, Aggregations: 1, ValueStateAggregations: 1},
			payload + records*valueEntryBytes + bucket(1)},
		{"fused crosstab", MemoryInputs{Arm: ArmFusedCrosstab, Records: records, Stride: stride, RecordBytes: rec, Buckets: 1, Aggregations: 1, Cells: 40},
			payload + 40*cellBytes},
		{"join build side", MemoryInputs{Arm: ArmStreaming, Records: records, Stride: stride, RecordBytes: rec, Buckets: 1, Aggregations: 1, JoinRightRows: 300},
			payload + 300*(fileCopyFactor*stride+buildCopyFactor*rec) + bucket(1)},
		{"matrix state", MemoryInputs{Arm: ArmStreaming, Records: records, Stride: stride, RecordBytes: rec, Buckets: 1, Aggregations: 1, MatrixBytes: 12345},
			payload + 12345 + bucket(1)},
		{"no records", MemoryInputs{Arm: ArmBuffered, Buckets: 1}, runBaseBytes + bucket(0)},
		// An archive (U51 E1-S2): streamed one shard at a time, so the
		// payload term charges the LARGEST shard's records x stride, not
		// the archive's; plus its central directory and the one schema
		// block decoded with its dictionaries (the canonical entry; the
		// shards' are read layout-only).
		{"archive", MemoryInputs{Arm: ArmStreaming, Records: records, Stride: stride, RecordBytes: rec, Buckets: 1, Aggregations: 1, MaxShardRecords: 400, DirectoryBytes: 900, SchemaDecodes: 1, SchemaBytes: 7000},
			runBaseBytes + fileCopyFactor*400*stride + directoryCopyFactor*900 + 7000 + bucket(1)},
		// Per-row terms keep the archive's whole record count: only the
		// payload term reads the largest shard.
		{"archive buffered", MemoryInputs{Arm: ArmBuffered, Records: records, Stride: stride, RecordBytes: rec, Buckets: 1, Aggregations: 1, MaxShardRecords: 400, DirectoryBytes: 900},
			runBaseBytes + fileCopyFactor*400*stride + directoryCopyFactor*900 + records*(rec+bufferedValueBytes) + bucket(1)},
		// The per-shard parallel arm: every partial's buckets and
		// per-value state beside the merged ones.
		{"shard partitions", MemoryInputs{Arm: ArmStreaming, Records: records, Stride: stride, RecordBytes: rec, Buckets: 4, Aggregations: 1, ValueStateAggregations: 1, PartitionBuckets: 9, PartitionRecords: records},
			payload + 2*records*valueEntryBytes + 13*bucket(1)},
		// A fan-out grouper (U35 #267): a record reaches up to FanOut
		// buckets, so the distinct (bucket, value) pairs reach records x
		// FanOut; the buffered arm adds a partition slot per extra bucket.
		{"fan-out value state", MemoryInputs{Arm: ArmStreaming, Records: records, Stride: stride, RecordBytes: rec, Buckets: 3, Aggregations: 1, ValueStateAggregations: 1, FanOut: 3},
			payload + 3*records*valueEntryBytes + 3*bucket(1)},
		{"fan-out buffered slots", MemoryInputs{Arm: ArmBuffered, Records: records, Stride: stride, RecordBytes: rec, Buckets: 3, Aggregations: 1, FanOut: 3},
			payload + records*(rec+bufferedValueBytes) + 2*records*fanOutSlotBytes + 3*bucket(1)},
		// Tier-1 tests (U35 #437): an instance per test per bucket, its
		// split levels, and a buffered test's kept values.
		{"tests", MemoryInputs{Arm: ArmBuffered, Records: records, Stride: stride, RecordBytes: rec, Buckets: 4, Aggregations: 1, TestInstances: 8, TestLevels: 16, TestValues: 2 * records},
			payload + records*(rec+bufferedValueBytes) + 4*bucket(1) + 8*testStateBytes + 16*testLevelBytes + 2*records*testValueBytes},
	}
	for _, c := range cases {
		if got := EstimateMemory(c.in); got != c.want {
			t.Errorf("%s: EstimateMemory = %d, want %d", c.name, got, c.want)
		}
	}
	if got := EstimateMemory(MemoryInputs{Arm: ArmBuffered, Records: math.MaxInt64 / 2, Stride: 100, RecordBytes: 100}); got != math.MaxInt64 {
		t.Errorf("saturation: %d", got)
	}
}

// TestSchemaDecodeBytes_BoundsDecode: SchemaDecodeBytes bounds what
// decoding a schema block really allocates (TotalAlloc over one
// ReadSchema), within a factor of two — on dictionaries of short,
// 40-byte and 1000-byte labels (per-entry overhead, then the label
// copies and the shared buffer's growth, dominate in turn).
// The schema decode is the archive term the memory estimate charges
// for the canonical entry (E8-S3; shards read layout-only since E8-S5).
func TestSchemaDecodeBytes_BoundsDecode(t *testing.T) {
	for _, c := range []struct {
		name   string
		format string
	}{{"short labels", "%d"}, {"40-byte labels", "tag-%07d-abcdefghijklmnopqrstuvwxyz"}, {"1000-byte labels", "%07d" + strings.Repeat("x", 993)}} {
		t.Run(c.name, func(t *testing.T) {
			s := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
			d := encoding.NewDictionary()
			for i := range 20_000 {
				if _, err := d.Add(fmt.Sprintf(c.format, i)); err != nil {
					t.Fatal(err)
				}
			}
			s.Fields = append(s.Fields, encoding.Field{Name: "tag", Type: encoding.FieldTypeCategoricalU32, ByteOffset: 8, Dictionary: d})
			var buf bytes.Buffer
			if err := encoding.WriteSchema(&buf, s); err != nil {
				t.Fatal(err)
			}
			raw := buf.Bytes()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			got, err := encoding.ReadSchema(bytes.NewReader(raw), encoding.FormatVersion)
			runtime.ReadMemStats(&after)
			if err != nil {
				t.Fatal(err)
			}
			runtime.KeepAlive(got)
			alloc := float64(after.TotalAlloc - before.TotalAlloc)
			bound := float64(SchemaDecodeBytes(s))
			t.Logf("allocated %.0f, bound %.0f (%.2f)", alloc, bound, alloc/bound)
			if alloc > bound || alloc < bound/2 {
				t.Errorf("decode allocated %.0f bytes, SchemaDecodeBytes %.0f: want alloc within [bound/2, bound]", alloc, bound)
			}
		})
	}
	if SchemaDecodeBytes(nil) != 0 {
		t.Error("nil schema")
	}
}

func TestMatrixRowBufferBytes(t *testing.T) {
	if got := MatrixRowBufferBytes(1000, 8*4); got != 1000*32 {
		t.Fatalf("MatrixRowBufferBytes = %d, want %d", got, 1000*32)
	}
	if got := MatrixRowBufferBytes(1000, 0); got != 0 {
		t.Fatalf("streamable (row bytes 0): %d, want 0", got)
	}
	if got := MatrixRowBufferBytes(math.MaxInt64/2, 32); got != math.MaxInt64 {
		t.Fatalf("saturation: %d", got)
	}
}

// TestEstimateMemory_JoinedRows: a fan-out join's JoinedRows replaces
// Records in every per-row processing term — buffered records and
// fan-out slots, per-value state — while the payload term stays on the
// left scan's Records; 0 leaves the figure unchanged.
func TestEstimateMemory_JoinedRows(t *testing.T) {
	base := MemoryInputs{Records: 100, Stride: 40, RecordBytes: 300, Buckets: 1, Aggregations: 1, ValueStateAggregations: 1, DerivedColumns: 2, FanOut: 2}
	for _, arm := range []MemoryArm{ArmBuffered, ArmStreaming, ArmFusedCrosstab} {
		in := base
		in.Arm = arm
		plain := EstimateMemory(in)
		in.JoinedRows = 0
		if got := EstimateMemory(in); got != plain {
			t.Fatalf("%s: JoinedRows 0 moved the estimate %d -> %d", arm, plain, got)
		}
		in.JoinedRows = 400
		// 300 extra rows: per-value state (x fan-out 2) on every arm,
		// the buffered record (300 + 16 + 2 x 128) and one fan-out slot
		// on the buffered arm. Never the payload.
		want := plain + 300*2*valueEntryBytes
		if arm == ArmBuffered {
			want += 300*(300+bufferedValueBytes+2*derivedColumnBytes) + 300*fanOutSlotBytes
		}
		if got := EstimateMemory(in); got != want {
			t.Fatalf("%s: JoinedRows 400 = %d, want %d", arm, got, want)
		}
	}
}
