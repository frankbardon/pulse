package io

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// decodeAll opens an imported .pulse file and returns the decoded schema plus
// per-record value / null maps, so promotion tests can assert byte-safe
// round-trips (earlier non-null records must stay non-null; the promoted
// record must decode as null).
func decodeAll(t *testing.T, fs afero.Fs, path string) (*encoding.Schema, []map[string]float64, []map[string]bool) {
	t.Helper()
	raw, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	r := bytes.NewReader(raw)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	rr := encoding.NewRecordReader(r, schema)
	var vals []map[string]float64
	var nulls []map[string]bool
	for {
		v := map[string]float64{}
		n := map[string]bool{}
		if err := rr.ReadRecord(v, n); err != nil {
			break
		}
		vals = append(vals, v)
		nulls = append(nulls, n)
	}
	return schema, vals, nulls
}

func fieldByName(s *encoding.Schema, name string) *encoding.Field {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return &s.Fields[i]
		}
	}
	return nil
}

// TestImportJob_Promote_OutOfSampleNull_BitmapPresent covers the common case:
// one sampled column is already nullable (so every record carries a bitmap),
// and a second column's first null lands past the inference sample window. The
// second column must be promoted in-loop, reported, and decode correctly.
func TestImportJob_Promote_OutOfSampleNull_BitmapPresent(t *testing.T) {
	// 60 rows > sample floor (50). Column "clean" is null only at the last
	// row (out of sample). Column "early" is null within the sample so a
	// bitmap is already written for every record.
	var rows [][]string
	for i := range 60 {
		early := "5"
		if i == 3 {
			early = "" // in-sample null → "early" nullable from inference
		}
		clean := "7"
		if i == 59 {
			clean = "" // out-of-sample null → must promote "clean"
		}
		rows = append(rows, []string{fmt.Sprintf("%d", i), early, clean})
	}
	reader := newMockReader([]string{"id", "early", "clean"}, rows)
	fs := afero.NewMemMapFs()

	job := NewImportJob(reader, "out.pulse")
	job.FS = fs
	job.SampleRows = 50 // force the last-row null out of the sample window
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsImported != 60 {
		t.Fatalf("RowsImported = %d, want 60", report.RowsImported)
	}
	if len(report.RowErrors) != 0 {
		t.Fatalf("RowErrors = %v, want none", report.RowErrors)
	}
	if got := report.PromotedFields; len(got) != 1 || got[0] != "clean" {
		t.Fatalf("PromotedFields = %v, want [clean]", got)
	}
	if f := fieldByName(report.Schema, "clean"); f == nil || !f.Nullable {
		t.Fatalf("clean field not promoted to nullable: %+v", f)
	}

	schema, _, nulls := decodeAll(t, fs, "out.pulse")
	if f := fieldByName(schema, "clean"); f == nil || !f.Nullable {
		t.Fatalf("decoded schema: clean not nullable")
	}
	if len(nulls) != 60 {
		t.Fatalf("decoded %d records, want 60", len(nulls))
	}
	// Earlier records: clean non-null. Last record: clean null.
	if nulls[0]["clean"] {
		t.Errorf("record 0 clean = null, want present")
	}
	if !nulls[59]["clean"] {
		t.Errorf("record 59 clean = present, want null")
	}
	// The in-sample nullable column round-trips independently.
	if !nulls[3]["early"] {
		t.Errorf("record 3 early = present, want null")
	}
}

// TestImportJob_Promote_OutOfSampleNull_CleanSample covers the case the plan
// called the "fallback": the entire inference sample is null-free (no bitmap
// would be written), yet a null appears past the sample. The deferred bitmap
// buffer must still promote the field and interleave a valid bitmap so earlier
// records decode as non-null and the promoted record decodes as null.
func TestImportJob_Promote_OutOfSampleNull_CleanSample(t *testing.T) {
	var rows [][]string
	for i := range 60 {
		v := "7"
		if i == 59 {
			v = "" // the ONLY null, out of sample
		}
		rows = append(rows, []string{fmt.Sprintf("%d", i), v})
	}
	reader := newMockReader([]string{"id", "v"}, rows)
	fs := afero.NewMemMapFs()

	job := NewImportJob(reader, "clean.pulse")
	job.FS = fs
	job.SampleRows = 50 // force the last-row null out of the sample window
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsImported != 60 {
		t.Fatalf("RowsImported = %d, want 60", report.RowsImported)
	}
	if got := report.PromotedFields; len(got) != 1 || got[0] != "v" {
		t.Fatalf("PromotedFields = %v, want [v]", got)
	}

	schema, vals, nulls := decodeAll(t, fs, "clean.pulse")
	if !schema.HasBitmap() {
		t.Fatalf("decoded schema has no bitmap; promotion did not persist")
	}
	if len(nulls) != 60 {
		t.Fatalf("decoded %d records, want 60", len(nulls))
	}
	if nulls[0]["v"] || vals[0]["v"] != 7 {
		t.Errorf("record 0 v = (null=%v, val=%v), want present 7", nulls[0]["v"], vals[0]["v"])
	}
	if !nulls[59]["v"] {
		t.Errorf("record 59 v = present, want null")
	}
}

