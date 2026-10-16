package rake_test

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

// TestRake_ImportBoundary is the import firewall for the rake solver: it
// is a leaf the facade runs and the no-execute layer may reuse (its
// Compile refusals need only the schema), so it may import neither the
// engine nor anything that reaches it. Direct intra-module imports are
// pinned to encoding / types / errors and internal/weighting; the
// transitive graph is checked against the execution and descriptor
// packages.
func TestRake_ImportBoundary(t *testing.T) {
	allowedDirect := map[string]bool{
		modulePrefix + "/types":    true,
		modulePrefix + "/errors":   true,
		modulePrefix + "/encoding": true,
		// The weight-validity rule and Kish n_eff (a stdlib + types leaf).
		modulePrefix + "/internal/weighting": true,
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
			if strings.Contains(path, ".") && !allowedDirect[path] {
				t.Errorf("%s imports %q; internal/rake may import only the standard library, encoding, types, errors and internal/weighting", name, path)
			}
		}
	}

	cmd := exec.Command("go", "list", "-deps", modulePrefix+"/internal/rake")
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
				t.Errorf("internal/rake transitively imports %q", dep)
			}
		}
	}
}
