package io

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// vecExportSchema: a scalar, a labelled vec_f64, a categorical, an
// unlabelled nullable vec_f32 and a trailing scalar — so a vector sits
// on both sides of an ordinary column and a label-augment sibling.
func vecExportSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8},
		{Name: "a", Type: encoding.FieldTypeVecF64},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8},
		{Name: "b", Type: encoding.FieldTypeVecF32, Nullable: true},
		{Name: "z", Type: encoding.FieldTypeF64},
	}, Vectors: []encoding.VectorField{
		{FieldIndex: 1, Dim: 2, Labels: []string{"x", "y"}},
		{FieldIndex: 3, Dim: 3},
	}}
}

func writeVecExportCohort(t *testing.T, s *encoding.Schema, rows [][]any) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	b, err := NewCohortBuild(context.Background(), fs, "v.pulse", s, CohortBuildOptions{})
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
	return fs
}

func vecExportRows() [][]any {
	return [][]any{
		{uint64(1), []float64{0.1, -2.5}, "p", []float32{1.5, float32(math.NaN()), 3}, 9.0},
		{uint64(2), []float64{math.NaN(), 1e-7}, "q", nil, 0.5},
	}
}

// listCollectWriter is collectWriter plus the native list channel.
type listCollectWriter struct {
	collectWriter
	lists []ListColumn
}

func (w *listCollectWriter) SetListColumns(cols []ListColumn) { w.lists = cols }

func (w *listCollectWriter) WriteRow(values []any) error {
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

// schemaCollectWriter records the schema SetPulseSchema hands it.
type schemaCollectWriter struct {
	collectWriter
	schema *encoding.Schema
}

func (w *schemaCollectWriter) SetPulseSchema(s *encoding.Schema) { w.schema = s }

// TestExport_VectorExpanded: a writer without native lists gets each
// vector as one column per element, named <field>_<label> or
// <field>_1..N, in the vector's place; a NaN element and every element
// of a null vector are nil; vec_f32 renders at float32 precision. A
// label-augment sibling and a projection land on the right columns.
func TestExport_VectorExpanded(t *testing.T) {
	t.Parallel()

	fs := writeVecExportCohort(t, vecExportSchema(), vecExportRows())

	w := &collectWriter{}
	job := NewExportJob("v.pulse", w)
	job.FS = fs
	job.LabelResolver = newFakeResolver("c", types.LabelModeAugment, map[string]string{"p": "Pea"})
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.RowsExported != 2 || len(w.rows) != 2 {
		t.Fatalf("exported %d rows (%d written), want 2", rep.RowsExported, len(w.rows))
	}
	wantHeader := []string{"id", "a_x", "a_y", "c", "c_label", "b_1", "b_2", "b_3", "z"}
	if !reflect.DeepEqual(w.header, wantHeader) {
		t.Fatalf("header %v, want %v", w.header, wantHeader)
	}
	want := [][]any{
		{"1", "0.1", "-2.5", "p", "Pea", "1.5", nil, "3", "9"},
		{"2", nil, "0.0000001", "q", "", nil, nil, nil, "0.5"},
	}
	if !reflect.DeepEqual(w.rows, want) {
		t.Fatalf("rows\n got %#v\nwant %#v", w.rows, want)
	}

	// Projection selects a vector by its FIELD name and keeps its
	// expansion whole.
	w = &collectWriter{}
	job = NewExportJob("v.pulse", w)
	job.FS = fs
	job.Includes = []string{"z", "b"}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"b_1", "b_2", "b_3", "z"}; !reflect.DeepEqual(w.header, want) {
		t.Fatalf("projected header %v, want %v", w.header, want)
	}
	if want := []any{"1.5", nil, "3", "9"}; !reflect.DeepEqual(w.rows[0], want) {
		t.Fatalf("projected row %#v, want %#v", w.rows[0], want)
	}
}

// TestExport_VectorListColumns: a ListAwareWriter keeps each vector as
// ONE column holding the []float64 (nil when null, NaN verbatim), and is
// told its position, type, dim and labels — after a label sibling that
// shifts it.
func TestExport_VectorListColumns(t *testing.T) {
	t.Parallel()

	fs := writeVecExportCohort(t, vecExportSchema(), vecExportRows())
	w := &listCollectWriter{}
	job := NewExportJob("v.pulse", w)
	job.FS = fs
	job.LabelResolver = newFakeResolver("c", types.LabelModeAugment, map[string]string{"p": "Pea"})
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"id", "a", "c", "c_label", "b", "z"}; !reflect.DeepEqual(w.header, want) {
		t.Fatalf("header %v, want %v", w.header, want)
	}
	wantLists := []ListColumn{
		{Index: 1, Name: "a", Type: encoding.FieldTypeVecF64, Dim: 2, Labels: []string{"x", "y"}},
		{Index: 4, Name: "b", Type: encoding.FieldTypeVecF32, Dim: 3, Nullable: true},
	}
	if !reflect.DeepEqual(w.lists, wantLists) {
		t.Fatalf("lists %+v, want %+v", w.lists, wantLists)
	}
	if len(w.rows) != 2 {
		t.Fatalf("rows %d, want 2", len(w.rows))
	}
	a0, _ := w.rows[0][1].([]float64)
	b0, _ := w.rows[0][4].([]float64)
	if !reflect.DeepEqual(a0, []float64{0.1, -2.5}) || len(b0) != 3 || b0[0] != 1.5 || !math.IsNaN(b0[1]) || b0[2] != 3 {
		t.Fatalf("row 0 vectors a=%v b=%v", a0, b0)
	}
	if w.rows[1][4] != nil {
		t.Fatalf("null vector cell = %#v, want nil", w.rows[1][4])
	}
	if a1, _ := w.rows[1][1].([]float64); len(a1) != 2 || !math.IsNaN(a1[0]) {
		t.Fatalf("row 1 a = %v", a1)
	}
}

