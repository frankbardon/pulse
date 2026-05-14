package arrow

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
)

// helperWriteArrow builds an Arrow IPC file from the given schema and batches.
func helperWriteArrow(t *testing.T, sc *arrow.Schema, batches []arrow.RecordBatch) []byte {
	t.Helper()
	var buf bytes.Buffer
	fw, err := ipc.NewFileWriter(&buf, ipc.WithSchema(sc))
	if err != nil {
		t.Fatalf("creating arrow writer: %v", err)
	}
	for _, rec := range batches {
		if err := fw.Write(rec); err != nil {
			t.Fatalf("writing batch: %v", err)
		}
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("closing arrow writer: %v", err)
	}
	return buf.Bytes()
}

// helperSimpleArrow builds a minimal Arrow IPC file with name/age/score columns.
func helperSimpleArrow(t *testing.T) []byte {
	t.Helper()
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "name", Type: arrow.BinaryTypes.String},
		{Name: "age", Type: arrow.PrimitiveTypes.Int32},
		{Name: "score", Type: arrow.PrimitiveTypes.Float64},
	}, nil)

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()

	bldr.Field(0).(*array.StringBuilder).AppendValues([]string{"alice", "bob", "charlie"}, nil)
	bldr.Field(1).(*array.Int32Builder).AppendValues([]int32{30, 25, 35}, nil)
	bldr.Field(2).(*array.Float64Builder).AppendValues([]float64{95.5, 88.0, 72.3}, nil)

	rec := bldr.NewRecordBatch()
	defer rec.Release()

	return helperWriteArrow(t, sc, []arrow.RecordBatch{rec})
}

func TestArrowReader_ReadHeader(t *testing.T) {
	data := helperSimpleArrow(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 3 {
		t.Fatalf("header len = %d, want 3", len(header))
	}
	if header[0] != "name" || header[1] != "age" || header[2] != "score" {
		t.Errorf("header = %v", header)
	}
}

func TestArrowReader_ReadRows(t *testing.T) {
	data := helperSimpleArrow(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}

	var rows [][]string
	err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0][0] != "alice" {
		t.Errorf("row[0][0] = %q, want alice", rows[0][0])
	}
	if rows[1][1] != "25" {
		t.Errorf("row[1][1] = %q, want 25", rows[1][1])
	}
}

func TestArrowReader_AllPrimitiveTypes(t *testing.T) {
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "col_uint8", Type: arrow.PrimitiveTypes.Uint8},
		{Name: "col_uint16", Type: arrow.PrimitiveTypes.Uint16},
		{Name: "col_uint32", Type: arrow.PrimitiveTypes.Uint32},
		{Name: "col_uint64", Type: arrow.PrimitiveTypes.Uint64},
		{Name: "col_float32", Type: arrow.PrimitiveTypes.Float32},
		{Name: "col_float64", Type: arrow.PrimitiveTypes.Float64},
		{Name: "col_bool", Type: arrow.FixedWidthTypes.Boolean},
		{Name: "col_date32", Type: arrow.FixedWidthTypes.Date32},
		{Name: "col_string", Type: arrow.BinaryTypes.String},
		{Name: "col_int32", Type: arrow.PrimitiveTypes.Int32},
	}, nil)

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()

	bldr.Field(0).(*array.Uint8Builder).Append(42)
	bldr.Field(1).(*array.Uint16Builder).Append(1000)
	bldr.Field(2).(*array.Uint32Builder).Append(100000)
	bldr.Field(3).(*array.Uint64Builder).Append(9999999999)
	bldr.Field(4).(*array.Float32Builder).Append(3.14)
	bldr.Field(5).(*array.Float64Builder).Append(2.71828)
	bldr.Field(6).(*array.BooleanBuilder).Append(true)
	bldr.Field(7).(*array.Date32Builder).Append(arrow.Date32(19000))
	bldr.Field(8).(*array.StringBuilder).Append("hello")
	bldr.Field(9).(*array.Int32Builder).Append(-5)

	rec := bldr.NewRecordBatch()
	defer rec.Release()

	data := helperWriteArrow(t, sc, []arrow.RecordBatch{rec})
	r := NewReaderFromBytes(data)
	defer r.Close()

	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}

	var rows [][]string
	err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row[0] != "42" {
		t.Errorf("uint8: got %q", row[0])
	}
	if row[1] != "1000" {
		t.Errorf("uint16: got %q", row[1])
	}
	if row[6] != "true" {
		t.Errorf("bool: got %q", row[6])
	}
	if row[7] == "" {
		t.Errorf("date32: empty")
	}
	if row[8] != "hello" {
		t.Errorf("string: got %q", row[8])
	}
}

