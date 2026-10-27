//go:build heavy

package sem

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestPolychoric_NoFusibleProducts is a source scan: every non-constant
// float product in the two-step files is the direct operand of a
// float64(...) conversion, the only construct the Go specification
// forbids the compiler to contract into a fused multiply-add.
func TestPolychoric_NoFusibleProducts(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := conf.Check("sem", fset, files, info); err != nil {
		t.Fatal(err)
	}
	scanned := map[string]int{}
	for _, f := range files {
		name := filepath.Base(fset.Position(f.Pos()).Filename)
		if !slices.Contains(twoStepFiles, name) {
			continue
		}
		barriered := map[ast.Expr]bool{}
		var products []*ast.BinaryExpr
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "float64" && len(n.Args) == 1 {
					barriered[ast.Unparen(n.Args[0])] = true
				}
			case *ast.AssignStmt:
				if n.Tok == token.MUL_ASSIGN {
					if b, ok := info.Types[n.Lhs[0]].Type.Underlying().(*types.Basic); ok && b.Info()&types.IsFloat != 0 {
						t.Errorf("%s: float `*=` cannot carry a float64() barrier", fset.Position(n.Pos()))
					}
				}
			case *ast.BinaryExpr:
				if n.Op != token.MUL || info.Types[n].Value != nil {
					return true
				}
				if b, ok := info.Types[n].Type.Underlying().(*types.Basic); ok && b.Info()&types.IsFloat != 0 {
					products = append(products, n)
				}
			}
			return true
		})
		scanned[name] = len(products)
		for _, p := range products {
			if !barriered[p] {
				t.Errorf("%s: float product without a float64() barrier", fset.Position(p.Pos()))
			}
		}
	}
	for _, name := range twoStepFiles {
		if scanned[name] < 10 {
			t.Errorf("%s: only %d float products found — the scan is broken or the file moved", name, scanned[name])
		}
	}
}