// TestExport_VectorExpandedSchema: a schema-aware writer that expands is
// handed the expanded schema, so per-column typing stays aligned; a list
// writer gets the source schema.
func TestExport_VectorExpandedSchema(t *testing.T) {
	t.Parallel()

	fs := writeVecExportCohort(t, vecExportSchema(), vecExportRows())
	w := &schemaCollectWriter{}
	job := NewExportJob("v.pulse", w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var names, typs []string
	for _, f := range w.schema.Fields {
		names = append(names, f.Name)
		typs = append(typs, f.Type.String())
	}
	if want := w.header; !reflect.DeepEqual(names, want) {
		t.Fatalf("schema fields %v, header %v", names, want)
	}
	if want := []string{"u8", "f64", "f64", "categorical_u8", "f32", "f32", "f32", "f64"}; !reflect.DeepEqual(typs, want) {
		t.Fatalf("schema types %v, want %v", typs, want)
	}
	if w.schema.HasVectors() || !w.schema.Fields[4].Nullable {
		t.Fatal("expanded element fields must be nullable scalars with no VECTORS")
	}
}

// TestExport_VectorColumnCollision: an expanded column whose name another
// column has is refused before any header — by Run and by a targeted
// Predict alike — while a list writer, which does not expand, exports.
func TestExport_VectorColumnCollision(t *testing.T) {
	t.Parallel()

	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "emb_1", Type: encoding.FieldTypeF64},
		{Name: "emb", Type: encoding.FieldTypeVecF64},
	}, Vectors: []encoding.VectorField{{FieldIndex: 1, Dim: 2}}}
	fs := writeVecExportCohort(t, s, [][]any{{1.0, []float64{1, 2}}})

	w := &collectWriter{}
	job := NewExportJob("v.pulse", w)
	job.FS = fs
	_, err := job.Run(context.Background())
	if !errors.HasCode(err, errors.PULSE_EXPORT_COLUMN_COLLISION) {
		t.Fatalf("err = %v, want PULSE_EXPORT_COLUMN_COLLISION", err)
	}
	if w.header != nil || len(w.rows) != 0 {
		t.Fatalf("wrote header %v / %d rows before refusing", w.header, len(w.rows))
	}
	if _, err := job.Predict(context.Background()); !errors.HasCode(err, errors.PULSE_EXPORT_COLUMN_COLLISION) {
		t.Fatalf("predict err = %v, want PULSE_EXPORT_COLUMN_COLLISION", err)
	}

	lw := &listCollectWriter{}
	job = NewExportJob("v.pulse", lw)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("list writer: %v", err)
	}
	if len(lw.rows) != 1 {
		t.Fatalf("list writer rows %d, want 1", len(lw.rows))
	}
}

// TestExportPredict_VectorColumns: a targeted predict reports the header
// the export would write for that target; no target reports none.
func TestExportPredict_VectorColumns(t *testing.T) {
	t.Parallel()

	fs := writeVecExportCohort(t, vecExportSchema(), vecExportRows())
	for _, tc := range []struct {
		name   string
		target Writer
		want   []string
	}{
		{"none", nil, nil},
		{"expanding", &collectWriter{}, []string{"id", "a_x", "a_y", "c", "b_1", "b_2", "b_3", "z"}},
		{"lists", &listCollectWriter{}, []string{"id", "a", "c", "b", "z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := NewExportJob("v.pulse", tc.target)
			job.FS = fs
			rep, err := job.Predict(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rep.Columns, tc.want) {
				t.Fatalf("columns %v, want %v", rep.Columns, tc.want)
			}
			if rep.EstimatedRows != 2 {
				t.Fatalf("estimated rows %d, want 2", rep.EstimatedRows)
			}
		})
	}

	// A vector-free cohort is predicted exactly as before: no Columns.
	plain := writeVecExportCohort(t, &encoding.Schema{Fields: []encoding.Field{{Name: "z", Type: encoding.FieldTypeF64}}},
		[][]any{{1.0}})
	job := NewExportJob("v.pulse", &collectWriter{})
	job.FS = plain
	rep, err := job.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Columns != nil {
		t.Fatalf("vector-free predict reported columns %v", rep.Columns)
	}
}
