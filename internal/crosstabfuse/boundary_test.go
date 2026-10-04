package crosstabfuse_test

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

const modulePrefix = "github.com/frankbardon/pulse"

// TestCrosstabFuse_ImportBoundary is the import firewall for the shared
// fusion rule. Its whole reason to exist is that BOTH the engine
// (internal/processing) and the no-execute predict layer
// (internal/descriptor, whose predict files may not import the engine —
// TestPredictNoExecutionImports) call it, so it may import neither, nor
// anything that reaches them. Direct intra-module imports are pinned to
// an allow-list; the transitive graph is checked against the execution
// and descriptor packages.
func TestCrosstabFuse_ImportBoundary(t *testing.T) {
	allowedDirect := map[string]bool{
		modulePrefix + "/types":    true,
		modulePrefix + "/encoding": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, modulePrefix) && !allowedDirect[path] {
				t.Errorf("%s imports %q; internal/crosstabfuse may import only types and encoding from this module", name, path)
			}
		}
	}

	cmd := exec.Command("go", "list", "-deps", modulePrefix+"/internal/crosstabfuse")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, stderr.String())
	}
	deps := strings.Fields(out.String())
	if len(deps) == 0 {
		t.Fatal("go list -deps returned nothing — the firewall cannot inspect an empty graph")
	}
	forbidden := []string{
		modulePrefix + "/internal/processing",
		modulePrefix + "/internal/service",
		modulePrefix + "/internal/descriptor",
		modulePrefix + "/descriptor",
		modulePrefix + "/internal/io",
	}
	for _, dep := range deps {
		for _, bad := range forbidden {
			if dep == bad || strings.HasPrefix(dep, bad+"/") {
				t.Errorf("internal/crosstabfuse transitively imports %q", dep)
			}
		}
	}
}
