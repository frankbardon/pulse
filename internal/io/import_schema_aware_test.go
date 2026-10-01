package io

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// --- byte-identity baseline -------------------------------------------------

// TestImportJob_NonSchemaAwareReader_ByteIdentical pins the exact .pulse bytes
// a plain (non-SchemaAwareReader) source produces through ImportJob.Run.
//
// The hashes below were captured from the tree IMMEDIATELY BEFORE the
// SchemaAwareReader bypass landed. They are a pre-change baseline on purpose:
// comparing two runs of the post-change code would prove nothing about the
// regression this test exists to catch. If a change to import.go moves any of
// these hashes, the absent-implementation path is no longer byte-identical and
// the change is a regression until proven otherwise.
func TestImportJob_NonSchemaAwareReader_ByteIdentical(t *testing.T) {
	tests := []struct {
		name    string
		columns []string
		rows    [][]string
		setup   func(j *ImportJob)
		want    string
	}{
		{
			name:    "inferred_no_nulls",
			columns: []string{"age", "name", "active"},
			rows: [][]string{
				{"10", "hello", "true"},
				{"20", "world", "false"},
				{"30", "hello", "true"},
			},
			want: "f067e0042c5e348b5163ba93791af17e0395470a2980aa6f441cb557c8a8f656",
		},
		{
			name:    "inferred_out_of_sample_null_promotes",
			columns: []string{"age", "name"},
			rows: func() [][]string {
				rows := make([][]string, 0, 120)
				for i := 0; i < 119; i++ {
					rows = append(rows, []string{"7", "alpha"})
				}
				return append(rows, []string{"", "beta"})
			}(),
			setup: func(j *ImportJob) { j.SampleRows = 50 },
			want:  "320dddf1b7f592532efcb1ac2803adb117cfd09f9f7c9427b82fc3ae094b2624",
		},
		{
			name:    "inferred_set_column",
			columns: []string{"id", "tags"},
			rows: func() [][]string {
				rows := make([][]string, 0, 60)
				for i := 0; i < 60; i++ {
					switch i % 3 {
					case 0:
						rows = append(rows, []string{"1", "red|blue"})
					case 1:
						rows = append(rows, []string{"2", "blue|green"})
					default:
						rows = append(rows, []string{"3", "red|green|blue"})
					}
				}
				return rows
			}(),
			want: "1734b7ffba72e834c6fe078d1af8546c55c3f50fc0a614f163712a2257dd5316",
		},
		{
			name:    "explicit_schema_non_nullable",
			columns: []string{"age", "name"},
			rows: [][]string{
				{"10", "hello"},
				{"20", "world"},
				{"30", "hello"},
			},
			setup: func(j *ImportJob) {
				j.Schema = &encoding.Schema{Fields: []encoding.Field{
					{Name: "age", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
					{Name: "name", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 1, Dictionary: encoding.NewDictionary()},
				}}
			},
			want: "5c289e0a07288f71f3e0e2d77b2b294107f5d0292eedac12ca5a4e5e0542cb9b",
		},
		{
			name:    "explicit_schema_nullable_bitmap",
			columns: []string{"age", "score"},
			rows: [][]string{
				{"10", "1.5"},
				{"", "2.5"},
				{"30", ""},
			},
			setup: func(j *ImportJob) {
				j.Schema = &encoding.Schema{Fields: []encoding.Field{
					{Name: "age", Type: encoding.FieldTypeU8, Nullable: true, CsvColumnIdx: 0},
					{Name: "score", Type: encoding.FieldTypeF64, Nullable: true, CsvColumnIdx: 1},
				}}
			},
			want: "8742d35c657aa59f69fbe48123fdd92181ffe33907d2aa09bc3212d43e27cc99",
		},
		{
			name:    "inference_steering_slots",
			columns: []string{"code", "tags"},
			rows: func() [][]string {
				rows := make([][]string, 0, 60)
				for i := 0; i < 60; i++ {
					rows = append(rows, []string{"12", "red;blue"})
				}
				return rows
			}(),
			setup: func(j *ImportJob) {
				j.SampleRows = 55
				j.SetInferenceMinPct = 20
				j.ColumnTypeOverrides = map[string]encoding.FieldType{"code": encoding.FieldTypeU32}
			},
			want: "d3f21ccb556c1c478ffdc8a90a9b119d5ce1b6d15d3f7b23d51881904537dc1f",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			job := NewImportJob(newMockReader(tt.columns, tt.rows), "out.pulse")
			job.FS = fs
			if tt.setup != nil {
				tt.setup(job)
			}
			if _, err := job.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			raw, err := afero.ReadFile(fs, "out.pulse")
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			sum := sha256.Sum256(raw)
			got := hex.EncodeToString(sum[:])
			if got != tt.want {
				t.Errorf("sha256(out.pulse) = %s, want %s (bytes=%d) — the absent-implementation import path is no longer byte-identical to its pre-SchemaAwareReader baseline", got, tt.want, len(raw))
			}
		})
	}
}

