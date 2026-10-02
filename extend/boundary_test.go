package extend_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// allowedModuleImports is the closed set of module packages extend may
// import. extend is the leaf extension-authoring contract: it must
// never reach the engine (processing) nor anything under internal/.
var allowedModuleImports = map[string]bool{
	"github.com/frankbardon/pulse/encoding": true,
	"github.com/frankbardon/pulse/types":    true,
	"github.com/frankbardon/pulse/errors":   true,
}

// TestExtendImportBoundary fails when a non-test file in extend imports
// a module package outside allowedModuleImports or any third-party
// package. Standard-library imports are always allowed.
func TestExtendImportBoundary(t *testing.T) {
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
			if !strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				continue // standard library
			}
			if !allowedModuleImports[path] {
				t.Errorf("%s imports %q; extend may import only stdlib and %v", f, path, keys(allowedModuleImports))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no extend source files found")
	}
}

// enginePackages are the module trees extend must never reach, even
// transitively through an allowed import: the operator engine moved
// under internal/processing and the orchestrator lives in
// internal/service. Reaching either would re-couple the public
// extension contract to engine internals.
var enginePackages = []string{
	"github.com/frankbardon/pulse/internal/processing",
	"github.com/frankbardon/pulse/internal/service",
}

// TestExtendImportBoundary_NoTransitiveEngine walks the full dependency
// graph of extend (`go list -deps`) and fails if any engine package is
// reachable, so an allowed import (encoding, types, errors) cannot
// smuggle the engine in.
func TestExtendImportBoundary_NoTransitiveEngine(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", ".")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps: %v\nstderr: %s", err, stderr.String())
	}
	deps := strings.Fields(stdout.String())
	if len(deps) == 0 {
		t.Fatal("go list -deps returned nothing; the boundary cannot inspect an empty graph")
	}
	for _, dep := range deps {
		for _, banned := range enginePackages {
			if dep == banned || strings.HasPrefix(dep, banned+"/") {
				t.Errorf("extend transitively depends on engine package %s", dep)
			}
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
