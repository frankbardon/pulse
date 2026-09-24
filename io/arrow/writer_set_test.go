package arrow

import (
	"context"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// setRowsReader is a minimal ResetReader over in-memory rows, enough to
// drive pio.ImportJob's inference pass.
type setRowsReader struct {
	cols []string
	rows [][]string
}

func (m *setRowsReader) ReadHeader() ([]string, error) { return m.cols, nil }
func (m *setRowsReader) ReadRows(ctx context.Context, fn func([]string) error) error {
	for _, r := range m.rows {
		if err := fn(append([]string{}, r...)); err != nil {
			return err
		}
	}
	return nil
}
func (m *setRowsReader) Close() error { return nil }
func (m *setRowsReader) Reset() error { return nil }

// buildSetCohort imports a two-column cohort whose "issuers" column is a
// pipe-delimited set drawn from a `tokens`-sized alphabet.
func buildSetCohort(t *testing.T, tokens, rows int) (afero.Fs, []string, encoding.FieldType) {
	t.Helper()
	cats := []string{"A", "B", "C", "D"}
	raw := make([][]string, 0, rows)
	cells := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		cell := "T" + strconv.Itoa((2*i)%tokens) + "|T" + strconv.Itoa((2*i+1)%tokens)
		cells = append(cells, cell)
		raw = append(raw, []string{cats[i%len(cats)], cell})
	}
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(&setRowsReader{cols: []string{"id", "issuers"}, rows: raw}, "s.pulse")
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("import(%d tokens): %v", tokens, err)
	}
	if rep.RowsImported != rows {
		t.Fatalf("import(%d tokens): RowsImported = %d, want %d", tokens, rep.RowsImported, rows)
	}
	return fs, cells, rep.Schema.Field("issuers").Type
}

// TestArrow_SetColumnExportsAsList is the Arrow half of the set
// round-trip. TypeFromPulse maps every set rung to LIST<UTF8>, but the
// writer used to hand that column a single pre-joined STRING through
// the generic AppendValueFromString path — which the list builder
// parses as JSON and rejects. Every row became a RowError and the
// export emitted ZERO rows while reporting success.
func TestArrow_SetColumnExportsAsList(t *testing.T) {
	cases := []struct {
		tokens int
		want   encoding.FieldType
	}{
		{6, encoding.FieldTypeSetU8},
		{206, encoding.FieldTypeSetU256},
	}
	const rows = 480
	for _, c := range cases {
		fs, cells, ft := buildSetCohort(t, c.tokens, rows)
		if ft != c.want {
			t.Fatalf("%d tokens: inferred %s, want %s", c.tokens, ft, c.want)
		}

		w := NewWriterToBuffer()
		ex := pio.NewExportJob("s.pulse", w)
		ex.FS = fs
		rep, err := ex.Run(context.Background())
		if err != nil {
			t.Fatalf("%s: export: %v", ft, err)
		}
		if len(rep.RowErrors) != 0 {
			t.Fatalf("%s: %d row errors, want 0; first = %v",
				ft, len(rep.RowErrors), rep.RowErrors[0].Err)
		}
		if rep.RowsExported != rows {
			t.Fatalf("%s: RowsExported = %d, want %d", ft, rep.RowsExported, rows)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("%s: Close: %v", ft, err)
		}

		// Read the Arrow file back: the set column must decode to the
		// same pipe-joined token list, so a re-import re-infers the
		// same rung.
		r := NewReaderFromBytes(w.Bytes())
		defer r.Close()
		header, err := r.ReadHeader()
		if err != nil {
			t.Fatalf("%s: ReadHeader: %v", ft, err)
		}
		col := -1
		for i, h := range header {
			if h == "issuers" {
				col = i
			}
		}
		if col < 0 {
			t.Fatalf("%s: issuers missing from %v", ft, header)
		}
		var got []string
		if err := r.ReadRows(context.Background(), func(row []string) error {
			got = append(got, row[col])
			return nil
		}); err != nil {
			t.Fatalf("%s: ReadRows: %v", ft, err)
		}
		if len(got) != rows {
			t.Fatalf("%s: read back %d rows, want %d", ft, len(got), rows)
		}
		for i := range cells {
			if got[i] != cells[i] {
				t.Fatalf("%s row %d: round-tripped %q, want %q", ft, i, got[i], cells[i])
			}
		}
	}
}

// TestArrow_SetColumnSchemaIsList pins the exported Arrow schema for a
// wide set column: a LIST<UTF8>, not a stringified scalar.
func TestArrow_SetColumnSchemaIsList(t *testing.T) {
	fs, _, ft := buildSetCohort(t, 206, 480)
	if ft != encoding.FieldTypeSetU256 {
		t.Fatalf("inferred %s, want set_u256", ft)
	}
	w := NewWriterToBuffer()
	ex := pio.NewExportJob("s.pulse", w)
	ex.FS = fs
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r := NewReaderFromBytes(w.Bytes())
	defer r.Close()
	psc, err := r.InferPulseSchema()
	if err != nil {
		t.Fatalf("InferPulseSchema: %v", err)
	}
	f := psc.Field("issuers")
	if f == nil {
		t.Fatalf("issuers missing from the round-tripped schema")
	}
	if !f.Type.IsSet() {
		t.Errorf("round-tripped issuers type = %s, want a set rung", f.Type)
	}
}
