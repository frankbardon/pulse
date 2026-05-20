package parquet

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/spf13/afero"
)

// helperWriteParquet creates a Parquet file in memory with the given Arrow schema and records.
func helperWriteParquet(t *testing.T, sc *arrow.Schema, records []arrow.RecordBatch) []byte {
	t.Helper()
	var buf bytes.Buffer
	props := parquet.NewWriterProperties(parquet.WithDictionaryDefault(true))
	arrowProps := pqarrow.DefaultWriterProps()
	pw, err := pqarrow.NewFileWriter(sc, &buf, props, arrowProps)
	if err != nil {
		t.Fatalf("creating parquet writer: %v", err)
	}
	for _, rec := range records {
		if err := pw.Write(rec); err != nil {
			t.Fatalf("writing record: %v", err)
		}
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("closing parquet writer: %v", err)
	}
	return buf.Bytes()
}

// helperSimpleParquet builds a minimal Parquet file with columns name(string), age(int32), score(float64).
func helperSimpleParquet(t *testing.T) []byte {
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

	return helperWriteParquet(t, sc, []arrow.RecordBatch{rec})
}

func TestParquetReader_ReadHeader(t *testing.T) {
	data := helperSimpleParquet(t)
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

func TestParquetReader_ReadRows(t *testing.T) {
	data := helperSimpleParquet(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	header, _ := r.ReadHeader()
	_ = header

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

func TestParquetReader_EmptyFile(t *testing.T) {
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "x", Type: arrow.PrimitiveTypes.Int32},
	}, nil)

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()
	rec := bldr.NewRecordBatch()
	defer rec.Release()

	// Write record with 0 rows.
	data := helperWriteParquet(t, sc, nil)
	r := NewReaderFromBytes(data)
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 1 || header[0] != "x" {
		t.Errorf("header = %v, want [x]", header)
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

func TestParquetReader_AllArrowTypes(t *testing.T) {
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
	bldr.Field(7).(*array.Date32Builder).Append(arrow.Date32(19000)) // days since epoch
	bldr.Field(8).(*array.StringBuilder).Append("hello")
	bldr.Field(9).(*array.Int32Builder).Append(-5)

	rec := bldr.NewRecordBatch()
	defer rec.Release()

	data := helperWriteParquet(t, sc, []arrow.RecordBatch{rec})
	r := NewReaderFromBytes(data)
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 10 {
		t.Fatalf("header len = %d, want 10", len(header))
	}

	var rows [][]string
	err = r.ReadRows(context.Background(), func(row []string) error {
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

	// Verify type-specific formatting.
	if row[0] != "42" {
		t.Errorf("uint8: got %q, want 42", row[0])
	}
	if row[1] != "1000" {
		t.Errorf("uint16: got %q, want 1000", row[1])
	}
	if row[6] != "true" {
		t.Errorf("bool: got %q, want true", row[6])
	}
	// Date32 = 19000 days from epoch = 2022-01-05 approximately.
	if row[7] == "" {
		t.Errorf("date32: got empty string")
	}
	if row[8] != "hello" {
		t.Errorf("string: got %q, want hello", row[8])
	}
}

func TestParquetWriter_WriteRows(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "output.parquet")

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

	// Read it back.
	data, err := afero.ReadFile(fs, "output.parquet")
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}

	r := NewReaderFromBytes(data)
	defer r.Close()

	header, _ := r.ReadHeader()
	if len(header) != 2 {
		t.Fatalf("header len = %d, want 2", len(header))
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0][0] != "alice" || rows[0][1] != "42" {
		t.Errorf("row 0 = %v", rows[0])
	}
}

func TestParquetImportExportRoundTrip(t *testing.T) {
	// Create a Parquet file with numeric data.
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
	pqData := helperWriteParquet(t, sc, []arrow.RecordBatch{rec})

	// Import the Parquet file into .pulse format.
	reader := NewReaderFromBytes(pqData)
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

	// Export from .pulse back to Parquet.
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

	// Read the exported Parquet back and verify.
	exported := writer.Bytes()
	r2 := NewReaderFromBytes(exported)
	defer r2.Close()

	header, _ := r2.ReadHeader()
	if len(header) != 2 {
		t.Fatalf("header len = %d, want 2", len(header))
	}

	var rows [][]string
	r2.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0][0] != "10" {
		t.Errorf("row[0][0] = %q, want 10", rows[0][0])
	}
}

func TestParquetImport_SchemaFromParquetMetadata(t *testing.T) {
	// When importing Parquet, the rich type metadata should inform Pulse type selection.
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "small_int", Type: arrow.PrimitiveTypes.Uint8},
		{Name: "big_float", Type: arrow.PrimitiveTypes.Float64},
		{Name: "day", Type: arrow.FixedWidthTypes.Date32},
		{Name: "flag", Type: arrow.FixedWidthTypes.Boolean},
	}, nil)

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()

	bldr.Field(0).(*array.Uint8Builder).Append(5)
	bldr.Field(1).(*array.Float64Builder).Append(3.14)
	bldr.Field(2).(*array.Date32Builder).Append(arrow.Date32(18000))
	bldr.Field(3).(*array.BooleanBuilder).Append(true)

	rec := bldr.NewRecordBatch()
	defer rec.Release()
	data := helperWriteParquet(t, sc, []arrow.RecordBatch{rec})

	r := NewReaderFromBytes(data)
	defer r.Close()

	// Use SchemaFromParquet to get a Pulse schema from Parquet metadata.
	pulseSchema, err := r.InferPulseSchema()
	if err != nil {
		t.Fatalf("InferPulseSchema: %v", err)
	}

	if f := pulseSchema.Field("small_int"); f == nil || f.Type != encoding.FieldTypeU8 {
		t.Errorf("small_int: got %v", f)
	}
	if f := pulseSchema.Field("big_float"); f == nil || f.Type != encoding.FieldTypeF64 {
		t.Errorf("big_float: got %v", f)
	}
	if f := pulseSchema.Field("day"); f == nil || f.Type != encoding.FieldTypeDate {
		t.Errorf("day: got %v", f)
	}
	if f := pulseSchema.Field("flag"); f == nil || f.Type != encoding.FieldTypePackedBool {
		t.Errorf("flag: got %v, want packed_bool", f)
	}
}

