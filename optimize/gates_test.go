package optimize_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// optimizeAllowed reports whether a non-stdlib import is permitted in
// package optimize: Pulse's coded errors, the public linear-algebra core
// and the deterministic math kernels — never the engine, so an embedder
// importing optimize drags in nothing else.
func optimizeAllowed(path string) bool {
	switch path {
	case "github.com/frankbardon/pulse/errors",
		"github.com/frankbardon/pulse/linalg",
		"github.com/frankbardon/pulse/linalg/detmath":
		return true
	}
	return false
}

// TestOptimize_ImportBoundary fails when a non-test file in optimize
// imports anything outside the standard library and the allowlist.
func TestOptimize_ImportBoundary(t *testing.T) {
	t.Parallel()

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
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if first, _, _ := strings.Cut(path, "/"); !strings.Contains(first, ".") {
				continue
			}
			if !optimizeAllowed(path) {
				t.Errorf("%s imports %q; optimize may import only the standard library, "+
					"pulse/errors, pulse/linalg and pulse/linalg/detmath", f, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no optimize source files found")
	}
}
