package statdist

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestStatdistImportBoundary keeps statdist a LEAF: its non-test files
// may import only the standard library and gonum. Anything wider (the
// engine, types, errors) would put processing and regression one import
// away from a cycle, which is the reason this package exists.
func TestStatdistImportBoundary(t *testing.T) {
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
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(path, "/")
			if !strings.Contains(first, ".") || strings.HasPrefix(path, "gonum.org/v1/gonum/") {
				continue
			}
			t.Errorf("%s imports %q: statdist may import only the standard library and gonum", name, path)
		}
	}
	if seen == 0 {
		t.Fatal("no non-test Go files found")
	}
}