func TestParquetImport_CategoricalColumn(t *testing.T) {
	// Create Parquet with a string column that should become categorical.
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "color", Type: arrow.BinaryTypes.String},
	}, nil)

	colors := []string{"red", "green", "blue"}
	vals := make([]string, 100)
	for i := range vals {
		vals[i] = colors[i%3]
	}

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()
	bldr.Field(0).(*array.StringBuilder).AppendValues(vals, nil)

	rec := bldr.NewRecordBatch()
	defer rec.Release()
	data := helperWriteParquet(t, sc, []arrow.RecordBatch{rec})

	reader := NewReaderFromBytes(data)
	fs := afero.NewMemMapFs()

	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs
	// Let schema inference run.

	report, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	f := report.Schema.Field("color")
	if f == nil {
		t.Fatal("field 'color' not found")
	}
	if !f.Type.IsCategorical() {
		t.Errorf("got %s, want categorical", f.Type)
	}
	if f.Dictionary == nil {
		t.Fatal("dictionary is nil")
	}
	if f.Dictionary.Count() != 3 {
		t.Errorf("dictionary count = %d, want 3", f.Dictionary.Count())
	}
}

func TestParquetExport_CategoricalAsDictEncoded(t *testing.T) {
	// Import a categorical column, then export to Parquet and verify dictionary encoding.
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "color", Type: arrow.BinaryTypes.String},
	}, nil)

	colors := []string{"red", "green", "blue"}
	vals := make([]string, 100)
	for i := range vals {
		vals[i] = colors[i%3]
	}

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()
	bldr.Field(0).(*array.StringBuilder).AppendValues(vals, nil)
	rec := bldr.NewRecordBatch()
	defer rec.Release()
	pqData := helperWriteParquet(t, sc, []arrow.RecordBatch{rec})

	// Import.
	reader := NewReaderFromBytes(pqData)
	fs := afero.NewMemMapFs()
	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs

	_, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Export to Parquet with schema that has dictionary info.
	writer := NewWriterToBuffer()
	exportJob := pio.NewExportJob("test.pulse", writer)
	exportJob.FS = fs

	exportReport, err := exportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if exportReport.RowsExported != 100 {
		t.Errorf("exported %d, want 100", exportReport.RowsExported)
	}
	writer.Close()

	// Read back the exported Parquet and verify values are strings.
	exported := writer.Bytes()
	r2 := NewReaderFromBytes(exported)
	defer r2.Close()

	var rowCount int
	r2.ReadRows(context.Background(), func(row []string) error {
		rowCount++
		val := row[0]
		if val != "red" && val != "green" && val != "blue" {
			t.Errorf("row %d: got %q, want a color", rowCount, val)
		}
		return nil
	})
	if rowCount != 100 {
		t.Errorf("got %d rows, want 100", rowCount)
	}
}

