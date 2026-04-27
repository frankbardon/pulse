package service

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// --- test helpers ---

// writePulseFile writes a complete .pulse file (header + schema + records) to a buffer.
func writePulseFile(t *testing.T, schema *encoding.Schema, records [][]uint64) []byte {
	t.Helper()
	var buf bytes.Buffer

	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	for ri, rec := range records {
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				t.Fatalf("WriteFieldValue record[%d] field[%d]: %v", ri, fi, err)
			}
		}
	}
	return buf.Bytes()
}

// setupTestFS creates an in-memory FS config with a .pulse file written at the given path.
func setupTestFS(t *testing.T, path string, schema *encoding.Schema, records [][]uint64) *fs.Config {
	t.Helper()
	cfg := fs.NewMemMap()
	data := writePulseFile(t, schema, records)
	if err := afero.WriteFile(cfg.Fs(), path, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfg
}

func testSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
}

func testRecords() [][]uint64 {
	return [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
		{3, math.Float64bits(30.0)},
		{4, math.Float64bits(40.0)},
		{5, math.Float64bits(50.0)},
	}
}

func floatClose(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

// --- Open tests ---

func TestService_OpenCohort(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if cohort == nil {
		t.Fatal("cohort is nil")
	}

	s := cohort.Schema()
	if len(s.Fields) != 2 {
		t.Errorf("schema fields = %d, want 2", len(s.Fields))
	}
	if s.Fields[0].Name != "id" {
		t.Errorf("field[0].Name = %q, want %q", s.Fields[0].Name, "id")
	}
	if s.Fields[1].Name != "score" {
		t.Errorf("field[1].Name = %q, want %q", s.Fields[1].Name, "score")
	}
}

func TestService_OpenCohort_NotFound(t *testing.T) {
	cfg := fs.NewMemMap()
	svc := New(cfg)

	_, err := svc.Open(context.Background(), "nonexistent.pulse")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.HasCode(err, errors.SERVICE_RESOURCE) {
		t.Errorf("expected SERVICE_RESOURCE error, got: %v", err)
	}
}

func TestService_OpenCohort_InvalidFormat(t *testing.T) {
	cfg := fs.NewMemMap()
	// Write garbage data
	if err := afero.WriteFile(cfg.Fs(), "bad.pulse", []byte("not a pulse file"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := New(cfg)

	_, err := svc.Open(context.Background(), "bad.pulse")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

// --- Process tests ---

func TestService_Process(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp == nil || len(resp.Data) == 0 {
		t.Fatal("response data is empty")
	}

	val, ok := resp.Data[0]["avg_score"].(float64)
	if !ok {
		t.Fatalf("avg_score not float64: %T", resp.Data[0]["avg_score"])
	}
	if !floatClose(val, 30.0, 0.001) {
		t.Errorf("avg_score = %f, want 30.0", val)
	}
	if resp.Metadata == nil {
		t.Fatal("metadata is nil")
	}
	if resp.Metadata.TotalRows != 5 {
		t.Errorf("TotalRows = %d, want 5", resp.Metadata.TotalRows)
	}
	if resp.Metadata.CohortFile != "test.pulse" {
		t.Errorf("CohortFile = %q, want %q", resp.Metadata.CohortFile, "test.pulse")
	}
}

func TestService_Process_WithFilter(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"20", "40"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	n, ok := resp.Data[0]["n"].(float64)
	if !ok || !floatClose(n, 3.0, 0.001) {
		t.Errorf("filtered count = %v, want 3.0", resp.Data[0]["n"])
	}
	if resp.Metadata.FilteredRows != 3 {
		t.Errorf("FilteredRows = %d, want 3", resp.Metadata.FilteredRows)
	}
}

func TestService_Process_WithGroup(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
		{1, math.Float64bits(30.0)},
		{2, math.Float64bits(40.0)},
	}
	cfg := setupTestFS(t, "test.pulse", schema, records)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("data rows = %d, want 2", len(resp.Data))
	}
}

func TestService_Process_ComposedRequest(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	composed := &types.ComposedRequest{
		Requests: []*types.Request{
			{
				Cohort: &types.Cohort{Filename: "test.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "score", Label: "total"},
				},
			},
			{
				Cohort: &types.Cohort{Filename: "test.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				},
			},
		},
	}

	responses, err := svc.Compose(context.Background(), composed)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(responses) != 2 {
		t.Fatalf("response count = %d, want 2", len(responses))
	}

	total, ok := responses[0].Data[0]["total"].(float64)
	if !ok || !floatClose(total, 150.0, 0.001) {
		t.Errorf("total = %v, want 150.0", responses[0].Data[0]["total"])
	}

	n, ok := responses[1].Data[0]["n"].(float64)
	if !ok || !floatClose(n, 5.0, 0.001) {
		t.Errorf("n = %v, want 5.0", responses[1].Data[0]["n"])
	}
}

func TestService_Process_CategoricalCohort(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("red")
	dict.Add("green")
	dict.Add("blue")

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{0, math.Float64bits(10.0)}, // red
		{1, math.Float64bits(20.0)}, // green
		{0, math.Float64bits(30.0)}, // red
		{2, math.Float64bits(40.0)}, // blue
	}
	cfg := setupTestFS(t, "test.pulse", schema, records)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "color", Values: []string{"red"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	n, ok := resp.Data[0]["n"].(float64)
	if !ok || !floatClose(n, 2.0, 0.001) {
		t.Errorf("categorical filtered count = %v, want 2.0", resp.Data[0]["n"])
	}
}

func TestService_Process_NilCohort(t *testing.T) {
	cfg := fs.NewMemMap()
	svc := New(cfg)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	_, err := svc.Process(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for nil cohort")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION error, got: %v", err)
	}
}

// --- Sample tests ---

func TestService_Sample(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	rows, err := svc.Sample(context.Background(), "test.pulse", 3)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("sample count = %d, want 3", len(rows))
	}

	// Each row should have field values
	for i, row := range rows {
		if _, ok := row["id"]; !ok {
			t.Errorf("row[%d] missing 'id'", i)
		}
		if _, ok := row["score"]; !ok {
			t.Errorf("row[%d] missing 'score'", i)
		}
	}
}

func TestService_Sample_MoreThanAvailable(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	rows, err := svc.Sample(context.Background(), "test.pulse", 100)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	// Should return all 5 records, not error
	if len(rows) != 5 {
		t.Errorf("sample count = %d, want 5", len(rows))
	}
}

// --- Facet tests ---

func TestService_Facet(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("red")
	dict.Add("green")
	dict.Add("blue")

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{0, math.Float64bits(10.0)},
		{1, math.Float64bits(20.0)},
		{0, math.Float64bits(30.0)},
		{2, math.Float64bits(40.0)},
	}
	cfg := setupTestFS(t, "test.pulse", schema, records)
	svc := New(cfg)

	values, err := svc.Facet(context.Background(), "test.pulse", "color")
	if err != nil {
		t.Fatalf("Facet: %v", err)
	}
	if len(values) != 3 {
		t.Errorf("facet values = %d, want 3", len(values))
	}

	// Check all values present
	valSet := make(map[string]bool)
	for _, v := range values {
		valSet[v] = true
	}
	for _, want := range []string{"red", "green", "blue"} {
		if !valSet[want] {
			t.Errorf("missing facet value: %q", want)
		}
	}
}

