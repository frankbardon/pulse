package excel

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/spf13/afero"
	"github.com/xuri/excelize/v2"
)

// helperCreateXLSX creates a minimal .xlsx file in memory with the given header and rows.
func helperCreateXLSX(t *testing.T, fs afero.Fs, path string, sheet string, header []string, rows [][]string) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()

	// Rename the default sheet.
	defaultSheet := f.GetSheetName(0)
	if sheet != "" && sheet != defaultSheet {
		idx, err := f.NewSheet(sheet)
		if err != nil {
			t.Fatalf("NewSheet: %v", err)
		}
		f.SetActiveSheet(idx)
		f.DeleteSheet(defaultSheet)
	} else if sheet == "" {
		sheet = defaultSheet
	}

	// Write header.
	for c, h := range header {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		f.SetCellValue(sheet, cell, h)
	}

	// Write rows.
	for r, row := range rows {
		for c, val := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			f.SetCellValue(sheet, cell, val)
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("WriteToBuffer: %v", err)
	}
	if err := afero.WriteFile(fs, path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// helperCreateTypedXLSX creates an xlsx with typed cell values (not all strings).
func helperCreateTypedXLSX(t *testing.T, fs afero.Fs, path string) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()

	sheet := f.GetSheetName(0)
	f.SetCellValue(sheet, "A1", "name")
	f.SetCellValue(sheet, "B1", "age")
	f.SetCellValue(sheet, "C1", "score")
	f.SetCellValue(sheet, "D1", "active")
	f.SetCellValue(sheet, "E1", "joined")

	// Row 1: string, int, float, bool, date
	f.SetCellValue(sheet, "A2", "alice")
	f.SetCellValue(sheet, "B2", 30)
	f.SetCellValue(sheet, "C2", 95.5)
	f.SetCellValue(sheet, "D2", true)
	// For dates, use a time.Time
	joined, _ := time.Parse("2006-01-02", "2024-01-15")
	f.SetCellValue(sheet, "E2", joined)

	// Row 2
	f.SetCellValue(sheet, "A3", "bob")
	f.SetCellValue(sheet, "B3", 25)
	f.SetCellValue(sheet, "C3", 88.0)
	f.SetCellValue(sheet, "D3", false)
	joined2, _ := time.Parse("2006-01-02", "2023-06-20")
	f.SetCellValue(sheet, "E3", joined2)

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("WriteToBuffer: %v", err)
	}
	if err := afero.WriteFile(fs, path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestExcelReader_ReadHeader(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "test.xlsx", "", []string{"name", "age", "score"}, [][]string{
		{"alice", "30", "95.5"},
	})

	r := NewReader(fs, "test.xlsx")
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

func TestExcelReader_ReadRows(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "test.xlsx", "", []string{"name", "age"}, [][]string{
		{"alice", "30"},
		{"bob", "25"},
		{"charlie", "35"},
	})

	r := NewReader(fs, "test.xlsx")
	defer r.Close()

	header, _ := r.ReadHeader()
	_ = header

	var rows [][]string
	err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0][0] != "alice" || rows[0][1] != "30" {
		t.Errorf("row 0 = %v", rows[0])
	}
}

func TestExcelReader_EmptySheet(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "test.xlsx", "", []string{"a", "b"}, nil)

	r := NewReader(fs, "test.xlsx")
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 2 {
		t.Fatalf("header len = %d, want 2", len(header))
	}

	var rows [][]string
	err = r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestExcelReader_MultipleSheets(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Create workbook with two sheets.
	f := excelize.NewFile()
	defer f.Close()

	sheet1 := f.GetSheetName(0)
	f.SetCellValue(sheet1, "A1", "col1")
	f.SetCellValue(sheet1, "A2", "val1")

	_, err := f.NewSheet("DataSheet")
	if err != nil {
		t.Fatalf("NewSheet: %v", err)
	}
	f.SetCellValue("DataSheet", "A1", "x")
	f.SetCellValue("DataSheet", "B1", "y")
	f.SetCellValue("DataSheet", "A2", "10")
	f.SetCellValue("DataSheet", "B2", "20")

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("WriteToBuffer: %v", err)
	}
	afero.WriteFile(fs, "multi.xlsx", buf.Bytes(), 0644)

	// Default reads first sheet.
	r1 := NewReader(fs, "multi.xlsx")
	defer r1.Close()
	h1, err := r1.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader (default): %v", err)
	}
	if len(h1) != 1 || h1[0] != "col1" {
		t.Errorf("default sheet header = %v, want [col1]", h1)
	}

	// WithSheet reads named sheet.
	r2 := NewReader(fs, "multi.xlsx", WithSheet("DataSheet"))
	defer r2.Close()
	h2, err := r2.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader (DataSheet): %v", err)
	}
	if len(h2) != 2 || h2[0] != "x" || h2[1] != "y" {
		t.Errorf("DataSheet header = %v, want [x y]", h2)
	}

	var rows [][]string
	r2.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if len(rows) != 1 || rows[0][0] != "10" || rows[0][1] != "20" {
		t.Errorf("DataSheet rows = %v", rows)
	}
}