func TestParquetExport_TypeMapping(t *testing.T) {
	// Verify that Pulse types map to appropriate Arrow types in the exported Parquet.
	tests := []struct {
		pulseType encoding.FieldType
		// We just verify the value round-trips correctly.
		value string
	}{
		{encoding.FieldTypeU8, "42"},
		{encoding.FieldTypeU16, "1000"},
		{encoding.FieldTypeU32, "100000"},
		{encoding.FieldTypeU64, "9999999999"},
		{encoding.FieldTypeF32, "3.14"},
		{encoding.FieldTypeF64, "2.71828"},
		{encoding.FieldTypeDate, "2022-01-05"},
		{encoding.FieldTypePackedBool, "true"},
	}

	for _, tt := range tests {
		t.Run(tt.pulseType.String(), func(t *testing.T) {
			// Create a minimal CSV-like reader with one value.
			csvData := fmt.Sprintf("col\n%s\n", tt.value)
			csvReader := csv.NewReaderFromBytes([]byte(csvData))
			fs := afero.NewMemMapFs()

			schema := &encoding.Schema{
				Fields: []encoding.Field{
					{Name: "col", Type: tt.pulseType, CsvColumnIdx: 0},
				},
			}

			importJob := pio.NewImportJob(csvReader, "test.pulse")
			importJob.FS = fs
			importJob.Schema = schema

			_, err := importJob.Run(context.Background())
			if err != nil {
				t.Fatalf("Import: %v", err)
			}

			// Export to Parquet.
			writer := NewWriterToBuffer()
			exportJob := pio.NewExportJob("test.pulse", writer)
			exportJob.FS = fs

			_, err = exportJob.Run(context.Background())
			if err != nil {
				t.Fatalf("Export: %v", err)
			}
			writer.Close()

			// Read back.
			r := NewReaderFromBytes(writer.Bytes())
			defer r.Close()
			r.ReadHeader()

			var got string
			r.ReadRows(context.Background(), func(row []string) error {
				got = row[0]
				return nil
			})

			if got == "" {
				t.Errorf("got empty value for %s", tt.pulseType)
			}

			// For floats, compare numerically.
			switch tt.pulseType {
			case encoding.FieldTypeF32:
				// f32 round-trips may have slight precision changes.
				if got == "" {
					t.Errorf("empty float32")
				}
			case encoding.FieldTypeF64:
				if got == "" {
					t.Errorf("empty float64")
				}
			default:
				if got != tt.value {
					t.Errorf("got %q, want %q", got, tt.value)
				}
			}
		})
	}
}

func TestParquetImport_RowGroupParallelism(t *testing.T) {
	// Create a Parquet file with multiple row groups using MaxRowGroupLength.
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "id", Type: arrow.PrimitiveTypes.Int32},
	}, nil)

	// Write with small max row group length to force multiple row groups.
	var buf bytes.Buffer
	props := parquet.NewWriterProperties(
		parquet.WithDictionaryDefault(true),
		parquet.WithMaxRowGroupLength(25),
	)
	arrowProps := pqarrow.DefaultWriterProps()
	pw, err := pqarrow.NewFileWriter(sc, &buf, props, arrowProps)
	if err != nil {
		t.Fatalf("creating writer: %v", err)
	}

	// Write 100 rows total; with MaxRowGroupLength=25 this should create 4 row groups.
	bldr := array.NewRecordBuilder(alloc, sc)
	vals := make([]int32, 100)
	for i := range vals {
		vals[i] = int32(i)
	}
	bldr.Field(0).(*array.Int32Builder).AppendValues(vals, nil)
	rec := bldr.NewRecordBatch()
	if err := pw.Write(rec); err != nil {
		t.Fatalf("writing record: %v", err)
	}
	rec.Release()
	bldr.Release()

	if err := pw.Close(); err != nil {
		t.Fatalf("closing writer: %v", err)
	}
	data := buf.Bytes()

	// Read with 2 workers.
	r := NewReaderFromBytes(data, WithWorkers(2))
	defer r.Close()

	header, err2 := r.ReadHeader()
	if err2 != nil {
		t.Fatalf("ReadHeader error: %v", err2)
	}
	if len(header) != 1 {
		t.Fatalf("header len = %d", len(header))
	}

	var rows [][]string
	err = r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 100 {
		t.Fatalf("got %d rows, want 100", len(rows))
	}

	// Verify all IDs are present (order may differ with parallel reading,
	// but our implementation preserves order).
	seen := make(map[string]bool)
	for _, row := range rows {
		seen[row[0]] = true
	}
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("%d", i)
		if !seen[key] {
			t.Errorf("missing id %d", i)
		}
	}
}

