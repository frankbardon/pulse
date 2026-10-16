package exprenv

import (
	"bytes"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strconv"
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

// TestExprenvImportBoundary keeps the shared formula environment a
// no-execute leaf: predict (internal/descriptor, whose predict files may
// not import the engine — TestPredictNoExecutionImports) and the engine
// both compile against it. Direct intra-module imports are pinned; the
// transitive graph is checked against the execution, descriptor and
// vector-resolver packages (internal/vectors imports this one).
func TestExprenvImportBoundary(t *testing.T) {
	const module = "github.com/frankbardon/pulse"
	allowedDirect := map[string]bool{
		module + "/encoding":         true,
		module + "/errors":           true,
		module + "/linalg/metric":    true,
		module + "/internal/vecmath": true,
		// The calendar helpers' only home for epoch-day and zone math
		// (TestNoZoneMathOutsideTemporal); a leaf over stdlib + errors.
		module + "/internal/temporal": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, module) && !allowedDirect[path] {
				t.Errorf("%s imports %q; internal/exprenv may import only encoding, errors, linalg/metric, internal/vecmath and internal/temporal from the module", f, path)
			}
		}
	}

	cmd := exec.Command("go", "list", "-deps", module+"/internal/exprenv")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, stderr.String())
	}
	deps := strings.Fields(out.String())
	if len(deps) == 0 {
		t.Fatal("go list -deps returned nothing; the firewall cannot inspect an empty graph")
	}
	forbidden := []string{
		module + "/internal/processing",
		module + "/internal/service",
		module + "/internal/descriptor",
		module + "/descriptor",
		module + "/internal/vectors",
	}
	for _, dep := range deps {
		for _, bad := range forbidden {
			if dep == bad || strings.HasPrefix(dep, bad+"/") {
				t.Errorf("internal/exprenv transitively imports %s", dep)
			}
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
		{"log1p", true, false},
		{"weekday", false, true},
		{"to_day", false, true},
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
