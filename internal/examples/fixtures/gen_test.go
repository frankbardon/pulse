package fixtures

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestFixtures_RegenerateByteIdentical pins every generated fixture CSV
// to its generator: Generate runs in memory and each file must equal
// the committed bytes. The generator is portable by construction
// (PCG streams, detmath variates, barriered multiply-adds), so on
// amd64 CI this proves the CSVs committed from any arch are what that
// arch computes too. On a mismatch, regenerate with
// go run ./internal/examples/fixtures/cmd/genfixtures and regenerate
// every golden downstream (docs/src/internals/regenerating-goldens.md).
func TestFixtures_RegenerateByteIdentical(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(BrandSpecPath)))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("Generate returned no files")
	}
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.Name] {
			t.Errorf("%s generated twice", f.Name)
		}
		seen[f.Name] = true
		committed, err := os.ReadFile(f.Name)
		if err != nil {
			t.Errorf("%s: %v (regenerate: go run ./internal/examples/fixtures/cmd/genfixtures)", f.Name, err)
			continue
		}
		if !bytes.Equal(committed, f.Data) {
			t.Errorf("%s: committed bytes differ from the generator at byte %d (regenerate: go run ./internal/examples/fixtures/cmd/genfixtures)",
				f.Name, firstDiff(committed, f.Data))
		}
	}
}

// TestFixtures_GenerateDeterministic: two runs in one process agree,
// so the pin above cannot pass by an accident of shared state.
func TestFixtures_GenerateDeterministic(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(BrandSpecPath)))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Generate(spec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("runs returned %d and %d files", len(a), len(b))
	}
	for i := range a {
		if a[i].Name != b[i].Name || !bytes.Equal(a[i].Data, b[i].Data) {
			t.Errorf("%s: two runs differ", a[i].Name)
		}
	}
}

// TestFixtures_GenerateRejectsBadBrandSpec: a malformed spec is an
// error, never a silently empty brand_survey.csv.
func TestFixtures_GenerateRejectsBadBrandSpec(t *testing.T) {
	t.Parallel()

	if _, err := Generate([]byte("{")); err == nil {
		t.Fatal("Generate accepted a malformed brand spec")
	}
}

func firstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
