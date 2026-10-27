package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// completionConstructors build a valueSource from a library lister.
var completionConstructors = map[string]bool{
	"vocab": true, "vocabBy": true, "assignments": true,
	"cliOwned": true, "fromPulse": true, "fromManifest": true,
	"fromDataDir": true, "fromCohort": true,
}

// completionMarkers are the class markers a table entry may name.
var completionMarkers = map[string]bool{
	"freeValue": true, "fileValue": true,
}

// completionSourceVars are package vars a table may name: each is
// initialised by a constructor call, checked where it is declared.
var completionSourceVars = map[string]bool{
	"archiveShards": true, "cohortValue": true, "fieldValue": true,
}

// completionTables are the package vars holding the flag / positional
// tables.
var completionTables = map[string]bool{
	"flagValues": true, "flagValuesIn": true, "positionalValues": true,
}

// cliOwnedVocab is the one vocabulary the CLI may spell itself: the root
// logging flags' (the CLI builds the logger).
var cliOwnedVocab = map[string]bool{"logLevelNames": true, "logFormatNames": true}

// cliOwnedLiterals are the completion-file slice literals that are not
// candidate lists: the shells `pulse completion` writes scripts for.
var cliOwnedLiterals = map[string]bool{"completionShells": true}

// libraryImports are the public library packages a provider may read.
var libraryImports = map[string]bool{
	"github.com/frankbardon/pulse":            true,
	"github.com/frankbardon/pulse/types":      true,
	"github.com/frankbardon/pulse/descriptor": true,
	"github.com/frankbardon/pulse/errors":     true,
	"github.com/frankbardon/pulse/io":         true,
}

type parsedFile struct {
	name string
	file *ast.File
	libs map[string]bool // local names of libraryImports
}

// TestCompletion_SingleSource keeps every completion candidate in the
// library: the completion code holds no candidate literal, and every
// table entry is a class marker or a constructor that reads a library
// lister (the same one the flag's validator reads). The CLI-owned log
// vocabulary is the one allowlisted exception.
func TestCompletion_SingleSource(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []parsedFile
	pkgNames := map[string]bool{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", n), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		pf := parsedFile{name: n, file: f, libs: map[string]bool{}}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !libraryImports[path] {
				continue
			}
			local := filepath.Base(path)
			if imp.Name != nil {
				local = imp.Name.Name
			} else if path == "github.com/frankbardon/pulse" {
				local = "pulse"
			}
			pf.libs[local] = true
		}
		files = append(files, pf)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					pkgNames[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					switch sp := sp.(type) {
					case *ast.ValueSpec:
						for _, id := range sp.Names {
							pkgNames[id.Name] = true
						}
					case *ast.TypeSpec:
						pkgNames[sp.Name.Name] = true
					}
				}
			}
		}
	}

	g := &singleSourceGate{t: t, fset: fset, pkgNames: pkgNames}
	sawTable := map[string]bool{}
	for _, pf := range files {
		g.libs = pf.libs
		completionFile := strings.HasPrefix(pf.name, "completion")
		for _, d := range pf.file.Decls {
			fd, isFunc := d.(*ast.FuncDecl)
			if isFunc && completionConstructors[fd.Name.Name] {
				continue // the constructors adapt a lister; their bodies hold no candidate
			}
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, id := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						switch {
						case completionTables[id.Name]:
							sawTable[id.Name] = true
							g.checkTableValue(vs.Values[i])
							continue
						case completionMarkers[id.Name]:
							continue
						case completionFile && cliOwnedLiterals[id.Name]:
							continue
						}
						g.checkDecl(vs.Values[i], completionFile)
					}
				}
				continue
			}
			g.checkDecl(d, completionFile)
		}
	}
	for name := range completionTables {
		if !sawTable[name] {
			t.Errorf("completion table %s not found — the gate inspects nothing", name)
		}
	}
	if g.constructorCalls == 0 {
		t.Error("no completion constructor call found — the gate inspects nothing")
	}
}

type singleSourceGate struct {
	t                *testing.T
	fset             *token.FileSet
	pkgNames         map[string]bool
	libs             map[string]bool
	constructorCalls int
}

func (g *singleSourceGate) errorf(n ast.Node, format string, args ...any) {
	g.t.Helper()
	g.t.Errorf("%s: "+format, append([]any{g.fset.Position(n.Pos())}, args...)...)
}

