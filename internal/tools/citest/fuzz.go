package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// fuzzWorkflow is the live-fuzzing workflow: one matrix leg per target.
const fuzzWorkflow = ".github/workflows/fuzz.yml"

// fuzzLegRe matches one fuzz.yml matrix leg, written as a flow mapping
// on its own line: `- { pkg: "./internal/io/csv", target: FuzzCsvHeaderParse }`.
var fuzzLegRe = regexp.MustCompile(`^\s*-\s*\{\s*pkg:\s*"([^"]+)"\s*,\s*target:\s*(\S+?)\s*\}\s*$`)

// fuzzTarget is one `func FuzzXxx(f *testing.F)` and its package
// directory ("." for the module root, else "./<slash path>").
type fuzzTarget struct{ pkg, name string }

func (t fuzzTarget) String() string { return t.pkg + " " + t.name }

// scanFuzzTargets lists every fuzz target of the root module: each
// top-level `func FuzzXxx(<name> *testing.F)` in a *_test.go file, any
// build tag. AST-parsed, so a target spelled inside a fixture string (the
// parallel-check fixture's FuzzX) is not one. The nested module, testdata
// and dot / underscore dirs are skipped, as in every citest scan.
func scanFuzzTargets(root string) ([]fuzzTarget, error) {
	var out []fuzzTarget
	err := walkTestFiles(root, func(path string) error {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Parse only files that could declare one: the scan runs in the
		// fast tier's 1 s budget, and most test files declare none.
		if !bytes.Contains(src, []byte("func Fuzz")) {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := "."
		if rel != "." {
			pkg = "./" + filepath.ToSlash(rel)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil || !strings.HasPrefix(fd.Name.Name, "Fuzz") || !isTestName(fd.Name.Name, "Fuzz") || !testingFParam(fd) {
				continue
			}
			out = append(out, fuzzTarget{pkg: pkg, name: fd.Name.Name})
		}
		return nil
	})
	sortTargets(out)
	return out, err
}

// testingFParam reports whether fd is func(x *testing.F).
func testingFParam(fd *ast.FuncDecl) bool {
	if fd.Type.TypeParams != nil || fd.Type.Results != nil || len(fd.Type.Params.List) != 1 {
		return false
	}
	star, ok := fd.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "F" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing"
}

// parseFuzzMatrix reads the fuzz.yml matrix legs. Every line that names a
// `target:` must be a well-formed leg, so a leg the parser cannot read is
// an error rather than a silently unchecked one.
func parseFuzzMatrix(src []byte) ([]fuzzTarget, error) {
	var out []fuzzTarget
	for i, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "target:") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := fuzzLegRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("%s:%d: matrix leg must read `- { pkg: \"./dir\", target: FuzzXxx }`: %q", fuzzWorkflow, i+1, strings.TrimSpace(line))
		}
		out = append(out, fuzzTarget{pkg: m[1], name: m[2]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no matrix legs (`- { pkg: ..., target: ... }`)", fuzzWorkflow)
	}
	return out, nil
}

// fuzzProblems compares the matrix with the tree: a target with no leg, a
// leg with no target, and a duplicated leg are each one problem.
func fuzzProblems(tree, matrix []fuzzTarget) []string {
	have := map[fuzzTarget]bool{}
	for _, t := range tree {
		have[t] = true
	}
	var probs []string
	seen := map[fuzzTarget]bool{}
	for _, l := range matrix {
		switch {
		case seen[l]:
			probs = append(probs, fmt.Sprintf("%s: leg %s is listed twice", fuzzWorkflow, l))
		case !have[l]:
			probs = append(probs, fmt.Sprintf("%s: leg %s names no fuzz target in the tree; drop it or fix the package/name", fuzzWorkflow, l))
		}
		seen[l] = true
	}
	for _, t := range tree {
		if !seen[t] {
			probs = append(probs, fmt.Sprintf("%s: fuzz target %s has no matrix leg; add `- { pkg: %q, target: %s }`", fuzzWorkflow, t, t.pkg, t.name))
		}
	}
	sort.Strings(probs)
	return probs
}

// fuzzCheck validates fuzz.yml against the tree under root.
func fuzzCheck(root string, w io.Writer) error {
	tree, err := scanFuzzTargets(root)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(fuzzWorkflow)))
	if err != nil {
		return err
	}
	matrix, err := parseFuzzMatrix(src)
	if err != nil {
		return err
	}
	if probs := fuzzProblems(tree, matrix); len(probs) > 0 {
		return fmt.Errorf("fuzz matrix out of sync:\n%s", strings.Join(probs, "\n"))
	}
	fmt.Fprintf(w, "fuzz-check: %d targets, each with one %s leg\n", len(tree), fuzzWorkflow)
	return nil
}

func sortTargets(ts []fuzzTarget) {
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].pkg != ts[j].pkg {
			return ts[i].pkg < ts[j].pkg
		}
		return ts[i].name < ts[j].name
	})
}
