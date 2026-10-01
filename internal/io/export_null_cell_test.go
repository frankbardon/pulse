package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// nullCellSource is a NullAwareReader over fixed rows, the only way to
// author a source in which the empty string is a VALUE rather than the
// null token.
type nullCellSource struct {
	columns []string
	rows    [][]string
	nulls   [][]bool
	cur     []bool
	pos     int
}

func (m *nullCellSource) ReadHeader() ([]string, error) { return m.columns, nil }
func (m *nullCellSource) ReadRows(ctx context.Context, fn func(row []string) error) error {
	for m.pos < len(m.rows) {
		m.cur = m.nulls[m.pos]
		if err := fn(m.rows[m.pos]); err != nil {
			return err
		}
		m.pos++
	}
	return nil
}
func (m *nullCellSource) Close() error     { return nil }
func (m *nullCellSource) Reset() error     { m.pos = 0; return nil }
func (m *nullCellSource) RowNulls() []bool { return m.cur }

var _ NullAwareReader = (*nullCellSource)(nil)

// nullCellCohort writes a three-record cohort whose categorical column
// carries an ordinary value, the empty string AS A VALUE, and a null.
func nullCellCohort(t *testing.T) afero.Fs {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"red", ""} {
		if _, err := dict.AddWithLimit(v, encoding.FieldTypeCategoricalU8.MaxCategoricalEntries()); err != nil {
			t.Fatalf("seeding dictionary: %v", err)
		}
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(&nullCellSource{
		columns: []string{"id", "c"},
		rows:    [][]string{{"1", "red"}, {"2", ""}, {"3", ""}},
		nulls:   [][]bool{{false, false}, {false, false}, {false, true}},
	}, "nc.pulse")
	job.FS = fs
	job.Schema = &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Nullable: true, CsvColumnIdx: 1, Dictionary: dict},
	}}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.RowsImported != 3 {
		t.Fatalf("imported %d rows, want 3 (errors %v)", rep.RowsImported, rep.RowErrors)
	}
	return fs
}

// TestExportJob_NullCellIsNilNotEmptyString is the export half of the
// categorical null contract, asserted at the row level where the
// decision is made.
//
// A categorical dictionary can hold "" as a genuine value, so the two
// cells are the SAME TEXT and the only thing that can separate them is
// the Go value the export loop puts in the row. Spelling a bitmap null
// "" is what made "answered with a blank" and "did not answer" the
// same cell for every adapter downstream.
func TestExportJob_NullCellIsNilNotEmptyString(t *testing.T) {
	fs := nullCellCohort(t)

	w := &collectWriter{}
	ex := NewExportJob("nc.pulse", w)
	ex.FS = fs
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(w.rows) != 3 {
		t.Fatalf("exported %d rows, want 3", len(w.rows))
	}

	if got, ok := w.rows[0][1].(string); !ok || got != "red" {
		t.Errorf("ordinary value exported as %#v, want %q", w.rows[0][1], "red")
	}
	if got, ok := w.rows[1][1].(string); !ok || got != "" {
		t.Errorf("empty-string VALUE exported as %#v, want the empty string", w.rows[1][1])
	}
	if w.rows[2][1] != nil {
		t.Errorf("null cell exported as %#v, want an untyped nil", w.rows[2][1])
	}
	if w.rows[1][1] == w.rows[2][1] {
		t.Fatalf("empty-string value and null both exported as %#v", w.rows[1][1])
	}
}

// TestExportJob_DeclaresExplicitNullsToNullAwareTargets pins the
// provenance rule: the export path tells a null-aware writer that nil
// is the only null cell, and does so BEFORE the header so the writer
// can act on it from the first row. ConvertJob makes no such call, and
// that asymmetry is what keeps a convert's "" — which IS the source
// null token there — reading as a null.
func TestExportJob_DeclaresExplicitNullsToNullAwareTargets(t *testing.T) {
	fs := nullCellCohort(t)

	w := &nullAwareCollectWriter{}
	ex := NewExportJob("nc.pulse", w)
	ex.FS = fs
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}
	if !w.explicit {
		t.Fatal("ExportJob.Run did not call SetExplicitNulls on a NullAwareWriter target")
	}
	if !w.explicitBeforeHeader {
		t.Error("SetExplicitNulls was called after WriteHeader; a writer that emits typed " +
			"columns at header time cannot act on it there")
	}
}

type nullAwareCollectWriter struct {
	collectWriter
	explicit             bool
	explicitBeforeHeader bool
	headerWritten        bool
}

func (w *nullAwareCollectWriter) SetExplicitNulls(on bool) {
	w.explicit = on
	w.explicitBeforeHeader = !w.headerWritten
}

func (w *nullAwareCollectWriter) WriteHeader(cols []string) error {
	w.headerWritten = true
	return w.collectWriter.WriteHeader(cols)
}

var _ NullAwareWriter = (*nullAwareCollectWriter)(nil)

// TestIsNullCell_TwoConventions pins the writer-side helper directly:
// the same "" is the absent cell on the convert (text) path and a
// value on the export path, and nil is absent on both.
func TestIsNullCell_TwoConventions(t *testing.T) {
	cases := []struct {
		v        any
		explicit bool
		want     bool
	}{
		{nil, false, true},
		{nil, true, true},
		{"", false, true},
		{"", true, false},
		{"red", false, false},
		{"red", true, false},
		// Never widened to the other null tokens: that would change
		// what a convert emits for a literal "na" cell.
		{"na", false, false},
		{"null", false, false},
	}
	for _, c := range cases {
		if got := IsNullCell(c.v, c.explicit); got != c.want {
			t.Errorf("IsNullCell(%#v, explicit=%v) = %v, want %v", c.v, c.explicit, got, c.want)
		}
	}
}
