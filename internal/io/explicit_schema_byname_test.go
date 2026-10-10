package io

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// The by-name resolver (explicit_schema.go) and every caller that runs
// it. The source below has two SAME-TYPED columns, so a positional
// mapping of a reordered schema imports without a single error and
// silently swaps them — the bug the resolver exists to close.

var byNameHeader = []string{"a", "b"}

func byNameRows() [][]string { return [][]string{{"1", "10"}, {"2", "20"}} }

// reorderedSchema declares b before a; CsvColumnIdx is deliberately the
// POSITIONAL value (the array index) a schema-file loader assigns, which
// the resolver must overwrite.
func reorderedSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "b", Type: encoding.FieldTypeU32, CsvColumnIdx: 0},
		{Name: "a", Type: encoding.FieldTypeU32, ByteOffset: 4, CsvColumnIdx: 1},
	}}
}

func schemaOf(names ...string) *encoding.Schema {
	s := &encoding.Schema{}
	for i, n := range names {
		s.Fields = append(s.Fields, encoding.Field{Name: n, Type: encoding.FieldTypeU32, ByteOffset: 4 * i, CsvColumnIdx: i})
	}
	return s
}

func cohortRows(t *testing.T, fs afero.Fs, path string) ([]string, [][]string) {
	t.Helper()
	w := &collectWriter{}
	ej := NewExportJob(path, w)
	ej.FS = fs
	if _, err := ej.Run(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}
	rows := make([][]string, len(w.rows))
	for i, r := range w.rows {
		for _, v := range r {
			rows[i] = append(rows[i], fmt.Sprint(v))
		}
	}
	return w.header, rows
}

func wantCode(t *testing.T, err error, code perr.Code, details map[string]any) {
	t.Helper()
	if !perr.HasCode(err, code) {
		t.Fatalf("err = %v, want %s", err, code)
	}
	ce := codedErr(t, err)
	for k, v := range details {
		if !reflect.DeepEqual(ce.Details[k], v) {
			t.Errorf("details[%q] = %#v, want %#v", k, ce.Details[k], v)
		}
	}
}

