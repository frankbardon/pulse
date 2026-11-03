package parquet

import (
	"bytes"
	"context"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	pq "github.com/apache/arrow-go/v18/parquet"
	"github.com/apache/arrow-go/v18/parquet/compress"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/spf13/afero"

	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
)

// parquetFuzzFile writes one Parquet file of sc, one row group per
// call of build, under props.
func parquetFuzzFile(tb testing.TB, sc *arrow.Schema, props *pq.WriterProperties, groups int, build func(b *array.RecordBuilder)) []byte {
	tb.Helper()
	var buf bytes.Buffer
	pw, err := pqarrow.NewFileWriter(sc, &buf, props, pqarrow.DefaultWriterProps())
	if err != nil {
		tb.Fatal(err)
	}
	b := array.NewRecordBuilder(memory.NewGoAllocator(), sc)
	defer b.Release()
	for i := 0; i < groups; i++ {
		build(b)
		rec := b.NewRecordBatch()
		if err := pw.Write(rec); err != nil {
			tb.Fatal(err)
		}
		rec.Release()
	}
	if err := pw.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// parquetFuzzSeeds are small valid files across the reader's typed
// branches and page encodings — plain and dictionary pages, snappy
// compression, strings with a null, ints, floats, bools, a timestamp, a
// date, a string list, two row groups, no rows — plus truncations.
func parquetFuzzSeeds(tb testing.TB) [][]byte {
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
	plain := parquetFuzzFile(tb, simpleSc, pq.NewWriterProperties(pq.WithDictionaryDefault(false)), 1, simpleRows)
	dict := parquetFuzzFile(tb, simpleSc, pq.NewWriterProperties(pq.WithDictionaryDefault(true)), 2, simpleRows)
	snappy := parquetFuzzFile(tb, simpleSc, pq.NewWriterProperties(pq.WithCompression(compress.Codecs.Snappy)), 1, simpleRows)
	typed := parquetFuzzFile(tb, arrow.NewSchema([]arrow.Field{
		{Name: "ts", Type: &arrow.TimestampType{Unit: arrow.Millisecond, TimeZone: "UTC"}, Nullable: true},
		{Name: "d", Type: arrow.FixedWidthTypes.Date32},
		{Name: "tags", Type: arrow.ListOf(arrow.BinaryTypes.String), Nullable: true},
	}, nil), pq.NewWriterProperties(), 1, func(b *array.RecordBuilder) {
		b.Field(0).(*array.TimestampBuilder).AppendValues([]arrow.Timestamp{1_700_000_000_123, 0}, []bool{true, false})
		b.Field(1).(*array.Date32Builder).AppendValues([]arrow.Date32{19000, -3}, nil)
		lb := b.Field(2).(*array.ListBuilder)
		vb := lb.ValueBuilder().(*array.StringBuilder)
		lb.Append(true)
		vb.AppendValues([]string{"x", "y"}, nil)
		lb.AppendNull()
	})
	empty := parquetFuzzFile(tb, arrow.NewSchema([]arrow.Field{
		{Name: "a", Type: arrow.BinaryTypes.String},
	}, nil), pq.NewWriterProperties(), 0, func(*array.RecordBuilder) {})

	seeds := [][]byte{plain, dict, snappy, typed, empty, nil, []byte("PAR1PAR1")}
	for _, cut := range []int{4, len(plain) / 2, len(plain) - 8, len(plain) - 1} {
		seeds = append(seeds, plain[:cut])
	}
	return seeds
}

// FuzzParquetImport holds the Parquet importer — ImportJob.Predict,
// then ImportJob.Run with schema inference into a hermetic fs, then the
// embedded-overlay read — to the fuzz contract: no panic (the Parquet
// and pqarrow decoders are third-party code; their panics must surface
// as coded errors), a bounded run, every error a registered
// *errors.CodedError.
func FuzzParquetImport(f *testing.F) {
	for _, s := range parquetFuzzSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fuzzImportContract.Run(t, in, parquetFuzzImport)
	})
}

// fuzzImportContract caps one import's total heap allocation at 1 GiB:
// far above what an honest input of at most fuzzcontract.DefaultMaxInput
// bytes needs, far below what a decoder trusting a declared length would
// take.
var fuzzImportContract = fuzzcontract.Contract{MaxAlloc: 1 << 30}

func parquetFuzzImport(in []byte) error {
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
