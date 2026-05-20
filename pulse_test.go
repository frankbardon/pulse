package pulse

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// mockReader implements io.Reader and io.ResetReader for testing.
type mockReader struct {
	columns []string
	rows    [][]string
	pos     int
}

func newMockReader(columns []string, rows [][]string) *mockReader {
	return &mockReader{columns: columns, rows: rows}
}

func (m *mockReader) ReadHeader() ([]string, error) {
	return m.columns, nil
}

func (m *mockReader) ReadRows(_ context.Context, fn func(row []string) error) error {
	for m.pos < len(m.rows) {
		if err := fn(m.rows[m.pos]); err != nil {
			return err
		}
		m.pos++
	}
	return nil
}

func (m *mockReader) Close() error { return nil }

func (m *mockReader) Reset() error {
	m.pos = 0
	return nil
}

// collectWriter is a Writer that collects output in memory for testing.
type collectWriter struct {
	header []string
	rows   [][]any
}

func (w *collectWriter) WriteHeader(columns []string) error {
	w.header = columns
	return nil
}

func (w *collectWriter) WriteRow(values []any) error {
	row := make([]any, len(values))
	copy(row, values)
	w.rows = append(w.rows, row)
	return nil
}

func (w *collectWriter) Close() error { return nil }

// --- Test helpers ---

// createTestPulseFile creates a .pulse file from tabular data in the given FS.
func createTestPulseFile(t *testing.T, memFs afero.Fs, path string, columns []string, rows [][]string) {
	t.Helper()
	reader := newMockReader(columns, rows)
	job := pio.NewImportJob(reader, path)
	job.FS = memFs

	_, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("creating test pulse file: %v", err)
	}
}

// --- Tests ---

