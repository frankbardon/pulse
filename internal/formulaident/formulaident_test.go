package formulaident

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func keys(m map[string]struct{}) []string { return sorted(m) }

func TestCollect(t *testing.T) {
	t.Parallel()

	tree, err := exprparser.Parse(`sqrt(cell) + ref_cell * slot.us.cell - slot["eu"].value + abs(x) + foo.bar`)
	if err != nil {
		t.Fatal(err)
	}
	r := Collect(&tree.Node)
	if got := keys(r.Vars); !reflect.DeepEqual(got, []string{"cell", "ref_cell", "x"}) {
		t.Errorf("vars = %v", got)
	}
	if got := keys(r.Funcs); !reflect.DeepEqual(got, []string{"sqrt"}) {
		t.Errorf("funcs = %v", got)
	}
	if got := keys(r.SlotPaths); !reflect.DeepEqual(got, []string{"slot.eu.value", "slot.us.cell"}) {
		t.Errorf("slot paths = %v", got)
	}
}

func TestComposeNamespace(t *testing.T) {
	t.Parallel()

	for shape, want := range map[types.OverlayShape]string{
		types.OverlayShapeMatrix: "ref_cell", types.OverlayShapeSeries: "ref_value", types.OverlayShapeScalar: "ref",
	} {
		vars := ComposeVars(shape)
		if _, ok := vars[want]; !ok || ComposeRefVar(shape) != want {
			t.Errorf("%s: ref var %q missing from %v", shape, want, keys(vars))
		}
		for _, v := range types.FormulaNamespace(shape) {
			if _, ok := vars[v]; !ok {
				t.Errorf("%s: %s missing", shape, v)
			}
		}
	}
	got := keys(ComposeSlotPaths([]Slot{{"a", types.OverlayShapeMatrix}, {"b", types.OverlayShapeSeries}, {"c", types.OverlayShapeScalar}}))
	if !reflect.DeepEqual(got, []string{"slot.a.cell", "slot.b.value", "slot.c.value"}) {
		t.Errorf("slot paths = %v", got)
	}
	funcs := AllowedFuncs([]string{"pct", ""}, true)
	for _, f := range []string{"sqrt", "has_any", "pct", "lookup"} {
		if _, ok := funcs[f]; !ok {
			t.Errorf("func %s missing", f)
		}
	}
	if _, ok := AllowedFuncs(nil, false)["lookup"]; ok {
		t.Error("lookup allowed without a LookupTable")
	}
	if _, ok := AllowedFuncs(nil, false)["vmean"]; ok {
		t.Error("a vector built-in is allowed")
	}
	// range_label is declared on every runtime overlay compile, tables
	// or not (an unknown table is its run-time refusal).
	if _, ok := AllowedFuncs(nil, false)["range_label"]; !ok {
		t.Error("range_label not allowed")
	}
}

func TestComposeRefusal(t *testing.T) {
	t.Parallel()

	slots := []Slot{{"a", types.OverlayShapeSeries}, {"b", types.OverlayShapeSeries}}
	funcs := AllowedFuncs(nil, false)
	cases := []struct {
		name    string
		params  map[string]any
		code    errors.Code
		details map[string]any
	}{
		{"missing", map[string]any{}, errors.PULSE_OVERLAY_PARAM_MISSING,
			map[string]any{"index": 3, "kind": "OVERLAY_FORMULA", "param": "formula", "reason": "missing formula param"}},
		{"non-string", map[string]any{"formula": 2.0}, errors.PULSE_OVERLAY_PARAM_MISSING,
			map[string]any{"index": 3, "kind": "OVERLAY_FORMULA", "param": "formula", "reason": "formula param is empty or non-string"}},
		{"empty", map[string]any{"formula": ""}, errors.PULSE_OVERLAY_PARAM_MISSING,
			map[string]any{"index": 3, "kind": "OVERLAY_FORMULA", "param": "formula", "reason": "formula param is empty or non-string"}},
		{"unknown var, sorted first", map[string]any{"formula": "zeta + cell + value"}, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
			map[string]any{"index": 3, "kind": "OVERLAY_FORMULA", "unknown_identifier": "cell", "host_shape": "series",
				"allowed": []string{"prior", "ref_value", "total", "value"}, "formula": "zeta + cell + value"}},
		{"unknown func", map[string]any{"formula": "pct(value)"}, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT, nil},
		{"unknown slot", map[string]any{"formula": "value - slot.c.value"}, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
			map[string]any{"index": 3, "kind": "OVERLAY_FORMULA", "unknown_identifier": "slot.c.value", "host_shape": "series",
				"allowed_slots": []string{"slot.a.value", "slot.b.value"}, "formula": "value - slot.c.value"}},
		{"wrong slot field", map[string]any{"formula": "slot.a.cell"}, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT, nil},
		{"parse", map[string]any{"formula": "(value"}, errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ce := ComposeRefusal(types.OverlayKindFormula, c.params, 3, types.OverlayShapeSeries, slots, funcs)
			if ce == nil || ce.Code != c.code {
				t.Fatalf("got %v, want %s", ce, c.code)
			}
			if c.details != nil && !reflect.DeepEqual(ce.Details, c.details) {
				t.Errorf("details = %v, want %v", ce.Details, c.details)
			}
		})
	}
	for _, f := range []string{"value / ref_value * 100", "(value - prior) / total + slot.b.value", "sqrt(value) + abs(ref_value)"} {
		if ce := ComposeRefusal(types.OverlayKindFormula, map[string]any{"formula": f}, 0, types.OverlayShapeSeries, slots, funcs); ce != nil {
			t.Errorf("%q refused: %v", f, ce)
		}
	}
}

