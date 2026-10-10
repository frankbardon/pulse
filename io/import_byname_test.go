package io_test

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// An explicit import schema is matched to the source's columns BY NAME on
// every adapter. Each source below has two SAME-TYPED columns, so the old
// positional mapping imported a reordered schema without a single error
// and silently swapped the columns.

func catSchema(names ...string) *encoding.Schema {
	s := &encoding.Schema{}
	for i, n := range names {
		s.Fields = append(s.Fields, encoding.Field{Name: n, Type: encoding.FieldTypeCategoricalU8, ByteOffset: i, CsvColumnIdx: i})
	}
	return s
}

// importCohort runs an explicit-schema import of src and returns the
// cohort read back (header + rows, via a CSV export).
func importCohort(t *testing.T, src pio.Reader, schema *encoding.Schema, ignoreExtra bool) ([]string, [][]string, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(src, "c.pulse")
	job.FS = fs
	job.Schema = schema
	job.IgnoreExtra = ignoreExtra
	if _, err := job.Run(context.Background()); err != nil {
		return nil, nil, err
	}
	w, err := pio.NewWriterToBuffer(pio.FormatCSV, pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ej := pio.NewExportJob("c.pulse", w)
	ej.FS = fs
	if _, err := ej.Run(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := pio.NewReaderFromBytes(pio.FormatCSV, w.Bytes(), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h, rows := readRows(t, r)
	return h, rows, nil
}

func hasCode(t *testing.T, err error, code perrors.Code) *perrors.CodedError {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ce
}

// TestImport_ReorderedSchemaByName_EveryAdapter covers csv, tsv, excel,
// ndjson, jsonarray — the positional path — and arrow, parquet, spss,
// where an explicit schema overrides the source's own schema: a reorder
// imports the right columns, a mismatch is refused, and --ignore-extra
// drops an unnamed column.
func TestImport_ReorderedSchemaByName_EveryAdapter(t *testing.T) {
	header := []string{"a", "b", "z"}
	rows := [][]string{{"a1", "b1", "z1"}, {"a2", "b2", "z2"}}
	for _, f := range pio.Formats() {
		if !f.CanRead() || !f.CanWrite() {
			continue
		}
		t.Run(f.String(), func(t *testing.T) {
			bw, err := pio.NewWriterToBuffer(f, pio.WriterOptions{})
			if err != nil {
				t.Fatal(err)
			}
			writeRows(t, bw, header, rows)
			data := bw.Bytes()
			src := func() pio.Reader {
				r, err := pio.NewReaderFromBytes(f, data, pio.ReaderOptions{})
				if err != nil {
					t.Fatal(err)
				}
				return r
			}

			gotH, gotRows, err := importCohort(t, src(), catSchema("z", "b", "a"), false)
			if err != nil {
				t.Fatalf("reordered schema: %v", err)
			}
			wantRows := [][]string{{"z1", "b1", "a1"}, {"z2", "b2", "a2"}}
			if !reflect.DeepEqual(gotH, []string{"z", "b", "a"}) || !reflect.DeepEqual(gotRows, wantRows) {
				t.Fatalf("cohort = %v %v, want [z b a] %v", gotH, gotRows, wantRows)
			}

			_, _, err = importCohort(t, src(), catSchema("b", "a", "c"), false)
			ce := hasCode(t, err, perrors.PULSE_IMPORT_SCHEMA_FIELD_MISSING)
			if ce.Details["field"] != "c" {
				t.Errorf("details = %v", ce.Details)
			}

			_, _, err = importCohort(t, src(), catSchema("b", "a"), false)
			ce = hasCode(t, err, perrors.PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED)
			if ce.Details["column"] != "z" {
				t.Errorf("details = %v", ce.Details)
			}

			_, gotRows, err = importCohort(t, src(), catSchema("b", "a"), true)
			if err != nil {
				t.Fatalf("ignore-extra: %v", err)
			}
			if !reflect.DeepEqual(gotRows, [][]string{{"b1", "a1"}, {"b2", "a2"}}) {
				t.Fatalf("ignore-extra rows = %v", gotRows)
			}
		})
	}
}

// An NDJSON source whose keys are written in sorted order (as the Pulse
// stream emits them) imports correctly under a schema listed in any
// other order — no "keep the schema sorted" rule.
func TestImport_NDJSONUnsortedSchema(t *testing.T) {
	data := []byte(`{"channel":"tv","spend":"x1","week":"w1"}
{"channel":"radio","spend":"x2","week":"w2"}
`)
	r, err := pio.NewReaderFromBytes(pio.FormatNDJSON, data, pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h, rows, err := importCohort(t, r, catSchema("week", "channel", "spend"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h, []string{"week", "channel", "spend"}) ||
		!reflect.DeepEqual(rows, [][]string{{"w1", "tv", "x1"}, {"w2", "radio", "x2"}}) {
		t.Fatalf("cohort = %v %v", h, rows)
	}
}

// A key first seen after the first object is refused under an explicit
// schema (it used to be dropped silently), naming the key and the row;
// --ignore-extra drops it.
func TestImport_LateKeyRefused(t *testing.T) {
	sources := map[pio.Format][]byte{
		pio.FormatNDJSON: []byte(`{"a":"1"}
{"a":"2"}
{"a":"3","late":"x"}
`),
		pio.FormatJSONArray: []byte(`[{"a":"1"},{"a":"2"},{"a":"3","late":"x"}]`),
	}
	for f, data := range sources {
		t.Run(f.String(), func(t *testing.T) {
			src := func() pio.Reader {
				r, err := pio.NewReaderFromBytes(f, data, pio.ReaderOptions{})
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			_, _, err := importCohort(t, src(), catSchema("a"), false)
			ce := hasCode(t, err, perrors.PULSE_IMPORT_SCHEMA_KEY_LATE)
			if ce.Details["column"] != "late" || ce.Details["row"] != 3 {
				t.Errorf("details = %v, want column late, row 3", ce.Details)
			}

			_, rows, err := importCohort(t, src(), catSchema("a"), true)
			if err != nil {
				t.Fatalf("ignore-extra: %v", err)
			}
			if len(rows) != 3 {
				t.Fatalf("rows = %v", rows)
			}

			// The inference path is unchanged: the late key is dropped.
			fs := afero.NewMemMapFs()
			job := pio.NewImportJob(src(), "i.pulse")
			job.FS = fs
			if _, err := job.Run(context.Background()); err != nil {
				t.Fatalf("inferred import: %v", err)
			}
		})
	}
}
