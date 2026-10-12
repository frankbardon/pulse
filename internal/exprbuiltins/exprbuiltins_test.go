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

func TestOverlaySubset(t *testing.T) {
	cases := []struct {
		name    string
		allowed bool
		refused bool
	}{
		{"sqrt", true, false},
		{"log", true, false},
		{"exp", true, false},
		{"pow", true, false},
		{"vmean", false, true},
		{"vsum", false, true},
		{"dot", false, true},
		{"cosine", false, true},
		{"argmax", false, true},
		{"vnormalize", false, true},
		{"abs", false, false},
		{"pct_change", false, false},
	}
	for _, tc := range cases {
		if got := OverlayAllowed(tc.name); got != tc.allowed {
			t.Errorf("OverlayAllowed(%q) = %v, want %v", tc.name, got, tc.allowed)
		}
		if got := OverlayRefused(tc.name); got != tc.refused {
			t.Errorf("OverlayRefused(%q) = %v, want %v", tc.name, got, tc.refused)
		}
	}
	for _, n := range OverlayNames() {
		if !IsReserved(n) {
			t.Errorf("overlay name %q is not a reserved built-in", n)
		}
	}
	names := OverlayNames()
	names[0] = "mutated"
	if OverlayNames()[0] == "mutated" {
		t.Error("OverlayNames() returned the backing slice")
	}
}

// TestSetHelperNames: the unreserved set helpers are listed (a copy),
// never reserved and never refused in an overlay formula.
func TestSetHelperNames(t *testing.T) {
	names := SetHelperNames()
	if len(names) != 8 || names[0] != "has_any" || names[7] != "set_xor" {
		t.Fatalf("SetHelperNames = %v", names)
	}
	names[0] = "mutated"
	if SetHelperNames()[0] != "has_any" {
		t.Error("SetHelperNames returned the backing slice")
	}
	for _, n := range SetHelperNames() {
		if IsReserved(n) || OverlayRefused(n) {
			t.Errorf("%s is reserved or overlay-refused", n)
		}
	}
}

// TestMetricFunctions_AreReservedAndTyped: every metric-evaluating
// built-in is a reserved name, its form one of the two metric forms,
// and the declaration-order list carries exactly the table.
func TestMetricFunctions_AreReservedAndTyped(t *testing.T) {
	names := MetricFunctionNames()
	want := []string{"dist", "cosine", "jaccard", "dice", "hamming", "overlap"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("MetricFunctionNames = %v, want %v", names, want)
	}
	for _, n := range names {
		mf, ok := Metric(n)
		if !ok || !IsReserved(n) || mf.Metric == "" || (mf.Form != "distance" && mf.Form != "similarity") {
			t.Errorf("Metric(%q) = %+v, %v (reserved %v)", n, mf, ok, IsReserved(n))
		}
	}
	if mf, _ := Metric("dist"); mf.Metric != "euclidean" || mf.Form != "distance" {
		t.Errorf("dist = %+v", mf)
	}
	if mf, _ := Metric("cosine"); mf.Metric != "cosine" || mf.Form != "similarity" {
		t.Errorf("cosine = %+v", mf)
	}
	if _, ok := Metric("vmean"); ok {
		t.Error("vmean evaluates no metric")
	}
}
