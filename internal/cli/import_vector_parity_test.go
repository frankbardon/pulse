package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// The CLI legs of the U29 import acceptance. They live here, not in the
// root package's vector_import_*_e2e_test.go: a root-package test that
// imports internal/cli drags urfave/cli into the test graph of every
// module importing pulse, and the contrib modules' `go mod tidy -diff`
// then fails (see TestRootTestGraphExcludesCLI). The root tests keep the
// library path and the analysis parity; these pin CLI ≡ library.

// vectorParityWideCSV is the wide fixture of TestVectorImport_EndToEnd:
// rate_a..rate_e on a 1..7 scale, with all-empty (null vector) rows and
// rows with empty (NaN) elements.
func vectorParityWideCSV() string {
	var csv strings.Builder
	csv.WriteString("id,rate_a,rate_b,rate_c,rate_d,rate_e,region\n")
	for i := 0; i < 60; i++ {
		cells := make([]string, 5)
		for k := range cells {
			cells[k] = fmt.Sprint(1 + (i*(k+2)+i/(k+2)+k*k)%7)
		}
		switch {
		case i%17 == 5:
			cells = []string{"", "", "", "", ""}
		case i%11 == 3:
			cells[1], cells[3] = "", ""
		}
		fmt.Fprintf(&csv, "%d,%s,%s\n", i+1, strings.Join(cells, ","), []string{"north", "south", "east"}[i%3])
	}
	return csv.String()
}

// vectorParityListNDJSON is the list-column fixture of
// TestVectorImport_ListColumnEndToEnd: an array column v of dim 3 with
// null lists and null elements.
func vectorParityListNDJSON() string {
	var nd strings.Builder
	for i := 0; i < 40; i++ {
		el := []string{fmt.Sprint(1 + i%7), fmt.Sprint((i*3)%5) + ".5", fmt.Sprint(10 - i%4)}
		switch {
		case i%9 == 4:
			el = nil
		case i%7 == 2:
			el[1] = "null"
		}
		list := "null"
		if el != nil {
			list = "[" + strings.Join(el, ",") + "]"
		}
		fmt.Fprintf(&nd, `{"id":%d,"v":%s}`+"\n", i+1, list)
	}
	return nd.String()
}

// TestImportCLI_VectorMatchesLibrary: the same vector declaration given
// to the library (pio.VectorDecl on an ImportJob) and to the CLI (its
// --vector / --vector-kind flag syntax) writes byte-identical cohorts —
// a regex fold of sibling columns with a scale kind, and a bare
// --vector over an NDJSON list column.
func TestImportCLI_VectorMatchesLibrary(t *testing.T) {
	t.Parallel()

	min, max := 1.0, 7.0
	cases := []struct {
		name    string
		file    string
		body    string
		format  pio.Format
		decl    pio.VectorDecl
		cliArgs []string
	}{
		{
			name: "regex_fold_scale", file: "survey.csv", body: vectorParityWideCSV(), format: pio.FormatCSV,
			decl:    pio.VectorDecl{Name: "brand_rating", Pattern: "^rate_(.*)$", Kind: "scale", ScaleMin: &min, ScaleMax: &max},
			cliArgs: []string{"csv", "--vector", "brand_rating:/^rate_(.*)$/", "--vector-kind", "brand_rating:scale:1:7"},
		},
		{
			name: "list_column", file: "lists.ndjson", body: vectorParityListNDJSON(), format: pio.FormatNDJSON,
			decl:    pio.VectorDecl{Name: "v"},
			cliArgs: []string{"ndjson", "--vector", "v"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, tc.file)
			if err := os.WriteFile(src, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()

			p, err := pulse.New(pulse.Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			r, err := pio.NewReader(tc.format, afero.NewOsFs(), src, pio.ReaderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			libPath := filepath.Join(dir, "lib.pulse")
			job := pio.NewImportJob(r, libPath)
			job.FS = afero.NewOsFs()
			job.Vectors = []pio.VectorDecl{tc.decl}
			if _, err := p.Import(ctx, job); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			cliPath := filepath.Join(dir, "cli.pulse")
			root := ImportCommand()
			root.Writer = &out
			args := append([]string{"import", tc.cliArgs[0], "-i", src, "-o", cliPath}, tc.cliArgs[1:]...)
			if err := root.Run(ctx, append(args, "--json")); err != nil {
				t.Fatalf("cli: %v\n%s", err, out.String())
			}

			libBytes, _ := os.ReadFile(libPath)
			cliBytes, _ := os.ReadFile(cliPath)
			if len(libBytes) == 0 || !bytes.Equal(libBytes, cliBytes) {
				t.Fatalf("CLI cohort (%d bytes) differs from the library's (%d bytes)\n%s", len(cliBytes), len(libBytes), out.String())
			}
			if s := readCohortSchema(t, cliPath); !s.HasVectors() {
				t.Fatalf("CLI cohort carries no vector field: %+v", s.Fields)
			}
		})
	}
}