// TestFormulaidentImportBoundary keeps the package a no-execute leaf:
// stdlib, the expr-lang parser / ast, types, errors, exprenv and
// the stdlib-only params read recorder internal/paramread,
// expression-slot list internal/exprslots and the literal table-call
// check internal/tablecall (itself a no-execute leaf,
// TestTablecallImportBoundary).
func TestFormulaidentImportBoundary(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{
		"sort": true, "github.com/expr-lang/expr/ast": true, "github.com/expr-lang/expr/parser": true,
		"github.com/frankbardon/pulse/errors": true, "github.com/frankbardon/pulse/types": true,
		"github.com/frankbardon/pulse/internal/exprenv":   true,
		"github.com/frankbardon/pulse/internal/exprident": true,
		"github.com/frankbardon/pulse/internal/totalcall": true,
		"github.com/frankbardon/pulse/internal/paramread": true,
		"github.com/frankbardon/pulse/internal/exprslots": true,
		"github.com/frankbardon/pulse/internal/tablecall": true,
		"strings": true, "encoding/json": true, "math": true,
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
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !allowed[path] {
				t.Errorf("%s imports %s; formulaident must stay a no-execute leaf", f, path)
			}
		}
	}
}

// noTables is a catalog registering no table (lookup() not the
// built-in; every range table unknown).
type noTables struct{}

func (noTables) HasLookup() bool                         { return false }
func (noTables) LookupAxes(string) ([][]string, bool)    { return nil, false }
func (noTables) RangeKind(string) (kind string, ok bool) { return "", false }

// TestTableRefusal_LocatesTheSpec: with tables, the Compose and
// Request-host gates judge a formula's literal range_label() table once
// its identifiers pass — the evaluating builtin's code with the spec's
// index and kind; nil tables judge none.
func TestTableRefusal_LocatesTheSpec(t *testing.T) {
	t.Parallel()

	slots := []Slot{{"a", types.OverlayShapeSeries}}
	funcs := AllowedFuncs(nil, false)
	params := map[string]any{"formula": `value + (range_label("nope", value) == "x" ? 1 : 0)`}
	ce := ComposeRefusalWithTables(types.OverlayKindFormula, params, 2, types.OverlayShapeSeries, slots, funcs, nil, noTables{})
	if ce == nil || ce.Code != errors.PULSE_RANGE_TABLE_UNKNOWN || ce.Details["index"] != 2 || ce.Details["kind"] != "OVERLAY_FORMULA" || ce.Details["table"] != "nope" {
		t.Errorf("compose: %+v", ce)
	}
	if ce := ComposeRefusalWithParams(types.OverlayKindFormula, params, 2, types.OverlayShapeSeries, slots, funcs, nil); ce != nil {
		t.Errorf("compose without tables: %v", ce)
	}
	spec := &types.OverlaySpec{Kind: types.OverlayKindFormula, Params: []byte(`{"formula":"value + (range_label(\"nope\", value) == \"x\" ? 1 : 0)"}`)}
	ce = RequestSpecRefusalWithTables(spec, 1, types.OverlayShapeSeries, funcs, nil, noTables{})
	if ce == nil || ce.Code != errors.PULSE_RANGE_TABLE_UNKNOWN || ce.Details["index"] != 1 {
		t.Errorf("request: %+v", ce)
	}
}