// --- SchemaAwareReader stubs ------------------------------------------------

// authoritativeReader implements Reader and SchemaAwareReader but NOT
// ResetReader. That omission is load-bearing: the inference path hard-requires
// a ResetReader, so an import that succeeds through this reader proves
// inference was bypassed structurally, not just that its output was discarded.
type authoritativeReader struct {
	columns []string
	rows    [][]string
	schema  *encoding.Schema
	err     error
	calls   int
	headers int
}

func (r *authoritativeReader) ReadHeader() ([]string, error) {
	r.headers++
	return r.columns, nil
}

func (r *authoritativeReader) ReadRows(_ context.Context, fn func(row []string) error) error {
	for _, row := range r.rows {
		if err := fn(row); err != nil {
			return err
		}
	}
	return nil
}

func (r *authoritativeReader) Close() error { return nil }

func (r *authoritativeReader) PulseSchema() (*encoding.Schema, error) {
	r.calls++
	return r.schema, r.err
}

var _ SchemaAwareReader = (*authoritativeReader)(nil)

// resettableAuthoritativeReader also implements ResetReader, so the
// decline-and-fall-back-to-inference path has a viable source.
type resettableAuthoritativeReader struct {
	*mockReader
	schema *encoding.Schema
	err    error
	calls  int
}

func (r *resettableAuthoritativeReader) PulseSchema() (*encoding.Schema, error) {
	r.calls++
	return r.schema, r.err
}

var _ SchemaAwareReader = (*resettableAuthoritativeReader)(nil)
var _ ResetReader = (*resettableAuthoritativeReader)(nil)

// --- the bypass ------------------------------------------------------------

func TestImportJob_SchemaAwareReader_BypassesInference(t *testing.T) {
	// Every cell is a small integer literal that inference would classify
	// u8, and "grade" reads like a categorical. The authoritative schema says
	// otherwise, and the authoritative schema is what must land on disk.
	src := &authoritativeReader{
		columns: []string{"score", "grade"},
		rows:    [][]string{{"1", "A"}, {"2", "B"}, {"3", "A"}},
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU16, CsvColumnIdx: 1, Dictionary: encoding.NewDictionary()},
		}},
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(src, "out.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if src.calls != 1 {
		t.Errorf("PulseSchema called %d times, want 1", src.calls)
	}
	if report.RowsImported != 3 {
		t.Errorf("RowsImported = %d, want 3", report.RowsImported)
	}

	schema, _, _ := decodeAll(t, fs, "out.pulse")
	if got := schema.Fields[0].Type; got != encoding.FieldTypeF64 {
		t.Errorf("score type = %s, want f64 (inference would have said u8)", got)
	}
	if got := schema.Fields[1].Type; got != encoding.FieldTypeCategoricalU16 {
		t.Errorf("grade type = %s, want categorical_u16", got)
	}
}

func TestImportJob_SchemaAwareReader_ExplicitSchemaWins(t *testing.T) {
	src := &authoritativeReader{
		columns: []string{"score"},
		rows:    [][]string{{"1"}, {"2"}},
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
		}},
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(src, "out.pulse")
	job.FS = fs
	job.Schema = &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
	}}

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if src.calls != 0 {
		t.Errorf("PulseSchema called %d times; an explicit ImportJob.Schema must not consult the reader", src.calls)
	}
	schema, _, _ := decodeAll(t, fs, "out.pulse")
	if got := schema.Fields[0].Type; got != encoding.FieldTypeU8 {
		t.Errorf("score type = %s, want u8 (the caller-supplied schema)", got)
	}
}

// --- inference-steering slots are inert -------------------------------------

