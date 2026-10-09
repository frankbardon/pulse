package guide

import (
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/frankbardon/pulse"

// executionPackages are the packages a no-execute package must never
// reach: the orchestration and engine layers.
var executionPackages = []string{
	modulePath + "/internal/service",
	modulePath + "/internal/processing",
}

func isExecutionImport(p string) bool {
	for _, banned := range executionPackages {
		if p == banned || strings.HasPrefix(p, banned+"/") {
			return true
		}
	}
	return false
}

// isEngineRoot is the transitive ban: the service package (any
// subpackage) or the engine root package itself.
func isEngineRoot(p string) bool {
	return p == executionPackages[1] || p == executionPackages[0] || strings.HasPrefix(p, executionPackages[0]+"/")
}

// bannedImportsIn returns the execution imports of one Go source.
func bannedImportsIn(name string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if isExecutionImport(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// TestGuideNoExecutionImports is internal/guide's no-execute import
// ban — the same ban TestPredictNoExecutionImports holds predict to: no
// non-test file here may import internal/service or internal/processing
// (any engine subpackage included). Transitively, no module package it
// reaches may be internal/service or the engine root
// internal/processing; the numeric leaf internal/processing/regression
// is already reached through internal/descriptor -> internal/synth and
// executes nothing on its own.
func TestGuideNoExecutionImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen++
		bad, err := bannedImportsIn(name, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, p := range bad {
			t.Errorf("%s imports %s: internal/guide is no-execute", name, p)
		}
	}
	if seen == 0 {
		t.Fatal("no non-test Go files found")
	}

	// Transitive: walk every module package internal/guide reaches.
	pkg, err := build.Default.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	visited := map[string]bool{}
	var walk func(path string, via []string)
	walk = func(path string, via []string) {
		if visited[path] || !strings.HasPrefix(path, modulePath) {
			return
		}
		visited[path] = true
		if isEngineRoot(path) {
			t.Errorf("internal/guide reaches %s via %s", path, strings.Join(via, " -> "))
			return
		}
		p, err := build.Default.Import(path, ".", 0)
		if err != nil {
			t.Fatalf("import %s: %v", path, err)
		}
		for _, imp := range p.Imports {
			walk(imp, append(append([]string(nil), via...), path))
		}
	}
	for _, imp := range pkg.Imports {
		walk(imp, []string{"internal/guide"})
	}
	if len(visited) == 0 {
		t.Fatal("transitive walk visited nothing")
	}
}

// TestGuideNoExecutionImports_Falsifier: the checker the gate runs
// flags a direct engine or service import (subpackages included).
func TestGuideNoExecutionImports_Falsifier(t *testing.T) {
	for _, imp := range []string{"internal/service", "internal/processing", "internal/processing/regression"} {
		src := "package guide\nimport _ \"" + modulePath + "/" + imp + "\"\n"
		bad, err := bannedImportsIn("x.go", src)
		if err != nil {
			t.Fatal(err)
		}
		if len(bad) != 1 {
			t.Errorf("import of %s not flagged", imp)
		}
	}
	ok, _ := bannedImportsIn("x.go", "package guide\nimport _ \""+modulePath+"/internal/descriptor\"\n")
	if len(ok) != 0 {
		t.Errorf("internal/descriptor flagged: %v", ok)
	}
}
