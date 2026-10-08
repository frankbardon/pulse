package observe

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestObserveImportBoundary keeps observe a leaf: standard library
// only, nothing from Pulse or any third party, so an adapter module can
// implement it without depending on the engine.
func TestObserveImportBoundary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				t.Errorf("%s imports %q; observe may import only the standard library", f, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no observe source files found")
	}
}

// TestEnumsDistinct guards each enumeration against a duplicate or
// empty value — they are metric label values, so a collision would merge
// two series.
func TestEnumsDistinct(t *testing.T) {
	check := func(name string, vals []string, want int) {
		t.Helper()
		if len(vals) != want {
			t.Errorf("%s: %d values, want %d", name, len(vals), want)
		}
		seen := map[string]bool{}
		for _, v := range vals {
			if v == "" || seen[v] {
				t.Errorf("%s: empty or duplicate value %q", name, v)
			}
			seen[v] = true
		}
	}
	var ops, phases, arms, scopes []string
	for _, v := range AllOperationKinds() {
		ops = append(ops, string(v))
	}
	for _, v := range AllPhases() {
		phases = append(phases, string(v))
	}
	for _, v := range AllArms() {
		arms = append(arms, string(v))
	}
	for _, v := range AllScopes() {
		scopes = append(scopes, string(v))
	}
	check("operation kinds", ops, 39)
	check("phases", phases, 8)
	check("arms", arms, 6)
	check("scopes", scopes, 2)
}
