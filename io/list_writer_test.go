package io_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// An embedder's own writer gets the native list channel the built-in
// list targets use: implementing the public pio.ListAwareWriter is all
// it takes for a vector to arrive as one column of []float64 cells
// rather than expanded scalar text.

// memListWriter is a custom (non-built-in) target recording what the
// export hands it, copying each vector cell (the export reuses it).
type memListWriter struct {
	lists  []pio.ListColumn
	header []string
	rows   [][]any
}

func (w *memListWriter) SetListColumns(cols []pio.ListColumn) { w.lists = cols }
func (w *memListWriter) WriteHeader(cols []string) error      { w.header = cols; return nil }
func (w *memListWriter) Close() error                         { return nil }
func (w *memListWriter) WriteRow(values []any) error {
	row := make([]any, len(values))
	for i, v := range values {
		if vec, ok := v.([]float64); ok {
			v = append([]float64(nil), vec...)
		}
		row[i] = v
	}
	w.rows = append(w.rows, row)
	return nil
}

// plainWriter hides the list channel.
type plainWriter struct{ inner *memListWriter }

func (p plainWriter) WriteHeader(cols []string) error { return p.inner.WriteHeader(cols) }
func (p plainWriter) WriteRow(values []any) error     { return p.inner.WriteRow(values) }
func (p plainWriter) Close() error                    { return p.inner.Close() }

// TestListAwareWriter_CustomWriterMatchesNDJSON: a custom list writer
// receives the vector as the very cells the built-in NDJSON writer turns
// into its JSON arrays; without the interface the same writer gets the
// expanded v_1..v_3 text columns.
func TestListAwareWriter_CustomWriterMatchesNDJSON(t *testing.T) {
	t.Parallel()

	nd, err := pio.NewReaderFromBytes(pio.FormatNDJSON, []byte(listNDJSON), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := importVector(t, nd)
	if err != nil {
		t.Fatal(err)
	}
	export := func(w pio.Writer) {
		t.Helper()
		fs := afero.NewMemMapFs()
		if err := afero.WriteFile(fs, "in.pulse", raw, 0o644); err != nil {
			t.Fatal(err)
		}
		job := pio.NewExportJob("in.pulse", w)
		job.FS = fs
		rep, err := job.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if rep.RowsExported != 5 {
			t.Fatalf("exported %d rows, want 5", rep.RowsExported)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}

	custom := &memListWriter{}
	export(custom)
	if want := []string{"id", "v"}; !reflect.DeepEqual(custom.header, want) {
		t.Fatalf("header %v, want %v", custom.header, want)
	}
	if len(custom.lists) != 1 || custom.lists[0].Index != 1 || custom.lists[0].Dim != 3 {
		t.Fatalf("list columns %+v", custom.lists)
	}

	ndw, err := pio.NewWriterToBuffer(pio.FormatNDJSON, pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	export(ndw)
	lines := bytes.Split(bytes.TrimSpace(ndw.Bytes()), []byte("\n"))
	for i, line := range lines {
		var obj struct{ V []*float64 }
		if err := json.Unmarshal(line, &obj); err != nil {
			t.Fatal(err)
		}
		cell, _ := custom.rows[i][1].([]float64)
		if (obj.V == nil) != (cell == nil) || len(obj.V) != len(cell) {
			t.Fatalf("row %d: ndjson %s, custom %v", i, line, cell)
		}
		for e, p := range obj.V {
			if (p == nil) != math.IsNaN(cell[e]) || (p != nil && *p != cell[e]) {
				t.Fatalf("row %d element %d: ndjson %s, custom %v", i, e, line, cell)
			}
		}
	}

	expanded := &memListWriter{}
	export(plainWriter{expanded})
	if want := []string{"id", "v_1", "v_2", "v_3"}; !reflect.DeepEqual(expanded.header, want) {
		t.Fatalf("expanded header %v, want %v", expanded.header, want)
	}
	if want := []any{"2", "4.5", nil, "6"}; !reflect.DeepEqual(expanded.rows[1], want) {
		t.Fatalf("expanded row %#v, want %#v", expanded.rows[1], want)
	}
}