func TestResolveExplicitSchema_ReordersByName(t *testing.T) {
	in := reorderedSchema()
	got, err := resolveExplicitSchema(&stringRowsReader{header: byNameHeader}, in, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields[0].CsvColumnIdx != 1 || got.Fields[1].CsvColumnIdx != 0 {
		t.Fatalf("resolved indices = %d,%d, want 1,0", got.Fields[0].CsvColumnIdx, got.Fields[1].CsvColumnIdx)
	}
	// A copy: the caller's schema keeps its own indices.
	if in.Fields[0].CsvColumnIdx != 0 || in.Fields[1].CsvColumnIdx != 1 {
		t.Fatal("resolver mutated the caller's schema")
	}
}

func TestResolveExplicitSchema_Refusals(t *testing.T) {
	cases := []struct {
		name        string
		header      []string
		schema      *encoding.Schema
		ignoreExtra bool
		code        perr.Code
		details     map[string]any
	}{
		{"field missing", []string{"a", "b"}, schemaOf("a", "c"), false,
			perr.PULSE_IMPORT_SCHEMA_FIELD_MISSING, map[string]any{"field": "c", "columns": []string{"a", "b"}}},
		{"field missing despite ignore-extra", []string{"a"}, schemaOf("a", "c"), true,
			perr.PULSE_IMPORT_SCHEMA_FIELD_MISSING, map[string]any{"field": "c"}},
		{"case is not a match", []string{"A"}, schemaOf("a"), true,
			perr.PULSE_IMPORT_SCHEMA_FIELD_MISSING, map[string]any{"field": "a"}},
		{"column unmapped", []string{"a", "b", "z"}, schemaOf("b", "a"), false,
			perr.PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED, map[string]any{"column": "z", "fields": []string{"b", "a"}}},
		{"schema duplicate", []string{"a"}, schemaOf("a", "a"), false,
			perr.PULSE_IMPORT_SCHEMA_DUPLICATE, map[string]any{"field": "a", "side": "schema"}},
		{"header duplicate of a field", []string{"a", "a"}, schemaOf("a"), true,
			perr.PULSE_IMPORT_SCHEMA_DUPLICATE, map[string]any{"column": "a", "side": "header"}},
		{"header duplicate, extras refused", []string{"a", "z", "z"}, schemaOf("a"), false,
			perr.PULSE_IMPORT_SCHEMA_DUPLICATE, map[string]any{"column": "z", "side": "header"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolveExplicitSchema(&stringRowsReader{header: c.header}, c.schema, c.ignoreExtra)
			wantCode(t, err, c.code, c.details)
		})
	}
}

func TestResolveExplicitSchema_IgnoreExtraDropsColumns(t *testing.T) {
	// An unnamed column — repeated, even — is dropped under ignoreExtra.
	got, err := resolveExplicitSchema(&stringRowsReader{header: []string{"z", "b", "z", "a"}}, schemaOf("a", "b"), true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields[0].CsvColumnIdx != 3 || got.Fields[1].CsvColumnIdx != 1 {
		t.Fatalf("resolved indices = %d,%d, want 3,1", got.Fields[0].CsvColumnIdx, got.Fields[1].CsvColumnIdx)
	}
}

func TestResolveExplicitSchema_EmptySourceUnresolved(t *testing.T) {
	got, err := resolveExplicitSchema(&stringRowsReader{}, schemaOf("a"), false)
	if err != nil || len(got.Fields) != 1 {
		t.Fatalf("empty source: %v %v", got, err)
	}
}

// --- every caller ---------------------------------------------------------

func TestImportRun_ReorderedSchemaByName(t *testing.T) {
	fs := afero.NewMemMapFs()
	job := &ImportJob{Source: &stringRowsReader{header: byNameHeader, rows: byNameRows()}, Target: "o.pulse", FS: fs, Schema: reorderedSchema()}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	header, rows := cohortRows(t, fs, "o.pulse")
	if !reflect.DeepEqual(header, []string{"b", "a"}) || !reflect.DeepEqual(rows, [][]string{{"10", "1"}, {"20", "2"}}) {
		t.Fatalf("cohort = %v %v, want [b a] [[10 1] [20 2]]", header, rows)
	}
}

func TestImportRun_RefusesBeforeRows(t *testing.T) {
	job := &ImportJob{Source: &panicRowsReader{t: t, header: []string{"a", "z"}}, Target: "o.pulse", FS: afero.NewMemMapFs(), Schema: schemaOf("a")}
	_, err := job.Run(context.Background())
	wantCode(t, err, perr.PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED, map[string]any{"column": "z"})
	if ok, _ := afero.Exists(job.FS, "o.pulse"); ok {
		t.Fatal("a refused import wrote a cohort")
	}
}

func TestImportRun_IgnoreExtra(t *testing.T) {
	fs := afero.NewMemMapFs()
	src := &stringRowsReader{header: []string{"z", "a", "b"}, rows: [][]string{{"x", "1", "10"}}}
	job := &ImportJob{Source: src, Target: "o.pulse", FS: fs, Schema: schemaOf("b"), IgnoreExtra: true}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, rows := cohortRows(t, fs, "o.pulse"); !reflect.DeepEqual(rows, [][]string{{"10"}}) {
		t.Fatalf("rows = %v, want [[10]]", rows)
	}
}

func TestImportPredict_ResolvesByName(t *testing.T) {
	job := &ImportJob{Source: &stringRowsReader{header: byNameHeader, rows: byNameRows()}, Target: "o.pulse", FS: afero.NewMemMapFs(), Schema: reorderedSchema()}
	rep, err := job.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Schema.Fields[0].CsvColumnIdx != 1 || rep.Schema.Fields[1].CsvColumnIdx != 0 {
		t.Fatalf("predicted indices = %d,%d, want 1,0", rep.Schema.Fields[0].CsvColumnIdx, rep.Schema.Fields[1].CsvColumnIdx)
	}

	bad := &ImportJob{Source: &stringRowsReader{header: byNameHeader, rows: byNameRows()}, Target: "o.pulse", FS: afero.NewMemMapFs(), Schema: schemaOf("a", "c")}
	_, err = bad.Predict(context.Background())
	wantCode(t, err, perr.PULSE_IMPORT_SCHEMA_FIELD_MISSING, map[string]any{"field": "c"})
}

// The measured predict (import_detect.go) converts every row as Run
// does; with a reordered schema its constant-elision verdict must name
// the field that is ACTUALLY constant in the source.
func TestImportPredictMeasured_ResolvesByName(t *testing.T) {
	var rows [][]string
	for i := 0; i < 200; i++ {
		rows = append(rows, []string{"7", fmt.Sprint(i)})
	}
	src := &stringRowsReader{header: []string{"a", "b"}, rows: rows}
	job := &ImportJob{Source: src, Target: "o.pulse", FS: afero.NewMemMapFs(), Schema: schemaOf("b", "a"), ElideConstants: true}
	rep, err := job.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.ElidedConstants, []string{"a"}) {
		t.Fatalf("ElidedConstants = %v, want [a]", rep.ElidedConstants)
	}

	bad := &ImportJob{Source: &stringRowsReader{header: []string{"a", "z"}, rows: byNameRows()}, Target: "o.pulse", FS: afero.NewMemMapFs(), Schema: schemaOf("a"), ElideConstants: true}
	_, err = bad.Predict(context.Background())
	wantCode(t, err, perr.PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED, map[string]any{"column": "z"})
}

func TestConvertRun_ReorderedSchemaByName(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := &collectWriter{}
	job := NewConvertJob(&stringRowsReader{header: byNameHeader, rows: byNameRows()}, w)
	job.FS = fs
	job.Schema = reorderedSchema()
	job.KeepPulseAt = "keep.pulse"
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w.header, []string{"b", "a"}) || !reflect.DeepEqual(w.rows, [][]any{{"10", "1"}, {"20", "2"}}) {
		t.Fatalf("converted = %v %v", w.header, w.rows)
	}
	// The KeepPulseAt intermediate re-imports through the same resolver.
	if _, rows := cohortRows(t, fs, "keep.pulse"); !reflect.DeepEqual(rows, [][]string{{"10", "1"}, {"20", "2"}}) {
		t.Fatalf("intermediate rows = %v", rows)
	}
}