func TestArrowReader_NullableColumns(t *testing.T) {
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "val", Type: arrow.PrimitiveTypes.Uint8, Nullable: true},
	}, nil)

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()
	b := bldr.Field(0).(*array.Uint8Builder)
	b.Append(10)
	b.AppendNull()
	b.Append(30)

	rec := bldr.NewRecordBatch()
	defer rec.Release()
	data := helperWriteArrow(t, sc, []arrow.RecordBatch{rec})

	r := NewReaderFromBytes(data)
	defer r.Close()
	r.ReadHeader()

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0][0] != "10" {
		t.Errorf("row 0 = %q", rows[0][0])
	}
	if rows[1][0] != "" {
		t.Errorf("row 1 = %q, want empty (null)", rows[1][0])
	}
	if rows[2][0] != "30" {
		t.Errorf("row 2 = %q", rows[2][0])
	}
}

func TestArrowReader_EmptyFile(t *testing.T) {
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "x", Type: arrow.PrimitiveTypes.Int32},
	}, nil)
	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()
	rec := bldr.NewRecordBatch()
	defer rec.Release()

	// File with one empty record batch.
	data := helperWriteArrow(t, sc, []arrow.RecordBatch{rec})
	r := NewReaderFromBytes(data)
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 1 || header[0] != "x" {
		t.Errorf("header = %v", header)
	}

	var rowCount int
	err = r.ReadRows(context.Background(), func(row []string) error {
		rowCount++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("got %d rows, want 0", rowCount)
	}
}