func TestImportJob_SchemaAwareReader_SteeringSlotsInert(t *testing.T) {
	rows := make([][]string, 0, 60)
	for i := 0; i < 60; i++ {
		rows = append(rows, []string{"7", "red|blue"})
	}
	dict := encoding.NewDictionary()
	for _, v := range []string{"red", "blue", "green"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("seed dictionary: %v", err)
		}
	}
	src := &authoritativeReader{
		columns: []string{"code", "tags"},
		rows:    rows,
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "code", Type: encoding.FieldTypeU16, CsvColumnIdx: 0},
			{Name: "tags", Type: encoding.FieldTypeSetU8, CsvColumnIdx: 1, Dictionary: dict},
		}},
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(src, "out.pulse")
	job.FS = fs
	// Every slot below exists only to steer inference. With an authoritative
	// schema there is nothing to steer, so none of them may take effect.
	job.SampleRows = 1
	job.SetInferenceMinPct = 99
	job.ColumnTypeOverrides = map[string]encoding.FieldType{
		"code": encoding.FieldTypeU64,
		"tags": encoding.FieldTypeCategoricalU8,
	}
	job.SetDelimiters = map[string]string{"tags": ";"}

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsImported != 60 {
		t.Errorf("RowsImported = %d, want 60 (SampleRows must not bound the row pass)", report.RowsImported)
	}

	schema, vals, _ := decodeAll(t, fs, "out.pulse")
	if got := schema.Fields[0].Type; got != encoding.FieldTypeU16 {
		t.Errorf("code type = %s, want u16 — ColumnTypeOverrides must be inert", got)
	}
	if got := schema.Fields[1].Type; got != encoding.FieldTypeSetU8 {
		t.Errorf("tags type = %s, want set_u8 — ColumnTypeOverrides must be inert", got)
	}
	// SetDelimiters said ";", which would leave "red|blue" a single unsplit
	// token. Inert means DefaultSetDelimiter split it into bits 0 and 1.
	if got := vals[0]["tags"]; got != 3 {
		t.Errorf("tags mask = %v, want 3 (red|blue via DefaultSetDelimiter) — SetDelimiters must be inert", got)
	}
	if got := schema.Fields[1].Dictionary.Values(); len(got) != 3 || got[0] != "red" || got[2] != "green" {
		t.Errorf("dictionary = %v, want the authoritative [red blue green]", got)
	}
}

// --- authoritative dictionaries are preserved, not re-derived ---------------

func TestImportJob_SchemaAwareReader_DictionaryOrderPreserved(t *testing.T) {
	// The source declares low=0, mid=1, high=2. The data contains only
	// "high", so a re-derived dictionary would give it ID 0 — silently
	// renumbering the source codes. The authoritative ordering must survive,
	// and an undeclared value appends after it rather than displacing it.
	dict := encoding.NewDictionary()
	for _, v := range []string{"low", "mid", "high"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("seed dictionary: %v", err)
		}
	}
	src := &authoritativeReader{
		columns: []string{"band"},
		rows:    [][]string{{"high"}, {"high"}, {"unlabelled"}},
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "band", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 0, Dictionary: dict},
		}},
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(src, "out.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	schema, vals, _ := decodeAll(t, fs, "out.pulse")
	want := []string{"low", "mid", "high", "unlabelled"}
	got := schema.Fields[0].Dictionary.Values()
	if len(got) != len(want) {
		t.Fatalf("dictionary = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dictionary = %v, want %v", got, want)
		}
	}
	if vals[0]["band"] != 2 {
		t.Errorf("band[0] = %v, want 2 (the source's own code for \"high\")", vals[0]["band"])
	}
	if vals[2]["band"] != 3 {
		t.Errorf("band[2] = %v, want 3 (appended after the declared entries)", vals[2]["band"])
	}
}

// --- no null promotion ------------------------------------------------------

