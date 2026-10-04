package extend_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/extend"
)

// engineOnlyNames are engine capabilities extend deliberately does NOT
// expose: Meta* is synthesised by the root adapter from a
// registration's ComponentsFunc, StreamableGrouper (the adapter
// synthesises KeyFor from KeyForRow) and IncludeOrdered are engine
// fast paths extensions never take, and ExtensionAware would hand an
// extension the whole engine registry. Merge capabilities ARE public
// (extend.MergeableAggregator), behind an explicit registration
// declaration, so no Mergeable* prefix is banned here. The row-weight
// machinery is engine-only too: an extension READS a resolved weight
// through Record.Weight, while resolution (StampWeights), validity
// (WeightRowTally) and the floor keys (WeightFloor) stay with the
// orchestrator — an extension never emits sum_weights / n_eff /
// n_weight_invalid.
var engineOnlyNames = []string{"StreamableGrouper", "IncludeOrdered", "ExtensionAware",
	"StampWeights", "WeightFloor", "WeightRowTally"}

var engineOnlyPrefixes = []string{"Meta"}

// TestExtendOmitsEngineOnlyCapabilities fails when a non-test file in
// extend declares an exported identifier naming an engine-only
// capability.
func TestExtendOmitsEngineOnlyCapabilities(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	declared := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			for _, name := range exportedNames(d) {
				declared++
				for _, bad := range engineOnlyNames {
					if name == bad {
						t.Errorf("%s declares engine-only %s", f, name)
					}
				}
				for _, p := range engineOnlyPrefixes {
					if strings.HasPrefix(name, p) {
						t.Errorf("%s declares engine-only %s (prefix %s)", f, name, p)
					}
				}
			}
		}
	}
	if declared == 0 {
		t.Fatal("no exported extend declarations found")
	}
}

func exportedNames(d ast.Decl) []string {
	var out []string
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil && d.Name.IsExported() {
			out = append(out, d.Name.Name)
		}
	case *ast.GenDecl:
		for _, s := range d.Specs {
			switch s := s.(type) {
			case *ast.TypeSpec:
				if s.Name.IsExported() {
					out = append(out, s.Name.Name)
				}
			case *ast.ValueSpec:
				for _, n := range s.Names {
					if n.IsExported() {
						out = append(out, n.Name)
					}
				}
			}
		}
	}
	return out
}

// TestRecordExposesWeight pins the read-only weight accessor on
// extend.Record — the one surface a WeightAware operator reads the
// engine-resolved row weight through — and that Record offers no other
// weight method (no setter, no validity judge: those are the engine's).
func TestRecordExposesWeight(t *testing.T) {
	_ = func(r extend.Record) (float64, bool) { return r.Weight() }
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "record.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	ast.Inspect(af, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Record" {
			return true
		}
		for _, m := range ts.Type.(*ast.InterfaceType).Methods.List {
			for _, name := range m.Names {
				methods = append(methods, name.Name)
			}
		}
		return false
	})
	found := false
	for _, m := range methods {
		switch {
		case m == "Weight":
			found = true
		case strings.Contains(m, "Weight"):
			t.Errorf("extend.Record declares %s; the weight is read-only (Weight)", m)
		}
	}
	if !found {
		t.Fatalf("extend.Record does not declare Weight (methods %v)", methods)
	}
}