func TestConvertRun_RefusalAndIgnoreExtra(t *testing.T) {
	src := func() *stringRowsReader {
		return &stringRowsReader{header: []string{"a", "z"}, rows: [][]string{{"1", "x"}}}
	}
	job := NewConvertJob(src(), &collectWriter{})
	job.Schema = schemaOf("a")
	_, err := job.Run(context.Background())
	wantCode(t, err, perr.PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED, map[string]any{"column": "z"})

	// IgnoreExtra reaches the KeepPulseAt intermediate too.
	fs := afero.NewMemMapFs()
	w := &collectWriter{}
	job = NewConvertJob(src(), w)
	job.FS = fs
	job.Schema = schemaOf("a")
	job.IgnoreExtra = true
	job.KeepPulseAt = "keep.pulse"
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := afero.Exists(fs, "keep.pulse"); !ok {
		t.Fatal("intermediate not written")
	}
}

func TestConvertPredict_ResolvesByName(t *testing.T) {
	job := NewConvertJob(&stringRowsReader{header: byNameHeader, rows: byNameRows()}, &collectWriter{})
	job.Schema = reorderedSchema()
	rep, err := job.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Schema.Fields[0].CsvColumnIdx != 1 {
		t.Fatalf("predicted b index = %d, want 1", rep.Schema.Fields[0].CsvColumnIdx)
	}
	bad := NewConvertJob(&stringRowsReader{header: byNameHeader, rows: byNameRows()}, &collectWriter{})
	bad.Schema = schemaOf("a", "a")
	_, err = bad.Predict(context.Background())
	wantCode(t, err, perr.PULSE_IMPORT_SCHEMA_DUPLICATE, map[string]any{"field": "a"})
}

// The inference path never consults the resolver: a duplicate header
// still infers as before.
func TestImportInferred_NoResolver(t *testing.T) {
	fs := afero.NewMemMapFs()
	job := &ImportJob{Source: &stringRowsReader{header: []string{"a", "a"}, rows: [][]string{{"1", "2"}}}, Target: "o.pulse", FS: fs}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("inferred import with a repeated header must be unchanged: %v", err)
	}
}
