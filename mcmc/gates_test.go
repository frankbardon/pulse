package mcmc_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// mcmcAllowed reports whether a non-stdlib import is permitted in
// package mcmc: Pulse's coded errors, the public linear-algebra core and
// the deterministic math kernels — never the engine (internal/processing,
// internal/service) nor any model package, so an embedder importing mcmc
// drags in nothing else.
func mcmcAllowed(path string) bool {
	switch path {
	case "github.com/frankbardon/pulse/errors",
		"github.com/frankbardon/pulse/linalg",
		"github.com/frankbardon/pulse/linalg/detmath":
		return true
	}
	return false
}

// TestMCMC_ImportBoundary fails when a non-test file in mcmc imports
// anything outside the standard library and the allowlist.
func TestMCMC_ImportBoundary(t *testing.T) {
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
			if !mcmcAllowed(path) {
				t.Errorf("%s imports %q; mcmc may import only the standard library, "+
					"pulse/errors, pulse/linalg and pulse/linalg/detmath", f, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no mcmc source files found")
	}
}
