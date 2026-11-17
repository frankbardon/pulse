package gp

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestGP_ImportBoundary keeps internal/gp a numerics leaf: besides the
// standard library it imports only linalg, linalg/detmath, optimize and
// internal/regresample (the seeded-PCG stream constant), so the sweep
// rules (TestSweep_ImportBoundary) can take it on without reaching the
// engine or the descriptor.
func TestGP_ImportBoundary(t *testing.T) {
	t.Parallel()
	const mod = "github.com/frankbardon/pulse/"
	allowed := map[string]bool{
		mod + "linalg":               true,
		mod + "linalg/detmath":       true,
		mod + "optimize":             true,
		mod + "internal/regresample": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen++
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(path, ".") && !allowed[path] {
				t.Errorf("%s imports %q; internal/gp may import only the standard library, linalg, linalg/detmath, optimize and internal/regresample", name, path)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no production files scanned")
	}
}