// TestImportJob_ExplicitSchema_NonNullable_StillErrors verifies an explicit
// user schema is a contract: a null in a declared non-nullable field remains a
// PULSE_IMPORT_ROW_ERROR (row skipped), never a silent promotion.
func TestImportJob_ExplicitSchema_NonNullable_StillErrors(t *testing.T) {
	rows := [][]string{
		{"10", "a"},
		{"", "b"}, // null in non-nullable "n"
		{"30", "c"},
	}
	reader := newMockReader([]string{"n", "s"}, rows)
	fs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0}, // non-nullable
			{Name: "s", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 1, Dictionary: encoding.NewDictionary()},
		},
	}
	job := NewImportJob(reader, "explicit.pulse")
	job.FS = fs
	job.Schema = schema

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.PromotedFields) != 0 {
		t.Fatalf("PromotedFields = %v, want none (explicit schema)", report.PromotedFields)
	}
	if report.RowsImported != 2 {
		t.Errorf("RowsImported = %d, want 2 (null row skipped)", report.RowsImported)
	}
	if len(report.RowErrors) != 1 {
		t.Fatalf("RowErrors = %d, want 1", len(report.RowErrors))
	}
	var coded *perr.CodedError
	if !errors.As(report.RowErrors[0].Err, &coded) || coded.Code != perr.PULSE_IMPORT_ROW_ERROR {
		t.Errorf("RowError code = %v, want PULSE_IMPORT_ROW_ERROR", report.RowErrors[0].Err)
	}
	if f := fieldByName(report.Schema, "n"); f == nil || f.Nullable {
		t.Errorf("n must stay non-nullable under explicit schema")
	}
}

// TestImportJob_ExplicitSchema_NullableHonored is the regression for the
// loadSchemaFromFile fix: an explicit schema that declares a field nullable
// must accept null cells without error and persist the flag.
func TestImportJob_ExplicitSchema_NullableHonored(t *testing.T) {
	rows := [][]string{
		{"10", "a"},
		{"", "b"},
		{"30", "c"},
	}
	reader := newMockReader([]string{"n", "s"}, rows)
	fs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "n", Type: encoding.FieldTypeU8, Nullable: true, CsvColumnIdx: 0},
			{Name: "s", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 1, Dictionary: encoding.NewDictionary()},
		},
	}
	job := NewImportJob(reader, "nullable.pulse")
	job.FS = fs
	job.Schema = schema

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsImported != 3 {
		t.Errorf("RowsImported = %d, want 3", report.RowsImported)
	}
	if len(report.RowErrors) != 0 {
		t.Fatalf("RowErrors = %v, want none", report.RowErrors)
	}
	if len(report.PromotedFields) != 0 {
		t.Errorf("PromotedFields = %v, want none (already declared nullable)", report.PromotedFields)
	}
	_, _, nulls := decodeAll(t, fs, "nullable.pulse")
	if len(nulls) != 3 || !nulls[1]["n"] {
		t.Errorf("record 1 n = present, want null (nulls=%v)", nulls)
	}
}

// TestImportJob_WithinSampleNull_NoPromotion confirms the baseline: a null
// inside the sample is picked up by inference, so the field is already nullable
// and nothing is promoted.
func TestImportJob_WithinSampleNull_NoPromotion(t *testing.T) {
	rows := [][]string{
		{"10", "7"},
		{"20", ""},
		{"30", "9"},
	}
	reader := newMockReader([]string{"id", "v"}, rows)
	fs := afero.NewMemMapFs()

	job := NewImportJob(reader, "within.pulse")
	job.FS = fs
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.PromotedFields) != 0 {
		t.Errorf("PromotedFields = %v, want none (null was in-sample)", report.PromotedFields)
	}
	if f := fieldByName(report.Schema, "v"); f == nil || !f.Nullable {
		t.Errorf("v should be nullable from inference")
	}
}

// TestImportJob_Predict_PromotesOutOfSampleNull verifies Predict's full row
// pass finalizes nullability so schema-template / predict report the accurate
// flag for a null past the sample window.
func TestImportJob_Predict_PromotesOutOfSampleNull(t *testing.T) {
	var rows [][]string
	for i := range 60 {
		v := "7"
		if i == 59 {
			v = ""
		}
		rows = append(rows, []string{fmt.Sprintf("%d", i), v})
	}
	reader := newMockReader([]string{"id", "v"}, rows)
	fs := afero.NewMemMapFs()

	job := NewImportJob(reader, "predict.pulse")
	job.FS = fs
	job.SampleRows = 50 // force the last-row null out of the sample window
	rep, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if f := fieldByName(rep.Schema, "v"); f == nil || !f.Nullable {
		t.Fatalf("Predict did not promote v to nullable")
	}
}