func TestExcelReader_TypedCells(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateTypedXLSX(t, fs, "typed.xlsx")

	r := NewReader(fs, "typed.xlsx")
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 5 {
		t.Fatalf("header len = %d, want 5", len(header))
	}

	var rows [][]string
	err = r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	// Row 0: alice, 30, 95.5, true/TRUE, 2024-01-15
	if rows[0][0] != "alice" {
		t.Errorf("name = %q, want alice", rows[0][0])
	}
	if rows[0][1] != "30" {
		t.Errorf("age = %q, want 30", rows[0][1])
	}
	if rows[0][2] != "95.5" {
		t.Errorf("score = %q, want 95.5", rows[0][2])
	}
	lowerActive := strings.ToLower(rows[0][3])
	if lowerActive != "true" && lowerActive != "1" {
		t.Errorf("active = %q, want true/TRUE/1", rows[0][3])
	}
	// Date: Excel may serialize as a date string in various formats.
	// Excelize returns dates in its default format which may be MM-DD-YY or similar.
	dateVal := rows[0][4]
	if !strings.Contains(dateVal, "2024") && !strings.Contains(dateVal, "24") && !strings.Contains(dateVal, "01-15") {
		t.Errorf("joined = %q, want something representing 2024-01-15", dateVal)
	}
}

func TestExcelWriter_WriteRows(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "out.xlsx")

	if err := w.WriteHeader([]string{"name", "age", "score"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{"alice", 30, 95.5}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.WriteRow([]any{"bob", 25, 88.0}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify the file exists and can be re-read.
	exists, _ := afero.Exists(fs, "out.xlsx")
	if !exists {
		t.Fatal("out.xlsx not created")
	}

	r := NewReader(fs, "out.xlsx")
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("re-read header: %v", err)
	}
	if len(header) != 3 {
		t.Fatalf("header len = %d, want 3", len(header))
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0][0] != "alice" {
		t.Errorf("row[0][0] = %q", rows[0][0])
	}
}

func TestExcelImportExportRoundTrip(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "input.xlsx", "", []string{"age", "name", "score"}, [][]string{
		{"10", "alice", "95.5"},
		{"20", "bob", "88.0"},
		{"30", "charlie", "72.3"},
	})

	reader := NewReader(fs, "input.xlsx")

	// Import.
	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs

	importReport, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if importReport.RowsImported != 3 {
		t.Errorf("imported %d, want 3", importReport.RowsImported)
	}

	// Export back to Excel.
	writer := NewWriter(fs, "output.xlsx")
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

	// Re-read exported file and verify.
	r2 := NewReader(fs, "output.xlsx")
	defer r2.Close()
	h, _ := r2.ReadHeader()
	if len(h) != 3 {
		t.Fatalf("exported header len = %d", len(h))
	}
	var rows [][]string
	r2.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
}

func TestExcelImport_CategoricalColumn(t *testing.T) {
	fs := afero.NewMemMapFs()
	colors := []string{"red", "green", "blue"}
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{colors[i%3]}
	}
	helperCreateXLSX(t, fs, "cat.xlsx", "", []string{"color"}, rows)

	reader := NewReader(fs, "cat.xlsx")
	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
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

func TestExcelExport_CategoricalResolved(t *testing.T) {
	fs := afero.NewMemMapFs()
	colors := []string{"red", "green", "blue"}
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{colors[i%3]}
	}
	helperCreateXLSX(t, fs, "cat.xlsx", "", []string{"color"}, rows)

	reader := NewReader(fs, "cat.xlsx")
	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs

	_, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Export to Excel.
	writer := NewWriter(fs, "catout.xlsx")
	exportJob := pio.NewExportJob("test.pulse", writer)
	exportJob.FS = fs

	_, err = exportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	writer.Close()

	// Re-read and verify categorical values are strings.
	r := NewReader(fs, "catout.xlsx")
	defer r.Close()
	r.ReadHeader()

	var exportedRows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		exportedRows = append(exportedRows, row)
		return nil
	})
	if len(exportedRows) != 100 {
		t.Fatalf("got %d rows, want 100", len(exportedRows))
	}
	for i := 0; i < 3; i++ {
		val := exportedRows[i][0]
		if val != "red" && val != "green" && val != "blue" {
			t.Errorf("row %d = %q, want a color name", i, val)
		}
	}
}