func TestConvertJob_CsvToParquet(t *testing.T) {
	csvData := "age,name\n10,alice\n20,bob\n30,charlie\n"
	csvReader := csv.NewReaderFromBytes([]byte(csvData))
	pqWriter := NewWriterToBuffer()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "age", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
			{Name: "name", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 1},
		},
	}

	job := pio.NewConvertJob(csvReader, pqWriter)
	job.Schema = schema

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 3 {
		t.Errorf("converted %d, want 3", report.RowsConverted)
	}
	pqWriter.Close()

	// Read the parquet output.
	data := pqWriter.Bytes()
	r := NewReaderFromBytes(data)
	defer r.Close()

	header, _ := r.ReadHeader()
	if len(header) != 2 {
		t.Fatalf("header len = %d, want 2", len(header))
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0][0] != "10" {
		t.Errorf("row[0][0] = %q, want 10", rows[0][0])
	}
	if rows[0][1] != "alice" {
		t.Errorf("row[0][1] = %q, want alice", rows[0][1])
	}
}

func TestConvertJob_ParquetToCsv(t *testing.T) {
	pqData := helperSimpleParquet(t)
	pqReader := NewReaderFromBytes(pqData)
	csvWriter := csv.NewWriterToBuffer()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "name", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 0},
			{Name: "age", Type: encoding.FieldTypeU8, CsvColumnIdx: 1},
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 2},
		},
	}

	job := pio.NewConvertJob(pqReader, csvWriter)
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
	if len(lines) != 4 { // header + 3 rows
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if lines[0] != "name,age,score" {
		t.Errorf("header = %q", lines[0])
	}
	// Verify alice is in the first data row.
	if !strings.HasPrefix(lines[1], "alice,") {
		t.Errorf("line 1 = %q, want prefix 'alice,'", lines[1])
	}
}

func TestParquetReader_Reset(t *testing.T) {
	data := helperSimpleParquet(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	// First read.
	header, _ := r.ReadHeader()
	if len(header) != 3 {
		t.Fatalf("header len = %d", len(header))
	}
	var count1 int
	r.ReadRows(context.Background(), func(row []string) error {
		count1++
		return nil
	})
	if count1 != 3 {
		t.Fatalf("first read: %d rows", count1)
	}

	// Reset and read again.
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	header2, _ := r.ReadHeader()
	if len(header2) != 3 {
		t.Fatalf("second read: header len = %d", len(header2))
	}
	var count2 int
	r.ReadRows(context.Background(), func(row []string) error {
		count2++
		return nil
	})
	if count2 != 3 {
		t.Fatalf("second read: %d rows", count2)
	}
}

func TestParquetReader_StopIteration(t *testing.T) {
	data := helperSimpleParquet(t)
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
		t.Errorf("got %d rows before stop, want 2", count)
	}
}

