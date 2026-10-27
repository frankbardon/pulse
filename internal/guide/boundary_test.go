package guide

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/frankbardon/pulse"

// executionPackages are the packages a no-execute package must never
// reach: the orchestration and engine layers.
var executionPackages = []string{
	modulePath + "/internal/service",
	modulePath + "/internal/processing",
}

func isExecutionImport(p string) bool {
	for _, banned := range executionPackages {
		if p == banned || strings.HasPrefix(p, banned+"/") {
			return true
		}
	}
	return false
}

// isEngineRoot is the transitive ban: the service package (any
// subpackage) or the engine root package itself.
func isEngineRoot(p string) bool {
	return p == executionPackages[1] || p == executionPackages[0] || strings.HasPrefix(p, executionPackages[0]+"/")
}

// bannedImportsIn returns the execution imports of one Go source.
func bannedImportsIn(name string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if isExecutionImport(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// TestGuideNoExecutionImports_Falsifier: the checker the gate runs
// flags a direct engine or service import (subpackages included).
func TestGuideNoExecutionImports_Falsifier(t *testing.T) {
	t.Parallel()

	for _, imp := range []string{"internal/service", "internal/processing", "internal/processing/regression"} {
		src := "package guide\nimport _ \"" + modulePath + "/" + imp + "\"\n"
		bad, err := bannedImportsIn("x.go", src)
		if err != nil {
			t.Fatal(err)
		}
		if len(bad) != 1 {
			t.Errorf("import of %s not flagged", imp)
		}
	}
	ok, _ := bannedImportsIn("x.go", "package guide\nimport _ \""+modulePath+"/internal/descriptor\"\n")
	if len(ok) != 0 {
		t.Errorf("internal/descriptor flagged: %v", ok)
	}
}