func TestArrowReader_Reset(t *testing.T) {
	data := helperSimpleArrow(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	var count1 int
	r.ReadRows(context.Background(), func(row []string) error {
		count1++
		return nil
	})
	if count1 != 3 {
		t.Fatalf("first read: %d", count1)
	}

	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	header, _ := r.ReadHeader()
	if len(header) != 3 {
		t.Fatalf("post-reset header len = %d", len(header))
	}
	var count2 int
	r.ReadRows(context.Background(), func(row []string) error {
		count2++
		return nil
	})
	if count2 != 3 {
		t.Errorf("post-reset row count = %d", count2)
	}
}

func TestArrowReader_StopIteration(t *testing.T) {
	data := helperSimpleArrow(t)
	r := NewReaderFromBytes(data)
	defer r.Close()
	r.ReadHeader()

	var count int
	err := r.ReadRows(context.Background(), func(row []string) error {
		count++
		if count >= 2 {
			return pio.ErrStopIteration()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d, want 2", count)
	}
}

func TestArrowReader_ContextCancel(t *testing.T) {
	data := helperSimpleArrow(t)
	r := NewReaderFromBytes(data)
	defer r.Close()
	r.ReadHeader()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := r.ReadRows(ctx, func(row []string) error { return nil })
	if err != context.Canceled {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestArrowReader_ReadRowsWithoutReadHeader(t *testing.T) {
	data := helperSimpleArrow(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	var rowCount int
	err := r.ReadRows(context.Background(), func(row []string) error {
		rowCount++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if rowCount != 3 {
		t.Errorf("got %d rows, want 3", rowCount)
	}
}

func TestArrowReader_ReadRowsCallbackError(t *testing.T) {
	data := helperSimpleArrow(t)
	r := NewReaderFromBytes(data)
	defer r.Close()
	r.ReadHeader()

	expectedErr := fmt.Errorf("test error")
	err := r.ReadRows(context.Background(), func(row []string) error {
		return expectedErr
	})
	if err != expectedErr {
		t.Errorf("got %v, want %v", err, expectedErr)
	}
}

func TestArrowReader_FromFilesystem(t *testing.T) {
	data := helperSimpleArrow(t)
	fs := afero.NewMemMapFs()
	afero.WriteFile(fs, "test.arrow", data, 0644)

	r := NewReader(fs, "test.arrow")
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 3 {
		t.Fatalf("header len = %d", len(header))
	}
}

func TestArrowReader_NoDataSource(t *testing.T) {
	r := &Reader{}
	if _, err := r.ReadHeader(); err == nil {
		t.Fatal("expected error for no data source")
	}
}

func TestArrowReader_FileNotFound(t *testing.T) {
	fs := afero.NewMemMapFs()
	r := NewReader(fs, "nonexistent.arrow")
	if _, err := r.ReadHeader(); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestArrowReader_InvalidData(t *testing.T) {
	r := NewReaderFromBytes([]byte("not an arrow file"))
	if _, err := r.ReadHeader(); err == nil {
		t.Fatal("expected error for invalid data")
	}
}

func TestArrowReader_InferPulseSchema(t *testing.T) {
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "small_int", Type: arrow.PrimitiveTypes.Uint8},
		{Name: "big_float", Type: arrow.PrimitiveTypes.Float64},
		{Name: "day", Type: arrow.FixedWidthTypes.Date32},
		{Name: "flag", Type: arrow.FixedWidthTypes.Boolean},
		{Name: "label", Type: arrow.BinaryTypes.String},
	}, nil)

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()
	bldr.Field(0).(*array.Uint8Builder).Append(5)
	bldr.Field(1).(*array.Float64Builder).Append(3.14)
	bldr.Field(2).(*array.Date32Builder).Append(arrow.Date32(18000))
	bldr.Field(3).(*array.BooleanBuilder).Append(true)
	bldr.Field(4).(*array.StringBuilder).Append("x")

	rec := bldr.NewRecordBatch()
	defer rec.Release()
	data := helperWriteArrow(t, sc, []arrow.RecordBatch{rec})

	r := NewReaderFromBytes(data)
	defer r.Close()

	ps, err := r.InferPulseSchema()
	if err != nil {
		t.Fatalf("InferPulseSchema: %v", err)
	}
	if f := ps.Field("small_int"); f == nil || f.Type != encoding.FieldTypeU8 {
		t.Errorf("small_int: %v", f)
	}
	if f := ps.Field("big_float"); f == nil || f.Type != encoding.FieldTypeF64 {
		t.Errorf("big_float: %v", f)
	}
	if f := ps.Field("day"); f == nil || f.Type != encoding.FieldTypeDate {
		t.Errorf("day: %v", f)
	}
	if f := ps.Field("flag"); f == nil || f.Type != encoding.FieldTypePackedBool {
		t.Errorf("flag: %v", f)
	}
	if f := ps.Field("label"); f == nil || !f.Type.IsCategorical() {
		t.Errorf("label: %v", f)
	}
}

func TestArrowWriter_WriteRows(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "output.arrow")
	if err := w.WriteHeader([]string{"name", "value"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{"alice", "42"}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.WriteRow([]any{"bob", "99"}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := afero.ReadFile(fs, "output.arrow")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	r := NewReaderFromBytes(data)
	defer r.Close()
	header, _ := r.ReadHeader()
	if len(header) != 2 {
		t.Fatalf("header len = %d", len(header))
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0][0] != "alice" || rows[0][1] != "42" {
		t.Errorf("row 0 = %v", rows[0])
	}
}

func TestArrowWriter_Buffer(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"x"})
	w.WriteRow([]any{"hello"})
	w.Close()

	data := w.Bytes()
	if len(data) < 6 || string(data[:6]) != "ARROW1" {
		head := data
		if len(head) > 6 {
			head = head[:6]
		}
		t.Errorf("invalid Arrow IPC magic: %q", head)
	}
}

func TestArrowWriter_HeaderOnly(t *testing.T) {
	w := NewWriterToBuffer()
	if err := w.WriteHeader([]string{"a", "b"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := NewReaderFromBytes(w.Bytes())
	defer r.Close()
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 2 || header[0] != "a" || header[1] != "b" {
		t.Errorf("header = %v", header)
	}
	var rowCount int
	r.ReadRows(context.Background(), func(row []string) error {
		rowCount++
		return nil
	})
	if rowCount != 0 {
		t.Errorf("got %d rows, want 0", rowCount)
	}
}

func TestArrowWriter_EmptyClose(t *testing.T) {
	w := NewWriterToBuffer()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(w.Bytes()) != 0 {
		t.Errorf("expected empty bytes, got %d", len(w.Bytes()))
	}
}

func TestArrowWriter_WriteRowBeforeHeader(t *testing.T) {
	w := NewWriterToBuffer()
	if err := w.WriteRow([]any{"x"}); err == nil {
		t.Fatal("expected error: WriteRow before WriteHeader")
	}
}

// TestArrowWriter_BatchBoundaries writes more than defaultRecordBatchSize rows
// to verify the writer flushes intermediate batches without splitting rows.
func TestArrowWriter_BatchBoundaries(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"id"})

	const total = defaultRecordBatchSize + 100
	for i := range total {
		if err := w.WriteRow([]any{i}); err != nil {
			t.Fatalf("WriteRow %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := NewReaderFromBytes(w.Bytes())
	defer r.Close()

	var rowCount int
	err := r.ReadRows(context.Background(), func(row []string) error {
		rowCount++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if rowCount != total {
		t.Errorf("got %d rows, want %d", rowCount, total)
	}
}

func TestArrowImportExportRoundTrip(t *testing.T) {
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "age", Type: arrow.PrimitiveTypes.Uint8},
		{Name: "score", Type: arrow.PrimitiveTypes.Float64},
	}, nil)

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()
	bldr.Field(0).(*array.Uint8Builder).AppendValues([]uint8{10, 20, 30}, nil)
	bldr.Field(1).(*array.Float64Builder).AppendValues([]float64{95.5, 88.0, 72.3}, nil)
	rec := bldr.NewRecordBatch()
	defer rec.Release()
	data := helperWriteArrow(t, sc, []arrow.RecordBatch{rec})

	reader := NewReaderFromBytes(data)
	fs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "age", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 1},
		},
	}

	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs
	importJob.Schema = schema

	importReport, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if importReport.RowsImported != 3 {
		t.Errorf("imported %d, want 3", importReport.RowsImported)
	}

	writer := NewWriterToBuffer()
	exportJob := pio.NewExportJob("test.pulse", writer)
	exportJob.FS = fs

	exportReport, err := exportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if exportReport.RowsExported != 3 {
		t.Errorf("exported %d, want 3", exportReport.RowsExported)
	}
	writer.Close()

	r2 := NewReaderFromBytes(writer.Bytes())
	defer r2.Close()
	header, _ := r2.ReadHeader()
	if len(header) != 2 {
		t.Fatalf("header len = %d", len(header))
	}
	var rows [][]string
	r2.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if len(rows) != 3 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0][0] != "10" {
		t.Errorf("row[0][0] = %q", rows[0][0])
	}
}

func TestConvertJob_CsvToArrow(t *testing.T) {
	csvData := "age,name\n10,alice\n20,bob\n30,charlie\n"
	csvReader := csv.NewReaderFromBytes([]byte(csvData))
	arrWriter := NewWriterToBuffer()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "age", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
			{Name: "name", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 1},
		},
	}

	job := pio.NewConvertJob(csvReader, arrWriter)
	job.Schema = schema

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 3 {
		t.Errorf("converted %d, want 3", report.RowsConverted)
	}
	arrWriter.Close()

	r := NewReaderFromBytes(arrWriter.Bytes())
	defer r.Close()
	header, _ := r.ReadHeader()
	if len(header) != 2 {
		t.Fatalf("header len = %d", len(header))
	}
	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0][0] != "10" || rows[0][1] != "alice" {
		t.Errorf("row 0 = %v", rows[0])
	}
}

func TestConvertJob_ArrowToCsv(t *testing.T) {
	arrData := helperSimpleArrow(t)
	arrReader := NewReaderFromBytes(arrData)
	csvWriter := csv.NewWriterToBuffer()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "name", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 0},
			{Name: "age", Type: encoding.FieldTypeU8, CsvColumnIdx: 1},
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 2},
		},
	}

	job := pio.NewConvertJob(arrReader, csvWriter)
	job.Schema = schema

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 3 {
		t.Errorf("converted %d, want 3", report.RowsConverted)
	}
	csvWriter.Close()

	output := string(csvWriter.Bytes())
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if lines[0] != "name,age,score" {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "alice,") {
		t.Errorf("line 1 = %q", lines[1])
	}
}