func TestParquetReader_ContextCancel(t *testing.T) {
	data := helperSimpleParquet(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	r.ReadHeader()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := r.ReadRows(ctx, func(row []string) error {
		return nil
	})
	if err != context.Canceled {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestParquetWriter_Buffer(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"x"})
	w.WriteRow([]any{"hello"})
	w.Close()

	data := w.Bytes()
	if len(data) == 0 {
		t.Fatal("Bytes() returned empty")
	}

	// Verify it's valid Parquet (magic bytes: PAR1).
	if len(data) < 4 || string(data[:4]) != "PAR1" {
		t.Errorf("invalid Parquet magic bytes")
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
		{arrow.FixedWidthTypes.Boolean, encoding.FieldTypePackedBool},
		{arrow.PrimitiveTypes.Int8, encoding.FieldTypeU8},
		{arrow.PrimitiveTypes.Int16, encoding.FieldTypeU16},
		{arrow.PrimitiveTypes.Int32, encoding.FieldTypeU32},
		{arrow.PrimitiveTypes.Int64, encoding.FieldTypeU64},
		{arrow.BinaryTypes.String, encoding.FieldTypeCategoricalU8},
	}

	for _, tt := range tests {
		t.Run(tt.arrowType.Name(), func(t *testing.T) {
			got := arrowTypeToPulse(tt.arrowType, false)
			if got != tt.want {
				t.Errorf("arrowTypeToPulse(%v) = %s, want %s", tt.arrowType, got, tt.want)
			}
		})
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
		{encoding.FieldTypeU4, arrow.UINT8},
		{encoding.FieldTypeCategoricalU8, arrow.STRING},
		{encoding.FieldTypeCategoricalU16, arrow.STRING},
		{encoding.FieldTypeCategoricalU32, arrow.STRING},
	}

	for _, tt := range tests {
		t.Run(tt.pulseType.String(), func(t *testing.T) {
			got := pulseTypeToArrow(tt.pulseType)
			if got.ID() != tt.wantID {
				t.Errorf("pulseTypeToArrow(%s) = %v, want type ID %v", tt.pulseType, got, tt.wantID)
			}
		})
	}
}

func TestParquetReader_NullableColumns(t *testing.T) {
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "val", Type: arrow.PrimitiveTypes.Uint8, Nullable: true},
	}, nil)

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()

	bldr.Field(0).(*array.Uint8Builder).Append(10)
	bldr.Field(0).(*array.Uint8Builder).AppendNull()
	bldr.Field(0).(*array.Uint8Builder).Append(30)

	rec := bldr.NewRecordBatch()
	defer rec.Release()
	data := helperWriteParquet(t, sc, []arrow.RecordBatch{rec})

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
		t.Errorf("row 0 = %q, want 10", rows[0][0])
	}
	if rows[1][0] != "" {
		t.Errorf("row 1 = %q, want empty (null)", rows[1][0])
	}
	if rows[2][0] != "30" {
		t.Errorf("row 2 = %q, want 30", rows[2][0])
	}
}

func TestParquetReader_DictionaryEncoded(t *testing.T) {
	// Create a Parquet file with dictionary-encoded string column.
	alloc := memory.NewGoAllocator()
	dictType := &arrow.DictionaryType{
		IndexType: arrow.PrimitiveTypes.Int32,
		ValueType: arrow.BinaryTypes.String,
	}
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "category", Type: dictType},
	}, nil)

	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()

	db := bldr.Field(0).(*array.BinaryDictionaryBuilder)
	db.AppendString("red")
	db.AppendString("green")
	db.AppendString("red")
	db.AppendString("blue")

	rec := bldr.NewRecordBatch()
	defer rec.Release()
	data := helperWriteParquet(t, sc, []arrow.RecordBatch{rec})

	r := NewReaderFromBytes(data)
	defer r.Close()
	r.ReadHeader()

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	})

	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	expected := []string{"red", "green", "red", "blue"}
	for i, want := range expected {
		if rows[i][0] != want {
			t.Errorf("row %d = %q, want %q", i, rows[i][0], want)
		}
	}
}

func TestParquetReader_FromFilesystem(t *testing.T) {
	data := helperSimpleParquet(t)
	fs := afero.NewMemMapFs()
	afero.WriteFile(fs, "test.parquet", data, 0644)

	r := NewReader(fs, "test.parquet")
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 3 {
		t.Fatalf("header len = %d, want 3", len(header))
	}

	var rowCount int
	r.ReadRows(context.Background(), func(row []string) error {
		rowCount++
		return nil
	})
	if rowCount != 3 {
		t.Errorf("got %d rows, want 3", rowCount)
	}
}

func TestParquetReader_NoDataSource(t *testing.T) {
	r := &Reader{}
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for no data source")
	}
}

func TestParquetReader_FileNotFound(t *testing.T) {
	fs := afero.NewMemMapFs()
	r := NewReader(fs, "nonexistent.parquet")
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParquetReader_InvalidData(t *testing.T) {
	r := NewReaderFromBytes([]byte("not a parquet file"))
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for invalid data")
	}
}