func TestService_Facet_NumericField(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	values, err := svc.Facet(context.Background(), "test.pulse", "score")
	if err != nil {
		t.Fatalf("Facet: %v", err)
	}

	// For numeric fields, facet returns distinct string representations of seen values
	if len(values) != 5 {
		t.Errorf("numeric facet values = %d, want 5", len(values))
	}
}

func TestService_Facet_UnknownField(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	_, err := svc.Facet(context.Background(), "test.pulse", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION error, got: %v", err)
	}
}

// --- Cohort method tests ---

func TestCohort_Records(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	iter, err := cohort.Records(context.Background())
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	count := 0
	for iter.Next() {
		rec := iter.Record()
		if rec == nil {
			t.Fatal("nil record from iterator")
		}
		count++
	}
	if count != 5 {
		t.Errorf("record count from iterator = %d, want 5", count)
	}
}

func TestCohort_RecordCount(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	n, err := cohort.RecordCount()
	if err != nil {
		t.Fatalf("RecordCount: %v", err)
	}
	if n != 5 {
		t.Errorf("RecordCount = %d, want 5", n)
	}
}

func TestCohort_RecordCount_EmptySchema(t *testing.T) {
	schema := &encoding.Schema{Fields: nil}
	cfg := setupTestFS(t, "empty.pulse", schema, nil)
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), "empty.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	n, err := cohort.RecordCount()
	if err != nil {
		t.Fatalf("RecordCount: %v", err)
	}
	if n != 0 {
		t.Errorf("RecordCount = %d, want 0", n)
	}
}

// --- DataDir resolution ---

func TestService_Process_WithDataDir(t *testing.T) {
	schema := testSchema()
	cfg := fs.NewMemMap()

	// Write the file in a subdirectory
	data := writePulseFile(t, schema, testRecords())
	if err := cfg.Fs().MkdirAll("subdir", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := afero.WriteFile(cfg.Fs(), "subdir/test.pulse", data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse", DataDir: "subdir"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	n, ok := resp.Data[0]["n"].(float64)
	if !ok || !floatClose(n, 5.0, 0.001) {
		t.Errorf("n = %v, want 5.0", resp.Data[0]["n"])
	}
}

// --- Compose validation ---

func TestService_Compose_Empty(t *testing.T) {
	cfg := fs.NewMemMap()
	svc := New(cfg)

	_, err := svc.Compose(context.Background(), &types.ComposedRequest{})
	if err == nil {
		t.Fatal("expected error for empty composed request")
	}
}

func TestService_Compose_Nil(t *testing.T) {
	cfg := fs.NewMemMap()
	svc := New(cfg)

	_, err := svc.Compose(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil composed request")
	}
}

// --- F32 type coverage ---

func TestService_Process_F32Field(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "val", Type: encoding.FieldTypeF32, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}
	records := [][]uint64{
		{uint64(math.Float32bits(1.5))},
		{uint64(math.Float32bits(2.5))},
	}
	cfg := setupTestFS(t, "f32.pulse", schema, records)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "f32.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "val", Label: "total"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	total, ok := resp.Data[0]["total"].(float64)
	if !ok || !floatClose(total, 4.0, 0.001) {
		t.Errorf("total = %v, want 4.0", resp.Data[0]["total"])
	}
}
