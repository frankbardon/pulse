package linalg_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// linalgAllowed reports whether a non-stdlib import is permitted in
// package linalg. linalg is a leaf: the standard library, gonum (its
// backend for routines with no bit contract) and Pulse's coded-error
// package, nothing else — in particular never anything under internal/,
// so the engine and synth can both depend on it without a cycle and an
// embedder importing it drags in no engine.
func linalgAllowed(path string) bool {
	return path == "github.com/frankbardon/pulse/errors" ||
		path == "gonum.org/v1/gonum" || strings.HasPrefix(path, "gonum.org/v1/gonum/")
}

// TestLinalgImportBoundary fails when a non-test file in linalg imports
// anything outside the standard library, gonum and pulse/errors.
func TestLinalgImportBoundary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(path, "/")
			if !strings.Contains(first, ".") {
				continue // standard library
			}
			if !linalgAllowed(path) {
				t.Errorf("%s imports %q; linalg may import only the standard library, "+
					"gonum.org/v1/gonum/... and github.com/frankbardon/pulse/errors", f, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no linalg source files found")
	}
}