func TestImportJob_SchemaAwareReader_NoNullPromotion(t *testing.T) {
	for _, inferredFlag := range []bool{false, true} {
		name := "InferredSchema=false"
		if inferredFlag {
			name = "InferredSchema=true"
		}
		t.Run(name, func(t *testing.T) {
			src := &authoritativeReader{
				columns: []string{"score"},
				rows:    [][]string{{"1"}, {""}, {"3"}},
				schema: &encoding.Schema{Fields: []encoding.Field{
					{Name: "score", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
				}},
			}
			fs := afero.NewMemMapFs()
			job := NewImportJob(src, "out.pulse")
			job.FS = fs
			// Even an explicit opt-in to inference tolerance must not widen an
			// authoritative field: the dictionary declared it non-nullable.
			job.InferredSchema = inferredFlag

			report, err := job.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(report.PromotedFields) != 0 {
				t.Errorf("PromotedFields = %v, want none", report.PromotedFields)
			}
			if report.Schema.Fields[0].Nullable {
				t.Error("score was widened to nullable; an authoritative schema must not null-promote")
			}
			if len(report.RowErrors) != 1 {
				t.Fatalf("RowErrors = %d, want 1 (the unexpected null)", len(report.RowErrors))
			}
			var ce *perr.CodedError
			if !errors.As(report.RowErrors[0].Err, &ce) {
				t.Fatalf("RowErrors[0] = %v, want a CodedError", report.RowErrors[0].Err)
			}
			if ce.Code != perr.PULSE_IMPORT_ROW_ERROR {
				t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_IMPORT_ROW_ERROR)
			}
			if report.RowsImported != 2 {
				t.Errorf("RowsImported = %d, want 2 (the null row is dropped)", report.RowsImported)
			}
			schema, _, _ := decodeAll(t, fs, "out.pulse")
			if schema.HasBitmap() {
				t.Error("output carries a null bitmap; a wholly non-nullable authoritative schema must not")
			}
		})
	}
}

// --- error / decline / malformed -------------------------------------------

func TestImportJob_SchemaAwareReader_ErrorFailsImport(t *testing.T) {
	tests := []struct {
		name   string
		schema *encoding.Schema
		err    error
	}{
		{
			name: "reader error is never swallowed",
			err:  fmt.Errorf("dictionary unreadable"),
		},
		{
			name:   "schema with no fields",
			schema: &encoding.Schema{},
		},
		{
			name: "negative CsvColumnIdx",
			schema: &encoding.Schema{Fields: []encoding.Field{
				{Name: "score", Type: encoding.FieldTypeU8, CsvColumnIdx: -1},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &authoritativeReader{
				columns: []string{"score"},
				rows:    [][]string{{"1"}},
				schema:  tt.schema,
				err:     tt.err,
			}
			job := NewImportJob(src, "out.pulse")
			job.FS = afero.NewMemMapFs()
			if _, err := job.Run(context.Background()); err == nil {
				t.Fatal("Run succeeded; a broken authoritative schema must fail the import, not fall back to inference")
			}
		})
	}
}

func TestImportJob_SchemaAwareReader_NilSchemaFallsBackToInference(t *testing.T) {
	src := &resettableAuthoritativeReader{
		mockReader: newMockReader([]string{"score"}, [][]string{{"1"}, {"2"}, {"3"}}),
		schema:     nil, // deliberate opt-out
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(src, "out.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if src.calls != 1 {
		t.Errorf("PulseSchema called %d times, want 1", src.calls)
	}
	if report.RowsImported != 3 {
		t.Errorf("RowsImported = %d, want 3", report.RowsImported)
	}
	// Inference ran: it narrows 1/2/3 to u4, which no authoritative schema
	// in this file declares.
	if got := report.Schema.Fields[0].Type; got != encoding.FieldTypeU4 {
		t.Errorf("score type = %s, want u4 from inference", got)
	}
}

// --- predict parity ---------------------------------------------------------

func TestImportJob_Predict_SchemaAwareReader(t *testing.T) {
	src := &authoritativeReader{
		columns: []string{"score"},
		rows:    [][]string{{"1"}, {""}, {"3"}},
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
		}},
	}
	job := NewImportJob(src, "out.pulse")
	job.FS = afero.NewMemMapFs()

	report, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if report.Schema.Fields[0].Type != encoding.FieldTypeF64 {
		t.Errorf("score type = %s, want f64 — Predict must resolve the same schema Run writes", report.Schema.Fields[0].Type)
	}
	if report.Schema.Fields[0].Nullable {
		t.Error("Predict promoted score to nullable; an authoritative schema must not null-promote")
	}
	if len(report.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", report.Warnings)
	}
	if report.EstimatedRows != 3 {
		t.Errorf("EstimatedRows = %d, want 3", report.EstimatedRows)
	}
}