func TestNew_DefaultOptions(t *testing.T) {
	p, err := New(Options{
		FS: afero.NewMemMapFs(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("New returned nil")
	}
}

func TestNew_CustomFS(t *testing.T) {
	memFs := afero.NewMemMapFs()
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("New returned nil")
	}
}

func TestNew_CustomDataDir(t *testing.T) {
	p, err := New(Options{
		DataDir: "/custom/data",
		FS:      afero.NewMemMapFs(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("New returned nil")
	}
}

func TestOpen_ValidPulseFile(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cohort, err := p.Open(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if cohort == nil {
		t.Fatal("Open returned nil")
	}

	schema := cohort.Schema()
	if schema == nil {
		t.Fatal("Schema is nil")
	}
	if len(schema.Fields) != 2 {
		t.Errorf("Fields = %d, want 2", len(schema.Fields))
	}
}

func TestOpen_InvalidFile(t *testing.T) {
	memFs := afero.NewMemMapFs()
	_ = afero.WriteFile(memFs, "bad.pulse", []byte("not a pulse file"), 0644)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = p.Open(context.Background(), "bad.pulse")
	if err == nil {
		t.Fatal("expected error for invalid file")
	}
}

func TestOpen_MissingFile(t *testing.T) {
	memFs := afero.NewMemMapFs()
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = p.Open(context.Background(), "missing.pulse")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestProcess_SimpleRequest(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Process(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "age"},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp == nil {
		t.Fatal("Process returned nil")
	}
	if resp.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
}

func TestProcess_WithFilter(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"}, {"40"}, {"50"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Process(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "age", Values: []string{"15", "35"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "age"},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp == nil {
		t.Fatal("Process returned nil")
	}
	if resp.Metadata.FilteredRows >= resp.Metadata.TotalRows {
		t.Errorf("expected filter to reduce rows: filtered=%d, total=%d",
			resp.Metadata.FilteredRows, resp.Metadata.TotalRows)
	}
}

func TestProcess_WithGroup(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
		{"30", "hello"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Process(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "name"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "age"},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp == nil {
		t.Fatal("Process returned nil")
	}
	if len(resp.Data) < 2 {
		t.Errorf("expected at least 2 groups, got %d", len(resp.Data))
	}
}

// TestProcess_WithWindow exercises the windowed pipeline through the public
// pulse.Process facade. It verifies the WIN_LAG output column lands on the
// response with one row per record.
func TestProcess_WithWindow(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"ts", "x"}, [][]string{
		{"1", "10"},
		{"2", "20"},
		{"3", "30"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Process(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Windows: []*types.Window{
			{
				Type:    types.WIN_LAG,
				Field:   "x",
				Label:   "x_lag",
				OrderBy: []types.OrderKey{{Field: "ts"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp == nil {
		t.Fatal("Process returned nil")
	}
	if len(resp.Data) != 3 {
		t.Fatalf("Data length = %d, want 3", len(resp.Data))
	}
	for _, r := range resp.Data {
		ts, _ := r["ts"].(float64)
		switch ts {
		case 1.0:
			if r["x_lag"] != nil {
				t.Errorf("ts=1 x_lag = %v, want nil", r["x_lag"])
			}
		case 2.0:
			if v, _ := r["x_lag"].(float64); v != 10.0 {
				t.Errorf("ts=2 x_lag = %v, want 10", r["x_lag"])
			}
		case 3.0:
			if v, _ := r["x_lag"].(float64); v != 20.0 {
				t.Errorf("ts=3 x_lag = %v, want 20", r["x_lag"])
			}
		}
	}
}

// TestProcess_WithSort exercises the response-level sort through the public
// pulse.Process facade. Verifies output rows arrive in the requested order
// regardless of input record order.
func TestProcess_WithSort(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"ts", "x"}, [][]string{
		{"3", "30"},
		{"1", "10"},
		{"2", "20"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Process(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Windows: []*types.Window{
			{
				Type:    types.WIN_RUNNING_SUM,
				Field:   "x",
				Label:   "x_cum",
				OrderBy: []types.OrderKey{{Field: "ts"}},
				Frame:   &types.FrameSpec{Mode: "rows", Following: ptrIntFacade(0)},
			},
		},
		Sort: []types.OrderKey{{Field: "ts"}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("Data length = %d, want 3", len(resp.Data))
	}
	want := []struct {
		ts  float64
		cum float64
	}{
		{1.0, 10.0},
		{2.0, 30.0},
		{3.0, 60.0},
	}
	for i, r := range resp.Data {
		ts, _ := r["ts"].(float64)
		cum, _ := r["x_cum"].(float64)
		if ts != want[i].ts || cum != want[i].cum {
			t.Errorf("row[%d] = (ts=%v cum=%v), want (ts=%v cum=%v)", i, ts, cum, want[i].ts, want[i].cum)
		}
	}
}

func ptrIntFacade(v int) *int { return &v }

func TestProcess_ComposedRequest(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	responses, err := p.Compose(context.Background(), &ComposedRequest{
		Requests: []*Request{
			{
				Cohort: &types.Cohort{Filename: "test.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "age"},
				},
			},
			{
				Cohort: &types.Cohort{Filename: "test.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "age"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(responses) != 2 {
		t.Errorf("expected 2 responses, got %d", len(responses))
	}
}

func TestImport_CsvToPulse(t *testing.T) {
	memFs := afero.NewMemMapFs()

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	reader := newMockReader([]string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
		{"30", "hello"},
	})

	job := pio.NewImportJob(reader, "imported.pulse")
	report, err := p.Import(context.Background(), job)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.RowsImported != 3 {
		t.Errorf("RowsImported = %d, want 3", report.RowsImported)
	}

	exists, _ := afero.Exists(memFs, "imported.pulse")
	if !exists {
		t.Error("imported.pulse was not written")
	}
}

func TestExport_PulseToCsv(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	writer := &collectWriter{}
	job := pio.NewExportJob("test.pulse", writer)

	report, err := p.Export(context.Background(), job)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if report.RowsExported != 2 {
		t.Errorf("RowsExported = %d, want 2", report.RowsExported)
	}
	if len(writer.rows) != 2 {
		t.Errorf("writer rows = %d, want 2", len(writer.rows))
	}
}

func TestConvert_CsvToTabular(t *testing.T) {
	memFs := afero.NewMemMapFs()

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	reader := newMockReader([]string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
	})
	writer := &collectWriter{}

	job := pio.NewConvertJob(reader, writer)
	report, err := p.Convert(context.Background(), job)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if report.RowsConverted != 2 {
		t.Errorf("RowsConverted = %d, want 2", report.RowsConverted)
	}
}

func TestInspect(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Inspect(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if result == nil {
		t.Fatal("Inspect returned nil")
	}
	if result.FieldCount != 2 {
		t.Errorf("FieldCount = %d, want 2", result.FieldCount)
	}
}

func TestPredict(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Predict(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "age"},
		},
	})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if result == nil {
		t.Fatal("Predict returned nil")
	}
	if !result.Valid {
		t.Error("expected valid prediction")
	}
}

func TestPredict_InvalidField(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Predict(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "nonexistent"},
		},
	})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid prediction for nonexistent field")
	}
}

func TestSample(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"}, {"40"}, {"50"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rows, err := p.Sample(context.Background(), "test.pulse", 3)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("Sample returned %d rows, want 3", len(rows))
	}
}

func TestFacet(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
		{"30", "hello"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	values, err := p.Facet(context.Background(), "test.pulse", "name")
	if err != nil {
		t.Fatalf("Facet: %v", err)
	}
	if len(values) < 2 {
		t.Errorf("Facet returned %d values, want at least 2", len(values))
	}
}

func TestCohort_Schema(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cohort, err := p.Open(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	schema := cohort.Schema()
	if schema == nil {
		t.Fatal("Schema is nil")
	}
}

func TestCohort_Field(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cohort, err := p.Open(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	f := cohort.Field("age")
	if f == nil {
		t.Fatal("Field('age') is nil")
	}
	if f.Name != "age" {
		t.Errorf("Field.Name = %q, want 'age'", f.Name)
	}

	missing := cohort.Field("nonexistent")
	if missing != nil {
		t.Error("expected nil for nonexistent field")
	}
}

func TestCohort_Categorical(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age", "name"}, [][]string{
		{"10", "hello"},
		{"20", "world"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cohort, err := p.Open(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// "name" should be categorical after inference.
	dict, ok := cohort.Categorical("name")
	if !ok {
		t.Fatal("expected categorical dictionary for 'name'")
	}
	if dict == nil {
		t.Fatal("dictionary is nil")
	}

	// "age" is numeric, should not be categorical.
	_, ok = cohort.Categorical("age")
	if ok {
		t.Error("age should not be categorical")
	}
}

func TestTypeAliases(t *testing.T) {
	// Verify that type aliases compile and work as expected.
	var req Request
	req.Cohort = &types.Cohort{Filename: "test.pulse"}
	if req.Cohort.Filename != "test.pulse" {
		t.Error("Request type alias broken")
	}

	var resp Response
	resp.Data = []map[string]any{{"key": "value"}}
	if len(resp.Data) != 1 {
		t.Error("Response type alias broken")
	}

	var composed ComposedRequest
	composed.Requests = []*Request{&req}
	if len(composed.Requests) != 1 {
		t.Error("ComposedRequest type alias broken")
	}
}

func TestEmbedScenario_CsvToProcessToParquet(t *testing.T) {
	memFs := afero.NewMemMapFs()

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Step 1: Write CSV data to FS and import via CSV reader.
	csvData := []byte("age,name,score\n10,alice,85\n20,bob,90\n30,alice,75\n40,bob,60\n50,alice,95")
	if err := afero.WriteFile(memFs, "input.csv", csvData, 0644); err != nil {
		t.Fatalf("writing CSV: %v", err)
	}

	csvReader := csv.NewReader(memFs, "input.csv")
	importJob := pio.NewImportJob(csvReader, "cohort.pulse")
	importReport, err := p.Import(context.Background(), importJob)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if importReport.RowsImported != 5 {
		t.Fatalf("RowsImported = %d, want 5", importReport.RowsImported)
	}

	// Step 2: Process the imported cohort.
	resp, err := p.Process(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "age"},
			{Type: types.AGG_AVERAGE, Field: "score"},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp == nil || resp.Metadata == nil {
		t.Fatal("Process returned nil response or metadata")
	}
	if resp.Metadata.TotalRows < 5 {
		t.Errorf("TotalRows = %d, want at least 5", resp.Metadata.TotalRows)
	}

	// Step 3: Export to CSV (as tabular output).
	csvWriter := csv.NewWriterToBuffer()
	exportJob := pio.NewExportJob("cohort.pulse", csvWriter)
	exportReport, err := p.Export(context.Background(), exportJob)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if exportReport.RowsExported != 5 {
		t.Errorf("RowsExported = %d, want 5", exportReport.RowsExported)
	}

	// Verify the exported data round-trips.
	exported := csvWriter.Bytes()
	if len(exported) == 0 {
		t.Fatal("exported CSV is empty")
	}
}

func TestNoOsExec(t *testing.T) {
	// Verify that the pulse package and its non-test source files do not import os/exec.
	// We scan all .go files in the pulse root package directory.
	fset := token.NewFileSet()

	// Get the directory containing pulse.go (the root package).
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading directory: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}

		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == "os/exec" {
				t.Errorf("pulse package file %s imports os/exec; library facade must not shell out", name)
			}
		}
	}
}

func TestComposeParallel_FacadeOrderPreserved(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"}, {"40"}, {"50"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	composed := &ComposedRequest{
		Requests: []*Request{
			{Cohort: &types.Cohort{Filename: "test.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "age", Label: "a"}}},
			{Cohort: &types.Cohort{Filename: "test.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age", Label: "b"}}},
			{Cohort: &types.Cohort{Filename: "test.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "c"}}},
		},
	}

	resps, err := p.ComposeParallel(context.Background(), composed, ComposeOptions{MaxWorkers: 3})
	if err != nil {
		t.Fatalf("ComposeParallel: %v", err)
	}
	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3", len(resps))
	}
	if _, ok := resps[0].Data[0]["a"]; !ok {
		t.Error("slot 0 missing label a")
	}
	if _, ok := resps[1].Data[0]["b"]; !ok {
		t.Error("slot 1 missing label b")
	}
	if _, ok := resps[2].Data[0]["c"]; !ok {
		t.Error("slot 2 missing label c")
	}
}

func TestProcessStream_FacadeReturnsIterator(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	iter, err := p.ProcessStream(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "age", Label: "n"},
		},
	})
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()

	var rows []Row
	for {
		row, ok, err := iter.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			break
		}
		rows = append(rows, row)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if iter.Metadata() == nil {
		t.Fatal("Metadata nil after drain")
	}
}

func TestCohort_SchemaNotNil(t *testing.T) {
	memFs := afero.NewMemMapFs()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		},
	}

	reader := newMockReader([]string{"x"}, [][]string{{"1"}, {"2"}})
	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = memFs
	job.Schema = schema
	_, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cohort, err := p.Open(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if cohort.Schema() == nil {
		t.Fatal("Schema() must not return nil for valid cohort")
	}
}

// TestProcessChain_FacadeRoundTrip exercises the pulse.ProcessChain
// facade against an in-memory cohort to confirm the type aliases and
// service wiring are reachable from the public API.
func TestProcessChain_FacadeRoundTrip(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"}, {"40"}, {"50"},
	})
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	chain := &ChainRequest{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Stages: []*ChainStage{
			{
				Name: "sum",
				Request: &Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: "age", Label: "s"},
					},
				},
			},
			{
				Name: "count_one",
				Request: &Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "s", Label: "n"},
					},
				},
			},
		},
	}

	resp, err := p.ProcessChain(context.Background(), chain)
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	if resp == nil || resp.Final == nil || len(resp.Final.Data) == 0 {
		t.Fatalf("final empty: %#v", resp)
	}
	if got := resp.Final.Data[0]["n"].(float64); got != 1.0 {
		t.Errorf("final count = %v, want 1.0", got)
	}
}
