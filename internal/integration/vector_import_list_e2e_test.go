package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// TestVectorImport_ListColumnEndToEnd is the list-source arm of the U29
// import acceptance: an NDJSON array column imported with a bare
// `--vector v` holds one vec_f64 field of dim 3, and AGG_VEC_MEAN over
// it answers exactly what it answers over the same data imported as
// sibling columns folded with `--vector v:v_a,v_b,v_c` (null lists and
// null elements included). The CLI ≡ library byte identity is internal/cli's
// TestImportCLI_VectorMatchesLibrary.
func TestVectorImport_ListColumnEndToEnd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var nd, csv strings.Builder
	csv.WriteString("id,v_a,v_b,v_c\n")
	for i := 0; i < 40; i++ {
		el := []string{fmt.Sprint(1 + i%7), fmt.Sprint((i*3)%5) + ".5", fmt.Sprint(10 - i%4)}
		cells := append([]string(nil), el...)
		switch {
		case i%9 == 4: // a null list / an all-empty row
			el, cells = nil, []string{"", "", ""}
		case i%7 == 2: // a null element
			el[1], cells[1] = "null", ""
		}
		list := "null"
		if el != nil {
			list = "[" + strings.Join(el, ",") + "]"
		}
		fmt.Fprintf(&nd, `{"id":%d,"v":%s}`+"\n", i+1, list)
		fmt.Fprintf(&csv, "%d,%s\n", i+1, strings.Join(cells, ","))
	}
	ndPath, csvPath := filepath.Join(dir, "lists.ndjson"), filepath.Join(dir, "wide.csv")
	if err := os.WriteFile(ndPath, []byte(nd.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(csvPath, []byte(csv.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	importLib := func(format pio.Format, src, target string, d pio.VectorDecl) {
		t.Helper()
		r, err := pio.NewReader(format, afero.NewOsFs(), src, pio.ReaderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		job := pio.NewImportJob(r, filepath.Join(dir, target))
		job.FS = afero.NewOsFs()
		job.Vectors = []pio.VectorDecl{d}
		if _, err := p.Import(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	importLib(pio.FormatNDJSON, ndPath, "list.pulse", pio.VectorDecl{Name: "v"})
	importLib(pio.FormatCSV, csvPath, "wide.pulse", pio.VectorDecl{Name: "v", Columns: []string{"v_a", "v_b", "v_c"}})

	libBytes, err := os.ReadFile(filepath.Join(dir, "list.pulse"))
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
	if v, ok := schema.Vector(1); !ok || schema.Fields[1].Type != encoding.FieldTypeVecF64 || v.Dim != 3 || v.Labels != nil {
		t.Fatalf("list cohort schema %+v / %+v", schema.Fields, v)
	}

	run := func(cohort string) []byte {
		t.Helper()
		var req pulse.Request
		body := `{"cohort":{"filename":"` + cohort + `"},"aggregations":[{"type":"AGG_VEC_MEAN","vector":"v"}]}`
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		resp, err := p.Process(ctx, &req)
		if err != nil {
			t.Fatalf("%s: %v", cohort, err)
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
		j, _ := json.Marshal(m)
		return j
	}
	list, wide := run("list.pulse"), run("wide.pulse")
	if !bytes.Contains(list, []byte("AGG_VEC_MEAN")) && !bytes.Contains(list, []byte(`"data"`)) {
		t.Fatalf("list response carries no figures: %s", list)
	}
	// The sibling fold labels its elements with the column names; the
	// list carries none, so its elements are keyed by position.
	for k, l := range []string{"v_a", "v_b", "v_c"} {
		wide = bytes.ReplaceAll(wide, []byte(`"`+l+`"`), []byte(fmt.Sprintf(`"v_%d"`, k+1)))
	}
	if !bytes.Equal(list, wide) {
		t.Fatalf("list-column and sibling-column responses differ:\nlist: %s\nwide: %s", list, wide)
	}
}