func TestExcelImport_InferredSchema(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := make([][]string, 100)
	for i := range rows {
		// Use values above the u4 range (max 15) so inference resolves to u8.
		rows[i] = []string{"100", "200", "30"}
	}
	helperCreateXLSX(t, fs, "infer.xlsx", "", []string{"a", "b", "c"}, rows)

	reader := NewReader(fs, "infer.xlsx")
	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Schema == nil {
		t.Fatal("schema is nil")
	}
	f := report.Schema.Field("a")
	if f == nil {
		t.Fatal("field 'a' not found")
	}
	// Should infer as u8.
	if f.Type != encoding.FieldTypeU8 {
		t.Errorf("got %s, want u8", f.Type)
	}
}

func TestExcelExport_DateFormatting(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{"2024-01-15"}
	}
	helperCreateXLSX(t, fs, "dates.xlsx", "", []string{"joined"}, rows)

	reader := NewReader(fs, "dates.xlsx")
	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs

	_, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	writer := NewWriter(fs, "datesout.xlsx")
	exportJob := pio.NewExportJob("test.pulse", writer)
	exportJob.FS = fs

	_, err = exportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	writer.Close()

	// Re-read and verify date format.
	r := NewReader(fs, "datesout.xlsx")
	defer r.Close()
	r.ReadHeader()

	var exportedRows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		exportedRows = append(exportedRows, row)
		return nil
	})
	if len(exportedRows) < 1 {
		t.Fatal("no rows exported")
	}
	if !strings.Contains(exportedRows[0][0], "2024-01-15") {
		t.Errorf("date = %q, want 2024-01-15", exportedRows[0][0])
	}
}

func TestConvertJob_CsvToExcel(t *testing.T) {
	fs := afero.NewMemMapFs()
	csvData := "name,age\nalice,30\nbob,25\ncharlie,35\n"
	csvReader := csv.NewReaderFromBytes([]byte(csvData))
	excelWriter := NewWriter(fs, "converted.xlsx")

	job := pio.NewConvertJob(csvReader, excelWriter)
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if report.RowsConverted != 3 {
		t.Errorf("converted %d, want 3", report.RowsConverted)
	}
	excelWriter.Close()

	// Verify the Excel file.
	r := NewReader(fs, "converted.xlsx")
	defer r.Close()
	h, _ := r.ReadHeader()
	if len(h) != 2 || h[0] != "name" || h[1] != "age" {
		t.Errorf("header = %v", h)
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0][0] != "alice" {
		t.Errorf("row[0][0] = %q", rows[0][0])
	}
}

func TestConvertJob_ExcelToCsv(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "source.xlsx", "", []string{"name", "age"}, [][]string{
		{"alice", "30"},
		{"bob", "25"},
	})

	excelReader := NewReader(fs, "source.xlsx")
	csvWriter := csv.NewWriterToBuffer()

	job := pio.NewConvertJob(excelReader, csvWriter)
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if report.RowsConverted != 2 {
		t.Errorf("converted %d, want 2", report.RowsConverted)
	}
	csvWriter.Close()

	output := string(csvWriter.Bytes())
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0] != "name,age" {
		t.Errorf("header = %q", lines[0])
	}
	if lines[1] != "alice,30" {
		t.Errorf("row 1 = %q", lines[1])
	}
}

func TestExcelReader_FromBytes(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "test.xlsx", "", []string{"x", "y"}, [][]string{
		{"1", "2"},
		{"3", "4"},
	})
	data, _ := afero.ReadFile(fs, "test.xlsx")

	r := NewReaderFromBytes(data)
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 2 || header[0] != "x" {
		t.Errorf("header = %v", header)
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
}

func TestExcelReader_NoDataSource(t *testing.T) {
	r := &Reader{}
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for no data source")
	}
	if !strings.Contains(err.Error(), "no data source") {
		t.Errorf("error = %v", err)
	}
}

func TestExcelReader_InvalidFile(t *testing.T) {
	r := NewReaderFromBytes([]byte("not a valid xlsx"))
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for invalid xlsx")
	}
}

