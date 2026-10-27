package reprosum

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strconv"
	"strings"
	"testing"
)

// parseKernel parses the package's non-test files.
func parseKernel(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
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
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no non-test Go files found")
	}
	return fset, files
}

// TestReproSum_ImportBoundary keeps the kernel a stdlib-only leaf.
func TestReproSum_ImportBoundary(t *testing.T) {
	t.Parallel()

	_, files := parseKernel(t)
	for _, f := range files {
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
				t.Errorf("imports %q: reprosum may import only the standard library", path)
			}
		}
	}
}

// TestReproSum_NoFusibleProducts is the FMA-barrier gate. Go may fuse a
// float product that feeds an add or subtract (x*y + z), even across
// statements, into one FMA on arm64, ppc64le and s390x but not on amd64;
// an explicit float64(...) conversion forbids it. So every non-constant
// float multiplication in the kernel must be the direct operand of a
// float64(...) conversion, and the architectures can never disagree.
func TestReproSum_NoFusibleProducts(t *testing.T) {
	t.Parallel()

	fset, files := parseKernel(t)
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := conf.Check("reprosum", fset, files, info); err != nil {
		t.Fatal(err)
	}
	barriered := map[ast.Expr]bool{}
	var products []*ast.BinaryExpr
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "float64" && len(n.Args) == 1 {
					barriered[ast.Unparen(n.Args[0])] = true
				}
			case *ast.BinaryExpr:
				if n.Op != token.MUL {
					return true
				}
				tv := info.Types[n]
				if tv.Value != nil { // constant: folded exactly at compile time
					return true
				}
				if b, ok := tv.Type.Underlying().(*types.Basic); ok && b.Info()&types.IsFloat != 0 {
					products = append(products, n)
				}
			}
			return true
		})
	}
	if len(products) == 0 {
		t.Fatal("found no float products: the scan is broken")
	}
	for _, p := range products {
		if !barriered[p] {
			t.Errorf("%s: float product without a float64() barrier", fset.Position(p.Pos()))
		}
	}
}
