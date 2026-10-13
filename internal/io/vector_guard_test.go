package io

import (
	"bytes"
	"context"
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

// TestCohortBuilder_VectorFieldRefused: the builder has no vector cell
// path yet, so a vec field is refused at construction, not mid-build.
func TestCohortBuilder_VectorFieldRefused(t *testing.T) {
	_, _, err := prepareBuildSchema(vecGuardSchema(), false)
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("err = %v, want SERVICE_VALIDATION", err)
	}
}