func TestArrowTypeToPulseType(t *testing.T) {
	tests := []struct {
		arrowType arrow.DataType
		want      encoding.FieldType
	}{
		{arrow.PrimitiveTypes.Uint8, encoding.FieldTypeU8},
		{arrow.PrimitiveTypes.Uint16, encoding.FieldTypeU16},
		{arrow.PrimitiveTypes.Uint32, encoding.FieldTypeU32},
		{arrow.PrimitiveTypes.Uint64, encoding.FieldTypeU64},
		{arrow.PrimitiveTypes.Float32, encoding.FieldTypeF32},
		{arrow.PrimitiveTypes.Float64, encoding.FieldTypeF64},
		{arrow.FixedWidthTypes.Date32, encoding.FieldTypeDate},
		{arrow.FixedWidthTypes.Date64, encoding.FieldTypeDate},
		{arrow.FixedWidthTypes.Boolean, encoding.FieldTypePackedBool},
		{arrow.PrimitiveTypes.Int8, encoding.FieldTypeU8},
		{arrow.PrimitiveTypes.Int16, encoding.FieldTypeU16},
		{arrow.PrimitiveTypes.Int32, encoding.FieldTypeU32},
		{arrow.PrimitiveTypes.Int64, encoding.FieldTypeU64},
		{arrow.BinaryTypes.String, encoding.FieldTypeCategoricalU8},
		{arrow.BinaryTypes.LargeString, encoding.FieldTypeCategoricalU8},
	}

	for _, tt := range tests {
		t.Run(tt.arrowType.Name(), func(t *testing.T) {
			got := TypeToPulse(tt.arrowType, false)
			if got != tt.want {
				t.Errorf("TypeToPulse(%v) = %s, want %s", tt.arrowType, got, tt.want)
			}
		})
	}
}

