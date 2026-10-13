package spss

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/spf13/afero"
)

// vecCohort writes a cohort through the row builder.
func vecCohort(t *testing.T, s *encoding.Schema, rows [][]any) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	b, err := pio.NewCohortBuild(context.Background(), fs, "vec.pulse", s, pio.CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for k, row := range rows {
		if err := b.Append(row); err != nil {
			t.Fatalf("row %d: %v", k, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return fs, "vec.pulse"
}

func vecSPSSSchema(labels []string) *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8},
		{Name: "r", Type: encoding.FieldTypeVecF64, Nullable: true},
		{Name: "e", Type: encoding.FieldTypeVecF32},
	}, Vectors: []encoding.VectorField{
		{FieldIndex: 1, Dim: 2, Labels: labels},
		{FieldIndex: 2, Dim: 2},
	}}
}

func vecSPSSRows() [][]any {
	return [][]any{
		{uint64(1), []float64{1.5, math.NaN()}, []float32{0.25, -3}},
		{uint64(2), nil, []float32{7, 8}},
	}
}

// exportSav runs a real ExportJob against the `.sav` writer.
func exportVecSav(t *testing.T, fs afero.Fs, cohort string, opts WriterOptions) ([]byte, *pio.ExportReport, error) {
	t.Helper()
	w := NewWriter(fs, "out.sav", opts)
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		return nil, nil, err
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return w.Bytes(), rep, nil
}

// TestExportJob_SPSS_VectorExpandsToElementVariables: a vector becomes one
// numeric variable per element, named <field>_<label> (<field>_1..N with no
// labels), its element label as the variable label; a NaN element and every
// element of a null vector read back system-missing, and every case is
// written.
func TestExportJob_SPSS_VectorExpandsToElementVariables(t *testing.T) {
	fs, cohort := vecCohort(t, vecSPSSSchema([]string{"a", "b"}), vecSPSSRows())
	sav, rep, err := exportVecSav(t, fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.RowsExported != 2 {
		t.Fatalf("RowsExported = %d, want 2", rep.RowsExported)
	}

	r := NewReaderFromBytes(sav)
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"id", "r_a", "r_b", "e_1", "e_2"}; !reflect.DeepEqual(header, want) {
		t.Fatalf("variables %v, want %v", header, want)
	}
	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"1", "1.5", "", "0.25", "-3"}, {"2", "", "", "7", "8"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("cases %v, want %v", rows, want)
	}

	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatal(err)
	}
	for name, label := range map[string]string{"r_a": "a", "r_b": "b"} {
		if v := readVar(t, d, name); !v.hasLabel || v.label != label {
			t.Errorf("%s: label %q (has %v), want %q", name, v.label, v.hasLabel, label)
		}
	}
	if v := readVar(t, d, "e_1"); v.hasLabel || v.width != 0 {
		t.Errorf("e_1: label %q width %d, want an unlabelled numeric", v.label, v.width)
	}
}

// TestExportJob_SPSS_VectorNamePolicy: element names go through the
// ordinary SPSS name policy — an illegal one is refused unless
// --sanitize-names repairs it, and one that repeats another variable's
// name is a collision, never a silent rename. ValidateCohort (export
// predict) answers the same.
func TestExportJob_SPSS_VectorNamePolicy(t *testing.T) {
	fs, cohort := vecCohort(t, vecSPSSSchema([]string{"a b", "c"}), vecSPSSRows())
	if _, _, err := exportVecSav(t, fs, cohort, WriterOptions{}); codeOf(t, err) != perr.PULSE_SPSS_NAME_INVALID {
		t.Fatalf("err = %v, want PULSE_SPSS_NAME_INVALID", err)
	}
	w := NewWriter(fs, "p.sav", WriterOptions{})
	if _, err := w.ValidateCohort(context.Background(), pio.CohortSource{FS: fs, Path: cohort}); codeOf(t, err) != perr.PULSE_SPSS_NAME_INVALID {
		t.Fatalf("validate err = %v, want PULSE_SPSS_NAME_INVALID", err)
	}
	sav, rep, err := exportVecSav(t, fs, cohort, WriterOptions{SanitizeNames: true})
	if err != nil {
		t.Fatalf("sanitized export: %v", err)
	}
	if rep.RowsExported != 2 || len(sav) == 0 {
		t.Fatalf("sanitized export wrote %d cases", rep.RowsExported)
	}

	clash := &encoding.Schema{Fields: []encoding.Field{
		{Name: "r_a", Type: encoding.FieldTypeF64},
		{Name: "r", Type: encoding.FieldTypeVecF64},
	}, Vectors: []encoding.VectorField{{FieldIndex: 1, Dim: 2, Labels: []string{"a", "b"}}}}
	fs, cohort = vecCohort(t, clash, [][]any{{1.0, []float64{2, 3}}})
	if _, _, err := exportVecSav(t, fs, cohort, WriterOptions{}); codeOf(t, err) != perr.PULSE_SPSS_NAME_COLLISION {
		t.Fatalf("err = %v, want PULSE_SPSS_NAME_COLLISION", err)
	}
}

// TestBuildDictionary_SidecarBesideAVectorIsRefused: no SPSS import makes a
// vector, so a sidecar beside one does not describe the cohort and is
// refused by name — before the vector could surface as an unmapped column
// or bind to a same-named source variable.
func TestBuildDictionary_SidecarBesideAVectorIsRefused(t *testing.T) {
	schema, res := exportFixture(t, richSpec())
	withVec := &encoding.Schema{Fields: append(append([]encoding.Field(nil), schema.Fields...),
		encoding.Field{Name: "emb", Type: encoding.FieldTypeVecF64})}
	withVec.Vectors = []encoding.VectorField{{FieldIndex: len(schema.Fields), Dim: 2}}

	_, err := BuildDictionary(DictionaryRequest{Schema: withVec, Sidecar: res, Cases: 3})
	if got := codeOf(t, err); got != perr.PULSE_SPSS_SIDECAR_INVALID {
		t.Fatalf("code = %s, want PULSE_SPSS_SIDECAR_INVALID", got)
	}
	if f := err.(*perr.CodedError).Details[perr.DetailSPSSField]; f != "emb" {
		t.Fatalf("details field = %v, want emb", f)
	}
}

// TestDataEncoder_VectorElementOutOfRangeRefused: an element variable
// pointing past its field's dim is refused at plan time, where predict can
// see it.
func TestDataEncoder_VectorElementOutOfRangeRefused(t *testing.T) {
	s := vecSPSSSchema(nil)
	plan, err := BuildDictionary(DictionaryRequest{Schema: s, Cases: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Columns {
		if plan.Columns[i].Encoding == EncodeVectorElement {
			plan.Columns[i].VecElem = 2
			break
		}
	}
	if _, err := NewDataEncoder(plan, s); codeOf(t, err) != perr.PULSE_SPSS_EXPORT_UNSUPPORTED {
		t.Fatalf("err = %v, want PULSE_SPSS_EXPORT_UNSUPPORTED", err)
	}
}
