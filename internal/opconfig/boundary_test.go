package opconfig_test

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

// TestOpConfig_ImportBoundary is the import firewall for the operator
// config rules (#781). Predict (internal/descriptor, no-execute —
// TestPredictNoExecutionImports) and the engine (internal/processing)
// both import it, so it may reach neither the engine nor the
// descriptor packages. Direct intra-module imports are pinned to the
// no-execute leaves it reads.
func TestOpConfig_ImportBoundary(t *testing.T) {
	t.Parallel()

	allowedDirect := map[string]bool{
		modulePrefix + "/types":              true,
		modulePrefix + "/errors":             true,
		modulePrefix + "/encoding":           true,
		modulePrefix + "/internal/paramread": true,
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
				t.Errorf("%s imports %q; internal/opconfig may import only types, errors, encoding and paramread", name, path)
			}
		}
	}
	cmd := exec.Command("go", "list", "-deps", modulePrefix+"/internal/opconfig")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, stderr.String())
	}
	deps := strings.Fields(out.String())
	if len(deps) == 0 {
		t.Fatal("go list -deps returned nothing — the firewall cannot inspect an empty graph")
	}
	for _, dep := range deps {
		for _, bad := range []string{"/internal/processing", "/internal/service", "/internal/descriptor", "/descriptor", "/internal/io"} {
			if dep == modulePrefix+bad || strings.HasPrefix(dep, modulePrefix+bad+"/") {
				t.Errorf("internal/opconfig transitively imports %q", dep)
			}
		}
	}
}