func TestArrowTypeToPulse_NullableCases(t *testing.T) {
	if got := TypeToPulse(arrow.PrimitiveTypes.Uint8, true); got != encoding.FieldTypeNullableU8 {
		t.Errorf("nullable uint8: %s", got)
	}
	if got := TypeToPulse(arrow.PrimitiveTypes.Uint16, true); got != encoding.FieldTypeNullableU16 {
		t.Errorf("nullable uint16: %s", got)
	}
	if got := TypeToPulse(arrow.FixedWidthTypes.Boolean, true); got != encoding.FieldTypeNullableBool {
		t.Errorf("nullable bool: %s", got)
	}
}

func TestArrowTypeToPulse_DefaultCase(t *testing.T) {
	got := TypeToPulse(arrow.FixedWidthTypes.Time32ms, false)
	if got != encoding.FieldTypeF64 {
		t.Errorf("default: got %s, want f64", got)
	}
}

func TestArrowTypeToPulse_Dictionary(t *testing.T) {
	dt := &arrow.DictionaryType{
		IndexType: arrow.PrimitiveTypes.Int32,
		ValueType: arrow.BinaryTypes.String,
	}
	got := TypeToPulse(dt, false)
	if got != encoding.FieldTypeCategoricalU8 {
		t.Errorf("dictionary: got %s", got)
	}
}

func TestPulseTypeToArrowType(t *testing.T) {
	tests := []struct {
		pulseType encoding.FieldType
		wantID    arrow.Type
	}{
		{encoding.FieldTypeU8, arrow.UINT8},
		{encoding.FieldTypeU16, arrow.UINT16},
		{encoding.FieldTypeU32, arrow.UINT32},
		{encoding.FieldTypeU64, arrow.UINT64},
		{encoding.FieldTypeF32, arrow.FLOAT32},
		{encoding.FieldTypeF64, arrow.FLOAT64},
		{encoding.FieldTypeDate, arrow.DATE32},
		{encoding.FieldTypePackedBool, arrow.BOOL},
		{encoding.FieldTypeNullableBool, arrow.BOOL},
		{encoding.FieldTypeNullableU4, arrow.UINT8},
		{encoding.FieldTypeNullableU8, arrow.UINT8},
		{encoding.FieldTypeNullableU16, arrow.UINT16},
		{encoding.FieldTypeCategoricalU8, arrow.STRING},
		{encoding.FieldTypeCategoricalU16, arrow.STRING},
		{encoding.FieldTypeCategoricalU32, arrow.STRING},
	}
	for _, tt := range tests {
		t.Run(tt.pulseType.String(), func(t *testing.T) {
			got := TypeFromPulse(tt.pulseType)
			if got.ID() != tt.wantID {
				t.Errorf("TypeFromPulse(%s) = %v", tt.pulseType, got)
			}
		})
	}
}

func TestPulseTypeToArrow_DefaultCase(t *testing.T) {
	got := TypeFromPulse(encoding.FieldType(255))
	if got.ID() != arrow.FLOAT64 {
		t.Errorf("default: %v", got)
	}
}
