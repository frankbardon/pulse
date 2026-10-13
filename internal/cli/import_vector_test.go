package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/imports"
)

const vectorCSV = "id,rate_a,rate_b,rate_c,region\n1,1,2,3,n\n2,4,5,6,s\n3,,,,n\n"

// readCohortSchema reads the schema block of the cohort at path.
func readCohortSchema(t *testing.T, path string) *encoding.Schema {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(b)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestImportCLI_VectorFlags: --vector / --vector-kind reach every leaf
// that imports — `import <format>`, `import predict` (the predicted
// schema carries the vec field) and `import auto` (the managed cohort
// and its sidecar) — and a bad declaration is the coded refusal on each.
func TestImportCLI_VectorFlags(t *testing.T) {
	dir := withTempDataDir(t)
	src := filepath.Join(dir, "survey.csv")
	if err := os.WriteFile(src, []byte(vectorCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--vector", "r:/^rate_(.)$/", "--vector-kind", "r:scale:1:7"}

	out := filepath.Join(dir, "out.pulse")
	if err := runImportCLI(t, append([]string{"csv", "-i", src, "-o", out}, flags...)...); err != nil {
		t.Fatal(err)
	}
	s := readCohortSchema(t, out)
	v, ok := s.Vector(1)
	if !ok || s.Fields[1].Name != "r" || v.Dim != 3 || v.Labels[2] != "c" || v.Kind != encoding.VectorKindScale {
		t.Fatalf("import csv: %+v / %+v", s.Fields, v)
	}

	var buf bytes.Buffer
	if err := runImportCSV(t, &buf, append([]string{"predict", "-i", src, "--json"}, flags...)...); err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data struct {
			Schema struct {
				Fields []struct {
					Name string `json:"Name"`
					Type int    `json:"Type"`
				} `json:"Fields"`
			} `json:"Schema"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if len(env.Data.Schema.Fields) != 3 || env.Data.Schema.Fields[1].Name != "r" ||
		encoding.FieldType(env.Data.Schema.Fields[1].Type) != encoding.FieldTypeVecF64 {
		t.Fatalf("import predict schema: %s", buf.String())
	}

	if err := runImportCLI(t, append([]string{"auto"}, append(flags, "survey.csv")...)...); err != nil {
		t.Fatal(err)
	}
	if s := readCohortSchema(t, filepath.Join(dir, "imports", "survey.pulse")); !s.HasVectors() {
		t.Fatal("import auto: managed cohort has no vector")
	}
	side, err := os.ReadFile(filepath.Join(dir, "imports", "survey.pulse"+imports.SidecarSuffix))
	if err != nil {
		t.Fatal(err)
	}
	var sc imports.Sidecar
	if err := json.Unmarshal(side, &sc); err != nil {
		t.Fatal(err)
	}
	if len(sc.Vectors) != 1 || sc.Vectors[0].Pattern != "^rate_(.)$" || sc.Vectors[0].Kind != "scale" {
		t.Fatalf("sidecar vectors = %+v", sc.Vectors)
	}

	for _, leaf := range [][]string{
		{"csv", "-i", src, "-o", filepath.Join(dir, "bad.pulse"), "--json"},
		{"predict", "-i", src, "--json"},
		{"auto", "--json", "--handle", "bad", "survey.csv"},
	} {
		buf.Reset()
		args := append(append([]string(nil), leaf[:len(leaf)-1]...), "--vector", "r:/^score_/", leaf[len(leaf)-1])
		if leaf[0] != "auto" {
			args = append(append([]string(nil), leaf...), "--vector", "r:/^score_/")
		}
		root := ImportCommand()
		root.Writer = &buf
		if err := root.Run(context.Background(), append([]string{"import"}, args...)); err != nil {
			t.Fatalf("%v: transport error %v", leaf, err)
		}
		if got := envelopeErrorCode(t, &buf); got != string(perrors.PULSE_IMPORT_VECTOR_INVALID) {
			t.Fatalf("%v: errors[0].code = %q\n%s", leaf, got, buf.String())
		}
	}
}
