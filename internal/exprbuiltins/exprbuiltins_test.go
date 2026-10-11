package exprbuiltins

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNames_CopyAndMembership(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("Names() is empty")
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Errorf("duplicate reserved name %q", n)
		}
		seen[n] = true
		if !IsReserved(n) {
			t.Errorf("IsReserved(%q) = false for a listed name", n)
		}
	}
	for _, n := range []string{"abs", "len", "total", "has_any", "coef", "lookup", ""} {
		if IsReserved(n) {
			t.Errorf("IsReserved(%q) = true; only the listed built-ins are reserved", n)
		}
	}
	names[0] = "mutated"
	if Names()[0] == "mutated" {
		t.Error("Names() returned the backing slice; callers could mutate the reservation")
	}
}

// TestExprbuiltinsImportBoundary keeps the package an import-free leaf so
// the root package can import it without reaching the engine.
func TestExprbuiltinsImportBoundary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		if len(af.Imports) > 0 {
			t.Errorf("%s imports %d packages; exprbuiltins must stay an import-free leaf", f, len(af.Imports))
		}
	}
}
