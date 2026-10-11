package metric_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// metricAllowed reports whether a non-stdlib import is permitted in
// package metric: Pulse's coded-error package and the parent linalg
// (for the Sym inverse covariance), never anything under internal/ and
// never gonum directly — an embedder importing the registry drags in no
// engine.
func metricAllowed(path string) bool {
	return path == "github.com/frankbardon/pulse/errors" || path == "github.com/frankbardon/pulse/linalg"
}

// TestMetricImportBoundary fails when a non-test file in linalg/metric
// imports anything outside the standard library, pulse/errors and
// pulse/linalg.
func TestMetricImportBoundary(t *testing.T) {
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
				continue
			}
			if !metricAllowed(path) {
				t.Errorf("%s imports %q; linalg/metric may import only the standard library, pulse/errors and pulse/linalg", f, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no linalg/metric source files found")
	}
}
