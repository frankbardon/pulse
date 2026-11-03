package arrow

import (
	"bytes"
	"context"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/spf13/afero"

	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
)

// arrowFuzzFile writes one IPC file of sc over the rows build appends,
// each batch one call of build.
func arrowFuzzFile(tb testing.TB, sc *arrow.Schema, batches int, build func(b *array.RecordBuilder), opts ...ipc.Option) []byte {
	tb.Helper()
	alloc := memory.NewGoAllocator()
	var buf bytes.Buffer
	fw, err := ipc.NewFileWriter(&buf, append([]ipc.Option{ipc.WithSchema(sc), ipc.WithAllocator(alloc)}, opts...)...)
	if err != nil {
		tb.Fatal(err)
	}
	b := array.NewRecordBuilder(alloc, sc)
	defer b.Release()
	for i := 0; i < batches; i++ {
		build(b)
		rec := b.NewRecordBatch()
		if err := fw.Write(rec); err != nil {
			tb.Fatal(err)
		}
		rec.Release()
	}
	if err := fw.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// arrowFuzzSeeds are small valid IPC files across the reader's typed
// branches — strings with a null, ints, floats, bools, a timestamp, a
// date, a decimal, a string list, a float vector, a dictionary column,
// two batches, LZ4- and zstd-compressed bodies, an empty file — plus
// truncations of the first.
func arrowFuzzSeeds(tb testing.TB) [][]byte {
	tb.Helper()
	simpleSc := arrow.NewSchema([]arrow.Field{
		{Name: "name", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "age", Type: arrow.PrimitiveTypes.Int32},
		{Name: "score", Type: arrow.PrimitiveTypes.Float64},
		{Name: "ok", Type: arrow.FixedWidthTypes.Boolean},
	}, nil)
	simpleRows := func(b *array.RecordBuilder) {
		b.Field(0).(*array.StringBuilder).AppendValues([]string{"alice", "", "carol"}, []bool{true, false, true})
		b.Field(1).(*array.Int32Builder).AppendValues([]int32{30, 25, 35}, nil)
		b.Field(2).(*array.Float64Builder).AppendValues([]float64{95.5, 88, 72.25}, nil)
		b.Field(3).(*array.BooleanBuilder).AppendValues([]bool{true, false, true}, nil)
	}
	simple := arrowFuzzFile(tb, simpleSc, 1, simpleRows)
	lz4File := arrowFuzzFile(tb, simpleSc, 1, simpleRows, ipc.WithLZ4())
	zstdFile := arrowFuzzFile(tb, simpleSc, 1, simpleRows, ipc.WithZstd())
	typed := arrowFuzzFile(tb, arrow.NewSchema([]arrow.Field{
		{Name: "ts", Type: &arrow.TimestampType{Unit: arrow.Millisecond, TimeZone: "UTC"}, Nullable: true},
		{Name: "d", Type: arrow.FixedWidthTypes.Date32},
		{Name: "amt", Type: &arrow.Decimal128Type{Precision: 10, Scale: 2}, Nullable: true},
		{Name: "tags", Type: arrow.ListOf(arrow.BinaryTypes.String), Nullable: true},
		{Name: "vec", Type: arrow.FixedSizeListOf(2, arrow.PrimitiveTypes.Float32)},
	}, nil), 2, func(b *array.RecordBuilder) {
		b.Field(0).(*array.TimestampBuilder).AppendValues([]arrow.Timestamp{1_700_000_000_123, 0}, []bool{true, false})
		b.Field(1).(*array.Date32Builder).AppendValues([]arrow.Date32{19000, -3}, nil)
		b.Field(2).(*array.Decimal128Builder).AppendValues([]decimal128.Num{decimal128.FromI64(12345), decimal128.FromI64(-1)}, nil)
		lb := b.Field(3).(*array.ListBuilder)
		vb := lb.ValueBuilder().(*array.StringBuilder)
		lb.Append(true)
		vb.AppendValues([]string{"x", "y"}, nil)
		lb.AppendNull()
		fb := b.Field(4).(*array.FixedSizeListBuilder)
		fv := fb.ValueBuilder().(*array.Float32Builder)
		fb.AppendValues([]bool{true, true})
		fv.AppendValues([]float32{1, 2, 3, 4}, nil)
	})
	dict := arrowFuzzFile(tb, arrow.NewSchema([]arrow.Field{
		{Name: "cat", Type: &arrow.DictionaryType{IndexType: arrow.PrimitiveTypes.Int8, ValueType: arrow.BinaryTypes.String}},
		{Name: "u", Type: arrow.PrimitiveTypes.Uint64},
	}, nil), 1, func(b *array.RecordBuilder) {
		db := b.Field(0).(*array.BinaryDictionaryBuilder)
		for _, v := range []string{"a", "b", "a"} {
			if err := db.AppendString(v); err != nil {
				tb.Fatal(err)
			}
		}
		b.Field(1).(*array.Uint64Builder).AppendValues([]uint64{1, 1 << 63, 7}, nil)
	})
	empty := arrowFuzzFile(tb, arrow.NewSchema([]arrow.Field{
		{Name: "a", Type: arrow.BinaryTypes.String},
	}, nil), 0, func(*array.RecordBuilder) {})

	seeds := [][]byte{simple, lz4File, zstdFile, typed, dict, empty, nil, []byte("ARROW1\x00\x00")}
	for _, cut := range []int{8, len(simple) / 2, len(simple) - 10, len(simple) - 1} {
		seeds = append(seeds, simple[:cut])
	}
	return seeds
}

// FuzzArrowImport holds the Arrow IPC importer — ImportJob.Predict,
// then ImportJob.Run with schema inference into a hermetic fs, then the
// embedded-overlay read — to the fuzz contract: no panic (the IPC
// decoder is third-party code; its panics must surface as coded
// errors), a bounded run, every error a registered *errors.CodedError.
func FuzzArrowImport(f *testing.F) {
	for _, s := range arrowFuzzSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fuzzImportContract.Run(t, in, arrowFuzzImport)
	})
}

// fuzzImportContract caps one import's total heap allocation at 1 GiB:
// far above what an honest input of at most fuzzcontract.DefaultMaxInput
// bytes needs, far below what a decoder trusting a declared length would
// take.
var fuzzImportContract = fuzzcontract.Contract{MaxAlloc: 1 << 30}

func arrowFuzzImport(in []byte) error {
	ctx := context.Background()
	if _, err := pio.NewImportJob(NewReaderFromBytes(in), "p.pulse").Predict(ctx); err != nil {
		return err
	}
	job := pio.NewImportJob(NewReaderFromBytes(in), "in.pulse")
	job.FS = afero.NewMemMapFs()
	if _, err := job.Run(ctx); err != nil {
		return err
	}
	r := NewReaderFromBytes(in)
	defer r.Close()
	_, err := r.ReadOverlays()
	return err
}
