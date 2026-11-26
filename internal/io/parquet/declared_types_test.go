package parquet

import (
	"context"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/io/csv"
)

// declaredParquet is one column per Arrow type class, three rows whose
// text alone would infer narrower or different types.
func declaredParquet(t *testing.T) []byte {
	t.Helper()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "small", Type: arrow.PrimitiveTypes.Uint16},
		{Name: "whole", Type: arrow.PrimitiveTypes.Float64},
		{Name: "signed", Type: arrow.PrimitiveTypes.Int32},
		{Name: "name", Type: arrow.BinaryTypes.String},
		{Name: "price", Type: &arrow.Decimal128Type{Precision: 10, Scale: 2}},
		{Name: "day", Type: arrow.FixedWidthTypes.Date32},
		{Name: "flag", Type: arrow.FixedWidthTypes.Boolean},
	}, nil)
	b := array.NewRecordBuilder(memory.NewGoAllocator(), sc)
	defer b.Release()
	for i := 1; i <= 3; i++ {
		b.Field(0).(*array.Uint16Builder).Append(uint16(i))
		b.Field(1).(*array.Float64Builder).Append(float64(i))
		b.Field(2).(*array.Int32Builder).Append(int32(i))
		b.Field(3).(*array.StringBuilder).Append("alpha")
		b.Field(4).(*array.Decimal128Builder).Append(decimal128.FromI64(int64(150 * i)))
		b.Field(5).(*array.Date32Builder).Append(arrow.Date32(18000 + i))
		b.Field(6).(*array.BooleanBuilder).Append(i%2 == 0)
	}
	rec := b.NewRecordBatch()
	defer rec.Release()
	return helperWriteParquet(t, sc, []arrow.RecordBatch{rec})
}

var declaredWant = map[string]encoding.FieldType{
	"small":  encoding.FieldTypeU16,           // pinned (text infers u4)
	"whole":  encoding.FieldTypeF64,           // pinned (text infers u4)
	"signed": encoding.FieldTypeU4,            // inferred: signed ints are not pinned
	"name":   encoding.FieldTypeCategoricalU8, // inferred
	"price":  encoding.FieldTypeDecimal128,    // pinned p10 s2 (text infers f64)
	"day":    encoding.FieldTypeDate,          // pinned
	"flag":   encoding.FieldTypePackedBool,    // pinned
}

// TestParquetImport_DeclaredWidthsArePinned: a width-declaring Parquet
// column keeps its declared type on import and convert (no promotion
// warning); signed ints and strings still infer; a column_type_overrides
// entry beats a pin.
func TestParquetImport_DeclaredWidthsArePinned(t *testing.T) {
	t.Parallel()

	data := declaredParquet(t)
	run := func(mutate func(*pio.ImportJob)) *pio.ImportReport {
		t.Helper()
		job := pio.NewImportJob(NewReaderFromBytes(data), "out.pulse")
		job.FS = afero.NewMemMapFs()
		if mutate != nil {
			mutate(job)
		}
		rep, err := job.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	rep := run(nil)
	for name, want := range declaredWant {
		if f := rep.Schema.Field(name); f == nil || f.Type != want {
			t.Errorf("%s: got %v, want %s", name, f, want)
		}
	}
	if p := rep.Schema.Field("price"); p.Precision != 10 || p.Scale != 2 {
		t.Errorf("price p%d s%d, want p10 s2", p.Precision, p.Scale)
	}
	if rep.RowsImported != 3 || len(rep.WidthWarnings) != 0 {
		t.Errorf("imported %d with width warnings %v, want 3 / none", rep.RowsImported, rep.WidthWarnings)
	}

	over := run(func(j *pio.ImportJob) {
		j.ColumnTypeOverrides = map[string]encoding.FieldType{"small": encoding.FieldTypeU32}
	})
	if f := over.Schema.Field("small"); f.Type != encoding.FieldTypeU32 {
		t.Errorf("overridden small = %s, want u32", f.Type)
	}

	cj := pio.NewConvertJob(NewReaderFromBytes(data), csv.NewWriterToBuffer())
	cj.FS = afero.NewMemMapFs()
	crep, err := cj.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range declaredWant {
		if f := crep.Schema.Field(name); f == nil || f.Type != want {
			t.Errorf("convert %s: got %v, want %s", name, f, want)
		}
	}
}

// TestParquet_U4PinRoundTrips: a u4 column the Parquet writer exports
// (a Uint8 carrying the pulse:type=u4 field metadata) reads back pinned
// u4 on import, while a plain Uint8 column pins u8. The marker survives
// only through the stored Arrow schema; a writer that drops it reads
// both columns back as u8.
func TestParquet_U4PinRoundTrips(t *testing.T) {
	t.Parallel()

	w := NewWriterToBuffer()
	w.SetPulseSchema(&encoding.Schema{Fields: []encoding.Field{
		{Name: "nib", Type: encoding.FieldTypeU4},
		{Name: "byte", Type: encoding.FieldTypeU8},
	}})
	if err := w.WriteHeader([]string{"nib", "byte"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{{"1", "1"}, {"2", "2"}} {
		if err := w.WriteRow(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	pins, err := NewReaderFromBytes(w.Bytes()).DeclaredColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if pins["nib"].Type != encoding.FieldTypeU4 || pins["byte"].Type != encoding.FieldTypeU8 {
		t.Errorf("pins nib=%s byte=%s, want u4 / u8", pins["nib"].Type, pins["byte"].Type)
	}

	job := pio.NewImportJob(NewReaderFromBytes(w.Bytes()), "out.pulse")
	job.FS = afero.NewMemMapFs()
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f := rep.Schema.Field("nib"); f == nil || f.Type != encoding.FieldTypeU4 {
		t.Errorf("imported nib = %v, want u4", f)
	}
	if rep.RowsImported != 2 {
		t.Errorf("imported %d rows, want 2", rep.RowsImported)
	}
}
