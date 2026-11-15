package postfit

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestPostFit_ImportBoundary: the package stays a no-execute leaf, so
// internal/descriptor can run Check at predict: it imports only the
// public nouns, extend and the no-execute model / result readers —
// never the engine (internal/processing, internal/service).
func TestPostFit_ImportBoundary(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{
		"github.com/frankbardon/pulse/types":               true,
		"github.com/frankbardon/pulse/errors":              true,
		"github.com/frankbardon/pulse/descriptor":          true,
		"github.com/frankbardon/pulse/extend":              true,
		"github.com/frankbardon/pulse/internal/model":      true,
		"github.com/frankbardon/pulse/internal/resultcall": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, "github.com/frankbardon/pulse") && !allowed[path] {
				t.Errorf("%s imports %q", e.Name(), path)
			}
		}
	}
}
