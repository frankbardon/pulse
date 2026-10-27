package pulse

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// rowLabelsPulse builds a cohort with one field of each label class a row
// export distinguishes — a categorical, an integer-coded u8 and the
// numeric / date types a label can never key — and an instance with a
// table for each binding shape.
func rowLabelsPulse(t *testing.T) *Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	b, err := iio.NewCohortBuild(context.Background(), fsys, "c.pulse", &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "n", Type: encoding.FieldTypeU8},
		{Name: "score", Type: encoding.FieldTypeF64},
		{Name: "big", Type: encoding.FieldTypeU64},
		{Name: "day", Type: encoding.FieldTypeDate},
	}}, iio.CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{
		{"NE", uint64(1), 1.5, uint64(1), int32(19000)},
		{"SO", uint64(2), 2.5, uint64(2), int32(19001)},
		{"WE", uint64(7), 3.5, uint64(7), int32(19002)},
	} {
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: fsys, Extensions: Extensions{LabelTables: map[string]LabelTable{
		"regions": {Rows: map[string]string{"NE": "Northeast", "SO": "South"}},
		"counts":  {Rows: map[string]string{"1": "One", "2": "Two", "9": "Nine"}},
		"padded":  {Rows: map[string]string{"01": "One"}},
		"wide":    {Rows: map[string]string{"256": "Too wide"}},
		"floats":  {Rows: map[string]string{"1.5": "Low"}},
		"closure": {Lookup: func(k string) (string, bool, error) {
			if k == "1" {
				return "Uno", true, nil
			}
			return "", false, nil
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func exportRowLabels(t *testing.T, p *Pulse, f pio.Format, bindings ...types.LabelBinding) ([]byte, *pio.ExportReport) {
	t.Helper()
	w, err := pio.NewWriterToBuffer(f, pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	j := pio.NewExportJob("c.pulse", w)
	j.Includes = []string{"region", "n"}
	for i := range bindings {
		j.Labels = append(j.Labels, &bindings[i])
	}
	rep, err := p.Export(context.Background(), j)
	if err != nil {
		t.Fatalf("%s export: %v", f, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.Bytes(), rep
}

// TestExport_IntegerCodedFieldLabels: a row export binding a label table to
// an integer-coded u8 field labels it like a categorical. Replace writes the
// label text in place (a typed format types the column as text), augment
// fills `<field>_label`; an unmatched code behaves as an unmatched
// categorical value — replace keeps the raw code's spelling, augment
// leaves the sibling empty, and PULSE_LABEL_LOOKUP_MISS summarises it.
func TestExport_IntegerCodedFieldLabels(t *testing.T) {
	t.Parallel()

	p := rowLabelsPulse(t)

	csv, rep := exportRowLabels(t, p, pio.FormatCSV, types.LabelBinding{Field: "n", Table: "counts"})
	if want := "region,n\nNE,One\nSO,Two\nWE,7\n"; string(csv) != want {
		t.Errorf("csv replace =\n%s\nwant\n%s", csv, want)
	}
	if !hasLabelWarning(rep, "PULSE_LABEL_LOOKUP_MISS") {
		t.Errorf("unmatched code 7 raised no PULSE_LABEL_LOOKUP_MISS: %+v", rep.LabelWarnings)
	}

	csv, _ = exportRowLabels(t, p, pio.FormatCSV, types.LabelBinding{Field: "n", Table: "counts", Mode: types.LabelModeAugment})
	if want := "region,n,n_label\nNE,1,One\nSO,2,Two\nWE,7,\n"; string(csv) != want {
		t.Errorf("csv augment =\n%s\nwant\n%s", csv, want)
	}

	// A Lookup-only table labels per cell: a row writer needs no entry list.
	csv, _ = exportRowLabels(t, p, pio.FormatCSV, types.LabelBinding{Field: "n", Table: "closure"})
	if want := "region,n\nNE,Uno\nSO,2\nWE,7\n"; string(csv) != want {
		t.Errorf("csv lookup replace =\n%s\nwant\n%s", csv, want)
	}

	// Arrow types its columns: a replaced integer column is text, an
	// augmented one keeps its integer type beside a text sibling.
	for _, tc := range []struct {
		mode  types.LabelMode
		cols  []string
		types []arrow.Type
		cells [][]string
	}{
		{types.LabelModeReplace, []string{"region", "n"}, []arrow.Type{arrow.STRING, arrow.STRING},
			[][]string{{"NE", "SO", "WE"}, {"One", "Two", "7"}}},
		{types.LabelModeAugment, []string{"region", "n", "n_label"}, []arrow.Type{arrow.STRING, arrow.UINT8, arrow.STRING},
			[][]string{{"NE", "SO", "WE"}, {"1", "2", "7"}, {"One", "Two", ""}}},
	} {
		data, _ := exportRowLabels(t, p, pio.FormatArrow, types.LabelBinding{Field: "n", Table: "counts", Mode: tc.mode})
		rdr, err := ipc.NewFileReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		sc := rdr.Schema()
		if sc.NumFields() != len(tc.cols) {
			t.Fatalf("%s: arrow schema %v, want columns %v", tc.mode, sc, tc.cols)
		}
		rec, err := rdr.RecordBatch(0)
		if err != nil {
			t.Fatal(err)
		}
		for i, name := range tc.cols {
			if got := sc.Field(i); got.Name != name || got.Type.ID() != tc.types[i] {
				t.Errorf("%s: arrow column %d = %s %s, want %s %s", tc.mode, i, got.Name, got.Type, name, tc.types[i])
			}
			for r, want := range tc.cells[i] {
				if got := rec.Column(i).ValueStr(r); got != want {
					t.Errorf("%s: arrow %s[%d] = %q, want %q", tc.mode, name, r, got, want)
				}
			}
		}
		_ = rdr.Close()
	}

	// Parquet carries the same cells.
	data, _ := exportRowLabels(t, p, pio.FormatParquet, types.LabelBinding{Field: "n", Table: "counts"})
	r, err := pio.NewReaderFromBytes(pio.FormatParquet, data, pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := r.ReadRows(context.Background(), func(row []string) error { got = append(got, row[1]); return nil }); err != nil {
		t.Fatal(err)
	}
	if want := []string{"One", "Two", "7"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("parquet replace n = %v, want %v", got, want)
	}
}

// TestExport_CategoricalLabelsUnchanged pins a categorical binding's row
// export byte for byte: the integer-coded rule adds a branch beside it and
// must not move it.
func TestExport_CategoricalLabelsUnchanged(t *testing.T) {
	t.Parallel()

	p := rowLabelsPulse(t)
	csv, _ := exportRowLabels(t, p, pio.FormatCSV, types.LabelBinding{Field: "region", Table: "regions"})
	if want := "region,n\nNortheast,1\nSouth,2\nWE,7\n"; string(csv) != want {
		t.Errorf("csv categorical replace =\n%s\nwant\n%s", csv, want)
	}
	csv, _ = exportRowLabels(t, p, pio.FormatCSV, types.LabelBinding{Field: "region", Table: "regions", Mode: types.LabelModeAugment})
	if want := "region,region_label,n\nNE,Northeast,1\nSO,South,2\nWE,,7\n"; string(csv) != want {
		t.Errorf("csv categorical augment =\n%s\nwant\n%s", csv, want)
	}
}

// TestPredictExport_RowLabelRefusalsMatchExport: a row export refuses a
// binding it could never apply — a field that is neither categorical nor
// integer-coded, or an integer-coded field whose table keys are not values
// it stores — with the code the `.sav` writer raises, and export predict
// (with a row target or none) raises the same code.
func TestPredictExport_RowLabelRefusalsMatchExport(t *testing.T) {
	t.Parallel()

	p := rowLabelsPulse(t)
	code := func(err error) perrors.Code {
		var ce *perrors.CodedError
		if stderrors.As(err, &ce) {
			return ce.Code
		}
		if err != nil {
			return "uncoded: " + perrors.Code(err.Error())
		}
		return ""
	}
	for _, tc := range []struct {
		name    string
		binding types.LabelBinding
		want    perrors.Code
	}{
		{"categorical", types.LabelBinding{Field: "region", Table: "regions"}, ""},
		{"integer coded", types.LabelBinding{Field: "n", Table: "counts", Mode: types.LabelModeAugment}, ""},
		{"integer coded lookup-only table", types.LabelBinding{Field: "n", Table: "closure"}, ""},
		{"integer coded text keys", types.LabelBinding{Field: "n", Table: "regions"}, perrors.PULSE_LABEL_KEY_INVALID},
		{"integer coded leading zero", types.LabelBinding{Field: "n", Table: "padded"}, perrors.PULSE_LABEL_KEY_INVALID},
		{"integer coded past the type", types.LabelBinding{Field: "n", Table: "wide", Mode: types.LabelModeAugment}, perrors.PULSE_LABEL_KEY_INVALID},
		{"float", types.LabelBinding{Field: "score", Table: "floats"}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"u64", types.LabelBinding{Field: "big", Table: "counts"}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"date", types.LabelBinding{Field: "day", Table: "counts", Mode: types.LabelModeAugment}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
	} {
		for _, f := range []pio.Format{pio.FormatCSV, pio.FormatParquet, pio.FormatExcel, ""} {
			t.Run(tc.name+"/"+string(f), func(t *testing.T) {
				job := func() (*pio.ExportJob, pio.BufferWriter) {
					var w pio.BufferWriter
					if f != "" {
						var err error
						if w, err = pio.NewWriterToBuffer(f, pio.WriterOptions{}); err != nil {
							t.Fatal(err)
						}
					}
					j := pio.NewExportJob("c.pulse", w)
					if w == nil {
						j.Target = nil
					}
					b := tc.binding
					j.Labels = []*types.LabelBinding{&b}
					return j, w
				}
				pj, _ := job()
				_, perr := p.PredictExport(context.Background(), pj)
				if got := code(perr); got != tc.want {
					t.Errorf("PredictExport code = %q, want %q", got, tc.want)
				}
				if f == "" {
					return
				}
				rj, w := job()
				_, rerr := p.Export(context.Background(), rj)
				if got := code(rerr); got != tc.want {
					t.Errorf("Export code = %q, want %q", got, tc.want)
				}
				if tc.want != "" {
					_ = w.Close()
					if f == pio.FormatCSV && len(w.Bytes()) != 0 {
						t.Errorf("a refused export wrote %q", w.Bytes())
					}
				}
			})
		}
	}
}

func hasLabelWarning(rep *pio.ExportReport, code string) bool {
	if rep == nil {
		return false
	}
	for _, w := range rep.LabelWarnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