// checkDecl walks code outside the tables: every constructor call is
// checked, a valueSource is never built by hand, and a completion file
// holds no string slice literal.
func (g *singleSourceGate) checkDecl(n ast.Node, completionFile bool) {
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && completionConstructors[id.Name] {
				g.checkConstructor(n)
				return false
			}
		case *ast.CompositeLit:
			if id, ok := n.Type.(*ast.Ident); ok && id.Name == "valueSource" {
				g.errorf(n, "valueSource built by hand; use a class marker or a constructor")
			}
			if completionFile && isStringSliceLit(n) {
				g.errorf(n, "string slice literal in completion code: candidates come from library listers")
			}
		}
		return true
	})
}

// checkTableValue accepts only class markers and constructor calls as a
// table's leaf values.
func (g *singleSourceGate) checkTableValue(e ast.Expr) {
	switch e := e.(type) {
	case *ast.CompositeLit:
		for _, el := range e.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				g.checkTableValue(kv.Value)
				continue
			}
			g.checkTableValue(el)
		}
	case *ast.Ident:
		if !completionMarkers[e.Name] && !g.isSourceVar(e.Name) {
			g.errorf(e, "table entry %s is neither a class marker nor a constructor-built source", e.Name)
		}
	case *ast.CallExpr:
		id, ok := e.Fun.(*ast.Ident)
		if !ok || !completionConstructors[id.Name] {
			g.errorf(e, "table entry calls %s; only completion constructors may build a source", exprString(e.Fun))
			return
		}
		g.checkConstructor(e)
	default:
		g.errorf(e, "table entry %T is neither a class marker nor a constructor call", e)
	}
}

// isSourceVar reports whether name is a package var initialised by a
// constructor call (checked where it is declared).
func (g *singleSourceGate) isSourceVar(name string) bool { return completionSourceVars[name] }

// checkConstructor: the lister argument is a library function (or, for
// cliOwned, the allowlisted CLI vocabulary); no argument spells a
// string, builds a slice or reaches another internal/cli declaration.
func (g *singleSourceGate) checkConstructor(call *ast.CallExpr) {
	g.constructorCalls++
	name := call.Fun.(*ast.Ident).Name
	if len(call.Args) == 0 {
		g.errorf(call, "%s() without a lister", name)
		return
	}
	first := call.Args[0]
	switch name {
	case "cliOwned":
		if id, ok := first.(*ast.Ident); !ok || !cliOwnedVocab[id.Name] {
			g.errorf(call, "cliOwned(%s): only the CLI-owned log vocabulary may be passed", exprString(first))
		}
		return
	case "vocab", "vocabBy", "assignments":
		if !g.isLibrarySelector(first) {
			g.errorf(call, "%s(%s): the lister must be a library function (pulse, types, descriptor, errors, io)", name, exprString(first))
		}
	case "fromPulse", "fromManifest", "fromDataDir", "fromCohort":
		if !g.referencesLibrary(first) {
			g.errorf(call, "%s: the lister must read the library", name)
		}
	}
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					g.errorf(n, "%s: string literal %s in a completion provider", name, n.Value)
				}
			case *ast.CompositeLit:
				if _, ok := n.Type.(*ast.ArrayType); ok {
					g.errorf(n, "%s: slice literal in a completion provider", name)
				}
			case *ast.SelectorExpr:
				// Only the package side of a selector can be a local name.
				ast.Inspect(n.X, func(m ast.Node) bool {
					if id, ok := m.(*ast.Ident); ok && g.pkgNames[id.Name] && !g.libs[id.Name] {
						g.errorf(id, "%s: provider reads internal/cli's %s, not the library", name, id.Name)
					}
					return true
				})
				return false
			case *ast.Ident:
				if g.pkgNames[n.Name] {
					g.errorf(n, "%s: provider reads internal/cli's %s, not the library", name, n.Name)
				}
			}
			return true
		})
	}
}

func (g *singleSourceGate) isLibrarySelector(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && g.libs[id.Name]
}

// referencesLibrary reports whether e calls into a library package or a
// method of a library value (a *pulse.Pulse parameter).
func (g *singleSourceGate) referencesLibrary(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && g.libs[id.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

func isStringSliceLit(c *ast.CompositeLit) bool {
	if _, ok := c.Type.(*ast.ArrayType); !ok {
		return false
	}
	for _, el := range c.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			el = kv.Value
		}
		if lit, ok := el.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			return true
		}
	}
	return false
}

func exprString(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.FuncLit:
		return "func literal"
	}
	return "expression"
}
