package exprenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/frankbardon/pulse/internal/exprslots"
)

// TestTemplateExprSplice_CompileSitesShared: every compile site this
// package declares (an exported Site* constant) is the shared
// expression-slot list's (internal/exprslots), and every one of that
// list's sites is a slot's. A new expression site declared here alone —
// a slot the request-template renderer would not know to refuse a
// `{{token}}` splice into — fails here.
func TestTemplateExprSplice_CompileSitesShared(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var declared []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					n := name.Name
					if !strings.HasPrefix(n, "Site") || len(n) == 4 || !unicode.IsUpper(rune(n[4])) {
						continue
					}
					declared = append(declared, n)
					if i >= len(vs.Values) {
						t.Errorf("%s: compile site %s has no value", f, n)
						continue
					}
					if !isExprslotsSelector(vs.Values[i]) {
						t.Errorf("%s: compile site %s is not declared as exprslots.<Site>; declare it in internal/exprslots (with its Slot) and re-export it here", f, n)
					}
				}
			}
		}
	}
	got := []string{SiteFormula, SiteFilter, SiteTotalArgument, SiteHoldout}
	if len(declared) != len(got) {
		t.Errorf("exprenv declares compile sites %v; this gate checks %d — add the new one to it", declared, len(got))
	}
	if want := exprslots.Sites(); !slices.Equal(got, want) {
		t.Errorf("exprenv compile sites %q, exprslots.Sites() %q", got, want)
	}
	covered := map[string]bool{}
	for _, s := range exprslots.All() {
		for _, site := range s.Sites {
			covered[site] = true
		}
	}
	for _, site := range exprslots.Sites() {
		if !covered[site] {
			t.Errorf("compile site %q is no expression slot's (exprslots.All)", site)
		}
	}
}

// isExprslotsSelector reports whether e spells exprslots.<Name>.
func isExprslotsSelector(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "exprslots"
}
