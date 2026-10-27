package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// serialMarker opts a top-level test out of t.Parallel(). It must carry a
// reason ("// serial: sets PULSE_DATA_DIR via t.Setenv") and sit in the
// test's doc comment or inside its body.
const serialMarker = "// serial:"

// parallelReport is what parallel-check finds.
type parallelReport struct {
	parallel, serial int
	problems         []string // "file:line: TestX: <what is wrong>"
}

// parallelCheck parses every *_test.go file of the root module — both
// tiers, any build tag — and reports each top-level `func TestXxx(t
// *testing.T)` whose body neither calls t.Parallel() as a direct statement
// nor carries a "// serial: <reason>" comment. TestMain, benchmarks, fuzz
// targets and examples are exempt.
//
// The nested modules (internal/embeddersmoke, contrib/*) are skipped, like
// every other citest scan: `make test` never runs them (`make smoke` /
// `make contrib` do, as separate go invocations whose wall time is compile
// bound), so parallelising their ~50 small tests buys nothing, and the
// contrib adapters register process-global OTel / Prometheus state.
func parallelCheck(root string) (parallelReport, error) {
	var paths []string
	if err := walkTestFiles(root, func(path string) error {
		paths = append(paths, path)
		return nil
	}); err != nil {
		return parallelReport{}, err
	}
	// Parse on every core: the gate runs in the fast tier's 1 s budget.
	reps := make([]parallelReport, len(paths))
	errs := make([]error, len(paths))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				reps[i], errs[i] = checkFile(root, paths[i])
			}
		}()
	}
	for i := range paths {
		next <- i
	}
	close(next)
	wg.Wait()
	var rep parallelReport
	for i := range paths {
		if errs[i] != nil {
			return rep, errs[i]
		}
		rep.parallel += reps[i].parallel
		rep.serial += reps[i].serial
		rep.problems = append(rep.problems, reps[i].problems...)
	}
	sort.Strings(rep.problems)
	return rep, nil
}

// checkFile applies the parallel-by-default rule to one test file.
func checkFile(root, path string) (parallelReport, error) {
	var rep parallelReport
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return rep, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Body == nil || fd.Name.Name == "TestMain" || !strings.HasPrefix(fd.Name.Name, "Test") || !isTestName(fd.Name.Name, "Test") {
			continue
		}
		param, ok := testingTParam(fd)
		if !ok {
			continue
		}
		where := fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), fset.Position(fd.Pos()).Line, fd.Name.Name)
		marked, empty := serialComment(f, fd)
		switch {
		case empty:
			rep.problems = append(rep.problems, where+`: "// serial:" has no reason; say why it cannot run in parallel ("// serial: uses t.Setenv")`)
		case callsParallel(fd.Body, param):
			rep.parallel++
		case marked:
			rep.serial++
		default:
			rep.problems = append(rep.problems, where+": add t.Parallel() as the first statement, or a \"// serial: <reason>\" comment above or inside the test")
		}
	}
	return rep, nil
}

// walkTestFiles calls fn for every *_test.go under root, skipping the dirs
// scan skips (dot / underscore dirs, testdata, vendor, nested modules).
func walkTestFiles(root string, fn func(path string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return fn(path)
	})
}

// testingTParam returns the parameter name when fd is func(x *testing.T).
func testingTParam(fd *ast.FuncDecl) (string, bool) {
	if fd.Type.TypeParams != nil || fd.Type.Results != nil || len(fd.Type.Params.List) != 1 {
		return "", false
	}
	p := fd.Type.Params.List[0]
	star, ok := p.Type.(*ast.StarExpr)
	if !ok {
		return "", false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "testing" {
		return "", false
	}
	if len(p.Names) != 1 {
		return "_", true // unnamed parameter: no t.Parallel() is possible
	}
	return p.Names[0].Name, true
}

// callsParallel reports whether a top-level statement of body is
// `<param>.Parallel()`. A call inside a closure or subtest does not count.
func callsParallel(body *ast.BlockStmt, param string) bool {
	for _, st := range body.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Parallel" {
			continue
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == param {
			return true
		}
	}
	return false
}

// serialComment reports whether fd's doc comment or body carries a
// "// serial:" marker, and whether any such marker has an empty reason.
func serialComment(f *ast.File, fd *ast.FuncDecl) (marked, empty bool) {
	for _, cg := range f.Comments {
		if cg != fd.Doc && (cg.Pos() < fd.Body.Lbrace || cg.Pos() > fd.Body.Rbrace) {
			continue
		}
		for _, c := range cg.List {
			if !strings.HasPrefix(c.Text, serialMarker) {
				continue
			}
			marked = true
			if strings.TrimSpace(strings.TrimPrefix(c.Text, serialMarker)) == "" {
				empty = true
			}
		}
	}
	return marked, empty
}

// parallelCheckMain prints the problems and returns 1 when there are any.
func parallelCheckMain(root string, out io.Writer) (int, error) {
	rep, err := parallelCheck(root)
	if err != nil {
		return 1, err
	}
	for _, p := range rep.problems {
		fmt.Fprintln(out, p)
	}
	fmt.Fprintf(out, "parallel-check: %d parallel, %d serial, %d to fix\n", rep.parallel, rep.serial, len(rep.problems))
	if len(rep.problems) > 0 {
		return 1, nil
	}
	return 0, nil
}
