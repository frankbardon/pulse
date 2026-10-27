//go:build heavy

package guide

import (
	"go/build"
	"os"
	"strings"
	"testing"
)

// TestGuideNoExecutionImports is internal/guide's no-execute import
// ban — the same ban TestPredictNoExecutionImports holds predict to: no
// non-test file here may import internal/service or internal/processing
// (any engine subpackage included). Transitively, no module package it
// reaches may be internal/service or the engine root
// internal/processing; the numeric leaf internal/processing/regression
// is already reached through internal/descriptor -> internal/synth and
// executes nothing on its own.
func TestGuideNoExecutionImports(t *testing.T) {
	t.Parallel()

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
