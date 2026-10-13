package io

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

func vecGuardSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8},
		{Name: "v", Type: encoding.FieldTypeVecF64},
	}, Vectors: []encoding.VectorField{{FieldIndex: 1, Dim: 2}}}
}

// TestExport_VectorFieldRefused: export has no column mapping for a
// vector yet; its read would end the loop like a truncated tail and
// export zero rows with no error, so it is refused up front.
func TestExport_VectorFieldRefused(t *testing.T) {
	s := vecGuardSchema()
	var b bytes.Buffer
	if err := encx.WritePreamble(&b, s); err != nil {
		t.Fatal(err)
	}
	b.WriteByte(1)
	if err := encoding.WriteVector(&b, encoding.FieldTypeVecF64, 2, []float64{1, 2}); err != nil {
		t.Fatal(err)
	}
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "vec.pulse", b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &collectWriter{}
	job := NewExportJob("vec.pulse", w)
	job.FS = fs
	_, err := job.Run(context.Background())
	if !errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) {
		t.Fatalf("err = %v, want ENCODING_TYPE_MISMATCH", err)
	}
	if len(w.rows) != 0 {
		t.Fatalf("exported %d rows before refusing", len(w.rows))
	}
}

// TestCohortBuilder_VectorSchema: a vec field needs its dim — one with
// no VECTORS entry is refused at construction (its row width would be
// unknown) — and the prepared schema keeps the vector metadata, so its
// stride sizes the field at element × dim.
func TestCohortBuilder_VectorSchema(t *testing.T) {
	bare := vecGuardSchema()
	bare.Vectors = nil
	if _, _, err := prepareBuildSchema(bare, false); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("no dim: err = %v, want SERVICE_VALIDATION", err)
	}
	out, _, err := prepareBuildSchema(vecGuardSchema(), false)
	if err != nil {
		t.Fatal(err)
	}
	if out.VectorDim(1) != 2 || out.FieldWidth(1) != 16 || out.RecordByteSize() != 17 {
		t.Fatalf("prepared schema: dim %d width %d stride %d, want 2 / 16 / 17",
			out.VectorDim(1), out.FieldWidth(1), out.RecordByteSize())
	}
}

// TestCohortBuilder_VectorCells: a vector cell is the native slice of
// exactly the field's dim; any other shape is a PULSE_IMPORT_ROW_ERROR
// that leaves no trace, and the accepted rows round-trip.
func TestCohortBuilder_VectorCells(t *testing.T) {
	fs := afero.NewMemMapFs()
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeVecF32, Nullable: true},
		{Name: "b", Type: encoding.FieldTypeVecF64},
	}, Vectors: []encoding.VectorField{{FieldIndex: 0, Dim: 2}, {FieldIndex: 1, Dim: 3}}}
	b, err := NewCohortBuild(context.Background(), fs, "v.pulse", s, CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bad := [][]any{
		{[]float64{1, 2}, []float64{1, 2, 3}},            // vec_f32 wants []float32
		{[]float32{1, 2}, []float32{1, 2, 3}},            // vec_f64 wants []float64
		{[]float32{1, 2, 3}, []float64{1, 2, 3}},         // wrong dim
		{[]float32{1, 2}, []float64(nil)},                // a nil slice is not a null
		{[]float32{1, 2}, nil},                           // non-nullable null
		{"1|2", []float64{1, 2, 3}},                      // no coercion from text
		{[]float32{1, 2}, []float64{1, 2, 3, 4}},         // wrong dim
		{[]float32{1, 2}, []float64{1, 2, 3}, uint64(1)}, // arity
	}
	for k, row := range bad {
		if err := b.Append(row); !errors.HasCode(err, errors.PULSE_IMPORT_ROW_ERROR) {
			t.Fatalf("bad row %d: err = %v, want PULSE_IMPORT_ROW_ERROR", k, err)
		}
	}
	nan := math.NaN()
	good := [][]any{
		{[]float32{1.5, -2}, []float64{nan, math.Inf(1), -0.25}},
		{nil, []float64{0, 0, 0}},
	}
	for k, row := range good {
		if err := b.Append(row); err != nil {
			t.Fatalf("good row %d: %v", k, err)
		}
	}
	rep, err := b.Close()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Records != 2 {
		t.Fatalf("records = %d, want 2", rep.Records)
	}
	raw, err := afero.ReadFile(fs, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(raw)
	got, _, err := encx.ReadPreamble(r)
	if err != nil {
		t.Fatal(err)
	}
	rr := encx.NewRecordReader(r, got)
	var rows [][2][]float64
	var nullA []bool
	for {
		vals, nulls, wide := map[string]float64{}, map[string]bool{}, map[string]any{}
		if err := rr.ReadRecordWithWide(vals, nulls, wide); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		a, _ := wide["a"].([]float64)
		bv, _ := wide["b"].([]float64)
		rows = append(rows, [2][]float64{a, bv})
		nullA = append(nullA, nulls["a"])
	}
	if len(rows) != 2 {
		t.Fatalf("read %d rows, want 2", len(rows))
	}
	if fmt.Sprint(rows[0][0]) != "[1.5 -2]" || fmt.Sprint(rows[0][1]) != "[NaN +Inf -0.25]" || nullA[0] {
		t.Errorf("row 0 = %v / %v (null a %v)", rows[0][0], rows[0][1], nullA[0])
	}
	if rows[1][0] != nil || !nullA[1] || fmt.Sprint(rows[1][1]) != "[0 0 0]" {
		t.Errorf("row 1 = %v (null a %v) / %v, want null a and zeros", rows[1][0], nullA[1], rows[1][1])
	}
}