func TestExcelReader_CancelContext(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{"val"}
	}
	helperCreateXLSX(t, fs, "test.xlsx", "", []string{"col"}, rows)

	r := NewReader(fs, "test.xlsx")
	defer r.Close()
	r.ReadHeader()

	ctx, cancel := context.WithCancel(context.Background())
	count := 0
	err := r.ReadRows(ctx, func(row []string) error {
		count++
		if count >= 5 {
			cancel()
		}
		return nil
	})
	// After cancel, the next iteration should pick up ctx.Done().
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExcelReader_StopIteration(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "test.xlsx", "", []string{"a"}, [][]string{
		{"1"}, {"2"}, {"3"},
	})
	r := NewReader(fs, "test.xlsx")
	defer r.Close()
	r.ReadHeader()

	count := 0
	err := r.ReadRows(context.Background(), func(row []string) error {
		count++
		if count >= 2 {
			return pio.ErrStopIteration()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestExcelReader_RowError(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "test.xlsx", "", []string{"a"}, [][]string{
		{"1"},
	})
	r := NewReader(fs, "test.xlsx")
	defer r.Close()
	r.ReadHeader()

	err := r.ReadRows(context.Background(), func(row []string) error {
		return fmt.Errorf("test error")
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExcelWriter_WithSheet(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "out.xlsx", WithSheet("MyData"))
	w.WriteHeader([]string{"a"})
	w.WriteRow([]any{"val"})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify the file can be read with the named sheet.
	r := NewReader(fs, "out.xlsx", WithSheet("MyData"))
	defer r.Close()
	h, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(h) != 1 || h[0] != "a" {
		t.Errorf("header = %v", h)
	}
}

func TestExcelWriter_CloseWithoutInit(t *testing.T) {
	w := &Writer{}
	err := w.Close()
	if err != nil {
		t.Fatalf("Close on uninitialized writer should not error: %v", err)
	}
}

func TestExcelReader_ReadRowsWithoutHeader(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "test.xlsx", "", []string{"a"}, [][]string{
		{"1"},
	})
	r := NewReader(fs, "test.xlsx")
	defer r.Close()

	// Call ReadRows without calling ReadHeader first.
	var rows [][]string
	err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
}

func TestExcelWriter_WriteRowWithoutHeader(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "out.xlsx")
	// Write row without header first - should auto-init.
	err := w.WriteRow([]any{"val"})
	if err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	w.Close()
}

func TestExcelReader_Reset(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "test.xlsx", "", []string{"a"}, [][]string{
		{"1"},
		{"2"},
	})

	r := NewReader(fs, "test.xlsx")
	defer r.Close()

	// First read.
	h1, _ := r.ReadHeader()
	if len(h1) != 1 {
		t.Fatalf("header len = %d", len(h1))
	}
	var count1 int
	r.ReadRows(context.Background(), func(row []string) error {
		count1++
		return nil
	})
	if count1 != 2 {
		t.Fatalf("first read: %d rows", count1)
	}

	// Reset and read again.
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	h2, _ := r.ReadHeader()
	if len(h2) != 1 {
		t.Fatalf("after reset: header len = %d", len(h2))
	}
	var count2 int
	r.ReadRows(context.Background(), func(row []string) error {
		count2++
		return nil
	})
	if count2 != 2 {
		t.Fatalf("second read: %d rows", count2)
	}
}

// TestExcel_DateTimeColumnWritesCanonicalString documents (and pins)
// the Excel writer's behaviour for a `datetime` column. Unlike Arrow /
// Parquet, the Excel adapter has no typed schema to satisfy: formatCell
// only special-cases decimal128 and passes everything else through to
// excelize as a plain cell value, so the canonical datetime literal
// io/export.go produces lands verbatim as text — the same treatment
// `date` already gets. No adapter change was needed for datetime; this
// test exists so a future typed arm cannot silently regress it.
func TestExcel_DateTimeColumnWritesCanonicalString(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "out.xlsx")
	w.SetPulseSchema(&encoding.Schema{Fields: []encoding.Field{
		{Name: "ts", Type: encoding.FieldTypeDateTime},
		{Name: "d", Type: encoding.FieldTypeDate},
	}})
	if err := w.WriteHeader([]string{"ts", "d"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{"2024-03-04T10:11:12Z", "2024-03-04"}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := afero.ReadFile(fs, "out.xlsx")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	r := NewReaderFromBytes(data)
	defer func() { _ = r.Close() }()
	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	var rows [][]string
	if err := r.ReadRows(t.Context(), func(row []string) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("read %d rows; want 1", len(rows))
	}
	if rows[0][0] != "2024-03-04T10:11:12Z" {
		t.Errorf("datetime cell = %q; want the canonical literal 2024-03-04T10:11:12Z", rows[0][0])
	}
}
