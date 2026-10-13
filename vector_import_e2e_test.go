package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// TestVectorImport_EndToEnd is the U29 import acceptance: a wide CSV
// (rate_a..rate_e) imported with `--vector brand_rating:/^rate_(.*)$/
// --vector-kind brand_rating:scale:1:7` holds ONE vec_f64 field of dim 5,
// labels a..e, kind scale 1..7; and a request naming that field in
// AGG_VEC_MEAN and MAT_CORRELATION answers exactly what the
// virtual-vector request over the raw columns of a plain import does
// (a null vector row and NaN elements included). The CLI ≡ library
// byte identity of the same import is internal/cli's
// TestImportCLI_VectorMatchesLibrary: a root test must not import
// internal/cli (TestRootTestGraphExcludesCLI).
func TestVectorImport_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	var csv strings.Builder
	csv.WriteString("id,rate_a,rate_b,rate_c,rate_d,rate_e,region\n")
	for i := 0; i < 60; i++ {
		cells := make([]string, 5)
		for k := range cells {
			cells[k] = fmt.Sprint(1 + (i*(k+2)+i/(k+2)+k*k)%7)
		}
		switch {
		case i%17 == 5: // a null vector
			cells = []string{"", "", "", "", ""}
		case i%11 == 3: // NaN elements
			cells[1], cells[3] = "", ""
		}
		fmt.Fprintf(&csv, "%d,%s,%s\n", i+1, strings.Join(cells, ","), []string{"north", "south", "east"}[i%3])
	}
	src := filepath.Join(dir, "survey.csv")
	if err := os.WriteFile(src, []byte(csv.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	importLib := func(target string, vectors bool) {
		t.Helper()
		r, err := pio.NewReader(pio.FormatCSV, afero.NewOsFs(), src, pio.ReaderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		job := pio.NewImportJob(r, filepath.Join(dir, target))
		job.FS = afero.NewOsFs()
		if vectors {
			min, max := 1.0, 7.0
			job.Vectors = []pio.VectorDecl{{Name: "brand_rating", Pattern: "^rate_(.*)$", Kind: "scale", ScaleMin: &min, ScaleMax: &max}}
		}
		if _, err := p.Import(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	importLib("native.pulse", true)
	importLib("raw.pulse", false)

	libBytes, err := os.ReadFile(filepath.Join(dir, "native.pulse"))
	if err != nil {
		t.Fatal(err)
	}

	r := bytes.NewReader(libBytes)
	ver, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(r, ver)
	if err != nil {
		t.Fatal(err)
	}
	idx := -1
	for i, f := range schema.Fields {
		if strings.HasPrefix(f.Name, "rate_") {
			t.Fatalf("member column %q survived the fold", f.Name)
		}
		if f.Name == "brand_rating" {
			idx = i
		}
	}
	if idx != 1 || schema.Fields[idx].Type != encoding.FieldTypeVecF64 || ver != encoding.FormatVersionV2 {
		t.Fatalf("brand_rating at %d, version %d: %+v", idx, ver, schema.Fields)
	}
	v, _ := schema.Vector(idx)
	if v.Dim != 5 || !reflect.DeepEqual(v.Labels, []string{"a", "b", "c", "d", "e"}) ||
		v.Kind != encoding.VectorKindScale || !v.HasScale || v.ScaleMin != 1 || v.ScaleMax != 7 {
		t.Fatalf("vector metadata %+v", v)
	}

	run := func(body string) map[string]any {
		t.Helper()
		var req pulse.Request
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		resp, err := p.Process(ctx, &req)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "metadata")
		return m
	}
	slots := `"aggregations":[{"type":"AGG_VEC_MEAN","vector":"brand_rating"}],` +
		`"matrices":[{"type":"MAT_CORRELATION","vector":"brand_rating"}]`
	native := run(`{"cohort":{"filename":"native.pulse"},` + slots + `}`)
	virtual := run(`{"cohort":{"filename":"raw.pulse"},"vectors":[{"name":"brand_rating",` +
		`"fields":["rate_a","rate_b","rate_c","rate_d","rate_e"],"labels":["a","b","c","d","e"],` +
		`"kind":"scale","scale_min":1,"scale_max":7}],` + slots + `}`)
	if native["data"] == nil || native["matrices"] == nil {
		t.Fatalf("native response is missing its figures: %v", native)
	}
	nj, _ := json.Marshal(native)
	vj, _ := json.Marshal(virtual)
	// A native vector's members are keyed by element ("brand_rating[1]"),
	// a virtual one's by its source columns; every figure must agree.
	for k, l := range []string{"a", "b", "c", "d", "e"} {
		vj = bytes.ReplaceAll(vj, []byte(`"rate_`+l+`"`), []byte(fmt.Sprintf(`"brand_rating[%d]"`, k+1)))
	}
	if bytes.Contains(nj, []byte("null]")) || bytes.Contains(nj, []byte("ZERO_VARIANCE")) {
		t.Fatalf("fixture degenerate (an undefined correlation): %s", nj)
	}
	if !bytes.Equal(nj, vj) {
		t.Fatalf("native and virtual-vector responses differ:\nnative:  %s\nvirtual: %s", nj, vj)
	}
}