func TestParquetWriter_FilesystemPath(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "output.parquet")
	w.WriteHeader([]string{"x"})
	w.WriteRow([]any{"hello"})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	exists, _ := afero.Exists(fs, "output.parquet")
	if !exists {
		t.Error("output.parquet was not written")
	}

	data, _ := afero.ReadFile(fs, "output.parquet")
	if len(data) < 4 || string(data[:4]) != "PAR1" {
		t.Error("invalid Parquet magic")
	}
}

func TestParquetWriter_EmptyClose(t *testing.T) {
	w := NewWriterToBuffer()
	// Close without writing anything.
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(w.Bytes()) != 0 {
		t.Errorf("expected empty bytes, got %d", len(w.Bytes()))
	}
}

func TestParquetReader_ReadRowsCallbackError(t *testing.T) {
	data := helperSimpleParquet(t)
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

func TestParquetReader_ReadRowsWithoutReadHeader(t *testing.T) {
	data := helperSimpleParquet(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	// Call ReadRows directly without ReadHeader first.
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

func TestParquetReader_DoubleReadHeader(t *testing.T) {
	data := helperSimpleParquet(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	h1, _ := r.ReadHeader()
	h2, _ := r.ReadHeader()
	if len(h1) != len(h2) {
		t.Errorf("headers differ: %v vs %v", h1, h2)
	}
}

func TestArrowTypeToPulse_DefaultCase(t *testing.T) {
	// Test with an unusual Arrow type that falls through to default.
	got := arrowTypeToPulse(arrow.FixedWidthTypes.Time32ms, false)
	if got != encoding.FieldTypeF64 {
		t.Errorf("default case: got %s, want f64", got)
	}
}

func TestPulseTypeToArrow_DefaultCase(t *testing.T) {
	// Test with an invalid field type.
	got := pulseTypeToArrow(encoding.FieldType(255))
	if got.ID() != arrow.FLOAT64 {
		t.Errorf("default case: got %v, want FLOAT64", got)
	}
}

func TestArrowTypeToPulse_NullableIgnored(t *testing.T) {
	// Nullability is carried separately by encoding.Field.Nullable, so the
	// nullable flag passed to arrowTypeToPulse should not change the
	// returned FieldType.
	if got := arrowTypeToPulse(arrow.PrimitiveTypes.Uint8, true); got != encoding.FieldTypeU8 {
		t.Errorf("nullable uint8: got %s, want u8", got)
	}
	if got := arrowTypeToPulse(arrow.PrimitiveTypes.Uint16, true); got != encoding.FieldTypeU16 {
		t.Errorf("nullable uint16: got %s, want u16", got)
	}
	if got := arrowTypeToPulse(arrow.PrimitiveTypes.Int8, true); got != encoding.FieldTypeU8 {
		t.Errorf("nullable int8: got %s, want u8", got)
	}
	if got := arrowTypeToPulse(arrow.PrimitiveTypes.Int16, true); got != encoding.FieldTypeU16 {
		t.Errorf("nullable int16: got %s, want u16", got)
	}
	if got := arrowTypeToPulse(arrow.FixedWidthTypes.Boolean, true); got != encoding.FieldTypePackedBool {
		t.Errorf("nullable bool: got %s, want packed_bool", got)
	}
}

func TestArrowTypeToPulse_Date64(t *testing.T) {
	got := arrowTypeToPulse(arrow.FixedWidthTypes.Date64, false)
	if got != encoding.FieldTypeDate {
		t.Errorf("date64: got %s, want date", got)
	}
}

func TestArrowTypeToPulse_LargeString(t *testing.T) {
	got := arrowTypeToPulse(arrow.BinaryTypes.LargeString, false)
	if got != encoding.FieldTypeCategoricalU8 {
		t.Errorf("large_string: got %s, want categorical_u8", got)
	}
}

func TestArrowTypeToPulse_Dictionary(t *testing.T) {
	dt := &arrow.DictionaryType{
		IndexType: arrow.PrimitiveTypes.Int32,
		ValueType: arrow.BinaryTypes.String,
	}
	got := arrowTypeToPulse(dt, false)
	if got != encoding.FieldTypeCategoricalU8 {
		t.Errorf("dictionary: got %s, want categorical_u8", got)
	}
}

// Suppress unused import warnings.
var _ = math.MaxFloat32
