package extend_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// engineOnlyNames are engine capabilities extend deliberately does NOT
// expose: Meta* is synthesised by the root adapter from a
// registration's ComponentsFunc, Mergeable* / StreamableGrouper /
// IncludeOrdered are engine fast paths extensions never take, and
// ExtensionAware would hand an extension the whole engine registry.
var engineOnlyNames = []string{"StreamableGrouper", "IncludeOrdered", "ExtensionAware"}

var engineOnlyPrefixes = []string{"Mergeable", "Meta"}

// TestExtendOmitsEngineOnlyCapabilities fails when a non-test file in
// extend declares an exported identifier naming an engine-only
// capability.
func TestExtendOmitsEngineOnlyCapabilities(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	declared := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			for _, name := range exportedNames(d) {
				declared++
				for _, bad := range engineOnlyNames {
					if name == bad {
						t.Errorf("%s declares engine-only %s", f, name)
					}
				}
				for _, p := range engineOnlyPrefixes {
					if strings.HasPrefix(name, p) {
						t.Errorf("%s declares engine-only %s (prefix %s)", f, name, p)
					}
				}
			}
		}
	}
	if declared == 0 {
		t.Fatal("no exported extend declarations found")
	}
}

func exportedNames(d ast.Decl) []string {
	var out []string
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil && d.Name.IsExported() {
			out = append(out, d.Name.Name)
		}
	case *ast.GenDecl:
		for _, s := range d.Specs {
			switch s := s.(type) {
			case *ast.TypeSpec:
				if s.Name.IsExported() {
					out = append(out, s.Name.Name)
				}
			case *ast.ValueSpec:
				for _, n := range s.Names {
					if n.IsExported() {
						out = append(out, n.Name)
					}
				}
			}
		}
	}
	return out
}
