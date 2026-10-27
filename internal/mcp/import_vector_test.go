package mcp

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// TestPulseImport_Vectors: `vectors` / `vector_kinds` (the CLI's own
// --vector / --vector-kind strings) fold the columns into a native
// vector; the schema slot takes the vector keys; a bad declaration, a
// kind for an undeclared vector and a vec schema entry without dim are
// each refused with their own code.
func TestPulseImport_Vectors(t *testing.T) {
	t.Parallel()

	p, afs := newSchemaImportPulse(t)
	if err := afero.WriteFile(afs, "wide.csv", []byte("id,q_a,q_b,q_c\n1,1,2,3\n2,4,,6\n3,,,\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := invokeImportOut(t, p, `{"source":"wide.csv","handle":"v","vectors":["q:/^q_(.)$/"],"vector_kinds":["q:scale:1:7"]}`)
	if err != nil {
		t.Fatal(err)
	}
	s, rows := readCohort(t, p, out.Path)
	v, ok := s.Vector(1)
	if !ok || s.Fields[1].Type != encoding.FieldTypeVecF64 || v.Dim != 3 || fmt.Sprint(v.Labels) != "[a b c]" ||
		v.Kind != encoding.VectorKindScale || v.ScaleMax != 7 {
		t.Fatalf("schema %+v / %+v", s.Fields, v)
	}
	if rows[0][1] != "[1 2 3]" || rows[1][1] != "[4 NaN 6]" || rows[2][1] != "<nil>" {
		t.Fatalf("rows = %v", rows)
	}

	out, err = invokeImportOut(t, p, `{"source":"wide.csv","handle":"s","vectors":["q:q_a,q_b,q_c"],
		"schema":[{"name":"id","type":"u8"},{"name":"q","type":"vec_f32","dim":3,"nullable":true,"labels":["x","y","z"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	s, _ = readCohort(t, p, out.Path)
	if v, _ := s.Vector(1); s.Fields[1].Type != encoding.FieldTypeVecF32 || fmt.Sprint(v.Labels) != "[x y z]" {
		t.Fatalf("schema slot: %+v / %+v", s.Fields, v)
	}

	for _, c := range []struct {
		args string
		code perr.Code
	}{
		{`{"source":"wide.csv","handle":"b1","vectors":["q:/^z/"]}`, perr.PULSE_IMPORT_VECTOR_INVALID},
		{`{"source":"wide.csv","handle":"b2","vectors":["q:q_a,q_b"],"vector_kinds":["w:scale"]}`, perr.PULSE_IMPORT_VECTOR_INVALID},
		{`{"source":"wide.csv","handle":"b3","vectors":["q:q_a,q_b,q_c"],"schema":[{"name":"id","type":"u8"},{"name":"q","type":"vec_f64"}]}`, perr.PULSE_IMPORT_SCHEMA_INVALID},
	} {
		if _, err := invokeImportOut(t, p, c.args); !perr.HasCode(err, c.code) {
			t.Fatalf("%s: err = %v, want %s", c.args, err, c.code)
		}
	}
}
