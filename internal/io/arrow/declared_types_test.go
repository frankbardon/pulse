package arrow

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
)

// declaredSchema is one column per Arrow type class: the width-declaring
// ones import pinned, the rest infer.
func declaredSchema() *arrow.Schema {
	return arrow.NewSchema([]arrow.Field{
		{Name: "small", Type: arrow.PrimitiveTypes.Uint16},
		{Name: "whole", Type: arrow.PrimitiveTypes.Float64},
		{Name: "single", Type: arrow.PrimitiveTypes.Float32},
		{Name: "signed", Type: arrow.PrimitiveTypes.Int32},
		{Name: "name", Type: arrow.BinaryTypes.String},
		{Name: "price", Type: &arrow.Decimal128Type{Precision: 10, Scale: 2}},
		{Name: "day", Type: arrow.FixedWidthTypes.Date32},
		{Name: "flag", Type: arrow.FixedWidthTypes.Boolean},
		{Name: "at", Type: arrow.FixedWidthTypes.Timestamp_s},
	}, nil)
}

// declaredBatch fills declaredSchema with three rows whose text alone
// would infer narrower or different types (u4, u4, …).
func declaredBatch(sc *arrow.Schema) arrow.RecordBatch {
	b := array.NewRecordBuilder(memory.NewGoAllocator(), sc)
	defer b.Release()
	for i := 1; i <= 3; i++ {
		b.Field(0).(*array.Uint16Builder).Append(uint16(i))
		b.Field(1).(*array.Float64Builder).Append(float64(i))
		b.Field(2).(*array.Float32Builder).Append(float32(i))
		b.Field(3).(*array.Int32Builder).Append(int32(i))
		b.Field(4).(*array.StringBuilder).Append("alpha")
		b.Field(5).(*array.Decimal128Builder).Append(decimal128.FromI64(int64(150 * i)))
		b.Field(6).(*array.Date32Builder).Append(arrow.Date32(18000 + i))
		b.Field(7).(*array.BooleanBuilder).Append(i%2 == 0)
		b.Field(8).(*array.TimestampBuilder).Append(arrow.Timestamp(1700000000 + i))
	}
	return b.NewRecordBatch()
}

// declaredWant is the type each declaredSchema column imports as.
var declaredWant = map[string]encoding.FieldType{
	"small":  encoding.FieldTypeU16,           // pinned (text infers u4)
	"whole":  encoding.FieldTypeF64,           // pinned (text infers u4)
	"single": encoding.FieldTypeF32,           // pinned (text infers u4)
	"signed": encoding.FieldTypeU4,            // inferred: signed ints are not pinned
	"name":   encoding.FieldTypeCategoricalU8, // inferred
	"price":  encoding.FieldTypeDecimal128,    // pinned p10 s2 (text infers f64)
	"day":    encoding.FieldTypeDate,          // pinned
	"flag":   encoding.FieldTypePackedBool,    // pinned
	"at":     encoding.FieldTypeDateTime,      // pinned
}

func importDeclared(t *testing.T, r pio.Reader, mutate func(*pio.ImportJob)) *pio.ImportReport {
	t.Helper()
	job := pio.NewImportJob(r, "out.pulse")
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

// TestArrowImport_DeclaredWidthsArePinned: a width-declaring Arrow column
// keeps its declared type on import (no promotion warning); signed ints
// and strings still infer; a column_type_overrides entry beats a pin.
func TestArrowImport_DeclaredWidthsArePinned(t *testing.T) {
	t.Parallel()

	sc := declaredSchema()
	rec := declaredBatch(sc)
	defer rec.Release()
	data := helperWriteArrow(t, sc, []arrow.RecordBatch{rec})

	rep := importDeclared(t, NewReaderFromBytes(data), nil)
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

	over := importDeclared(t, NewReaderFromBytes(data), func(j *pio.ImportJob) {
		j.ColumnTypeOverrides = map[string]encoding.FieldType{"small": encoding.FieldTypeU32}
	})
	if f := over.Schema.Field("small"); f.Type != encoding.FieldTypeU32 {
		t.Errorf("overridden small = %s, want u32", f.Type)
	}
}

// TestArrow_U4PinRoundTrips: Arrow has no 4-bit type, so FieldFromPulse
// marks a u4 column's Uint8 with pulse:type=u4 and the pin restores u4; a
// plain Uint8 pins u8.
func TestArrow_U4PinRoundTrips(t *testing.T) {
	t.Parallel()

	sc := arrow.NewSchema([]arrow.Field{
		FieldFromPulse(encoding.Field{Name: "nib", Type: encoding.FieldTypeU4}),
		{Name: "byte", Type: arrow.PrimitiveTypes.Uint8},
	}, nil)
	b := array.NewRecordBuilder(memory.NewGoAllocator(), sc)
	defer b.Release()
	b.Field(0).(*array.Uint8Builder).AppendValues([]uint8{1, 2}, nil)
	b.Field(1).(*array.Uint8Builder).AppendValues([]uint8{1, 2}, nil)
	rec := b.NewRecordBatch()
	defer rec.Release()

	pins, err := NewReaderFromBytes(helperWriteArrow(t, sc, []arrow.RecordBatch{rec})).DeclaredColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if pins["nib"].Type != encoding.FieldTypeU4 || pins["byte"].Type != encoding.FieldTypeU8 {
		t.Errorf("pins nib=%s byte=%s, want u4 / u8", pins["nib"].Type, pins["byte"].Type)
	}
}

// TestDeclaresWidth: exactly the width-declaring Arrow types pin.
func TestDeclaresWidth(t *testing.T) {
	t.Parallel()

	pinned := []arrow.DataType{
		arrow.PrimitiveTypes.Uint8, arrow.PrimitiveTypes.Uint16, arrow.PrimitiveTypes.Uint32, arrow.PrimitiveTypes.Uint64,
		arrow.PrimitiveTypes.Float32, arrow.PrimitiveTypes.Float64, arrow.FixedWidthTypes.Boolean,
		arrow.FixedWidthTypes.Date32, arrow.FixedWidthTypes.Date64, arrow.FixedWidthTypes.Timestamp_ms,
		&arrow.Decimal128Type{Precision: 5, Scale: 1},
	}
	inferred := []arrow.DataType{
		arrow.PrimitiveTypes.Int8, arrow.PrimitiveTypes.Int16, arrow.PrimitiveTypes.Int32, arrow.PrimitiveTypes.Int64,
		arrow.BinaryTypes.String, arrow.BinaryTypes.LargeString, arrow.BinaryTypes.Binary,
		&arrow.DictionaryType{IndexType: arrow.PrimitiveTypes.Int8, ValueType: arrow.BinaryTypes.String},
		arrow.ListOf(arrow.BinaryTypes.String), arrow.FixedSizeListOf(3, arrow.PrimitiveTypes.Float64),
	}
	for _, dt := range pinned {
		if !DeclaresWidth(dt) {
			t.Errorf("%s should pin", dt)
		}
	}
	for _, dt := range inferred {
		if DeclaresWidth(dt) {
			t.Errorf("%s should infer", dt)
		}
	}
}
