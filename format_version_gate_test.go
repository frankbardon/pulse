package pulse

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadSchemaCallersThreadHeaderVersion is the structural half of the
// format-version contract: every production call to encoding.ReadSchema
// must pass, as its version argument, a variable assigned from
// encoding.ReadHeader in the SAME function. A constant (0x01,
// FormatVersion) or any expression there means the header's version was
// dropped, and a 0x02 schema block would be parsed with the 0x01 layout —
// silently, while the extension block is empty, and wrongly once it
// carries the group descriptor. The facade parity tests cover the paths
// they drive; this gate covers every call site, including the ones no
// facade test reaches (joins, chains, parallel decode, widen, SPSS
// export, shard rewrite).
func TestReadSchemaCallersThreadHeaderVersion(t *testing.T) {
	fset := token.NewFileSet()
	var offenders []string
	sites := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != "." && (strings.HasPrefix(name, ".") || name == "testdata" || name == "docs" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		inspectFuncs(f, func(body *ast.BlockStmt) {
			versionVars := map[string]bool{}
			ast.Inspect(body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || len(as.Rhs) != 1 || len(as.Lhs) < 1 {
					return true
				}
				if call, ok := as.Rhs[0].(*ast.CallExpr); ok && calleeName(call) == "ReadHeader" && len(call.Args) == 1 {
					if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
						versionVars[id.Name] = true
					}
				}
				return true
			})
			ast.Inspect(body, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false // closures are checked as their own body
				}
				call, ok := n.(*ast.CallExpr)
				if !ok || calleeName(call) != "ReadSchema" || len(call.Args) != 2 {
					return true
				}
				sites++
				id, ok := call.Args[1].(*ast.Ident)
				if !ok || !versionVars[id.Name] {
					offenders = append(offenders, fset.Position(call.Pos()).String())
				}
				return true
			})
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking source: %v", err)
	}
	if sites < 30 {
		t.Fatalf("found only %d ReadSchema call sites; the walk is not seeing the tree", sites)
	}
	for _, o := range offenders {
		t.Errorf("%s: ReadSchema's version argument is not the value ReadHeader returned in this function", o)
	}
}

// inspectFuncs calls fn with the body of every function declaration and
// function literal in f.
func inspectFuncs(f *ast.File, fn func(*ast.BlockStmt)) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Body != nil {
				fn(x.Body)
			}
		case *ast.FuncLit:
			fn(x.Body)
		}
		return true
	})
}

// calleeName returns the bare function name of a call: ReadSchema for
// both ReadSchema(...) and encoding.ReadSchema(...). Method calls on
// other receivers (r.ReadHeader() on a tabular io.Reader) have no
// argument match and are filtered by the callers' arity checks.
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "encoding" {
			return fn.Sel.Name
		}
	}
	return ""
}
