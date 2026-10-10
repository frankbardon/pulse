package limits

import (
	"math"
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
		// Per shard: numbering restarts, so 4,097 + 5 rows take three
		// blocks where one file of 4,102 takes two.
		{[]int64{MergeBlockSize + 1, 5}, 3},
		{[]int64{MergeBlockSize + 6}, 2},
		{[]int64{-1, 3}, 1},
	}
	for _, c := range cases {
		if got := MergeBlocks(c.shards...); got != c.want {
			t.Errorf("MergeBlocks(%v) = %d, want %d", c.shards, got, c.want)
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
