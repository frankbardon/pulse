package paramread

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// banExempt are the module trees whose `.Params` reads are not an
// operator's runtime read of a slot's params object:
//   - internal/descriptor: the NO-EXECUTE predict judges params it does
//     not run (and owns the allowlist itself, ParamsRefusal);
//   - internal/synth, synth: a synth FieldSpec's distribution params (a
//     spec, not a request slot);
//   - types: the public wire package — its MatrixSpec streamability
//     helpers only peek at a key the matrix decoder reads and records,
//     and it must not import this package (every leaf imports types).
var banExempt = []string{
	"internal/descriptor/",
	"internal/synth/",
	"synth/",
	"types/",
	"internal/paramread/",
}

// TestParamsReadThroughParamread: the declared-vs-read gate
// (TestParamsDeclaredMatchRead) only sees reads that go through this
// package. A direct encoding/json decode of a slot's `.Params` —
// json.Unmarshal(x.Params, …), json.NewDecoder(bytes.NewReader(
// x.Params)) — or an index into a params map (`x.Params["k"]`)
// anywhere else in the module is refused here: read through Decode /
// DecodeOptional / DecodeStrict / Get (recorded), or Probe (a lenient
// look at a key another reader records).
func TestParamsReadThroughParamread(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var hits []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case rel != "." && strings.HasPrefix(d.Name(), "."),
				d.Name() == "testdata", d.Name() == "contrib", d.Name() == "docs", d.Name() == "tmp":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		for _, ex := range banExempt {
			if strings.HasPrefix(rel, ex) {
				return nil
			}
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if isJSONDecode(x) && len(x.Args) > 0 && readsParams(x.Args[0]) {
					hits = append(hits, fset.Position(x.Pos()).String())
				}
			case *ast.IndexExpr:
				if isParamsSelector(x.X) {
					if lit, ok := x.Index.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						hits = append(hits, fset.Position(x.Pos()).String())
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		t.Errorf("%s: reads a slot's .Params outside internal/paramread; use paramread.Decode / DecodeOptional / DecodeStrict / Get (or Probe for a lenient look another reader records)", h)
	}
}

// isJSONDecode reports json.Unmarshal(…) or json.NewDecoder(…).
func isJSONDecode(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "json" && (sel.Sel.Name == "Unmarshal" || sel.Sel.Name == "NewDecoder")
}

// readsParams reports x.Params, or bytes.NewReader(x.Params) /
// bytes.TrimSpace(x.Params) around it.
func readsParams(e ast.Expr) bool {
	if isParamsSelector(e) {
		return true
	}
	if c, ok := e.(*ast.CallExpr); ok && len(c.Args) == 1 {
		return readsParams(c.Args[0])
	}
	return false
}

func isParamsSelector(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Params"
}
