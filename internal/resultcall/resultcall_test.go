package resultcall

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/types"
)

// batch is three slots: "current" (ungrouped, aggregations tv and the
// default-labelled AGG_COUNT_week), an unlabelled grouped slot
// (request_2) and "cells" (a crosstab).
func batch() []*types.Request {
	return []*types.Request{
		{Label: "current", Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "spend_tv", Label: "tv"},
			{Type: types.AGG_COUNT, Field: "week"},
		}},
		{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}},
		nil,
		{Label: "cells", Crosstab: &types.CrosstabSpec{}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}},
	}
}

func reasonOf(t *testing.T, cerr *errors.CodedError) string {
	t.Helper()
	if cerr == nil {
		return ""
	}
	if cerr.Code != errors.PULSE_RESULT_INVALID {
		t.Fatalf("code %s, want PULSE_RESULT_INVALID", cerr.Code)
	}
	r, _ := cerr.Details["reason"].(string)
	return r
}

// TestRefusal_Static: the static rule over a three-slot batch — every
// reason, in its order, and the passes.
func TestRefusal_Static(t *testing.T) {
	t.Parallel()

	cat := CatalogOf(batch())
	cases := []struct {
		src, reason string
	}{
		{`result("current", "tv") * 0.8`, ""},
		{`result("current", "AGG_COUNT_week")`, ""},
		{`params.x + 1`, ""},
		{`result("current", params.agg)`, ReasonArgsNotLiteral},
		{`result("nope", "tv")`, ReasonSlotUnknown},
		{`result("current", "radio")`, ReasonAggregationUnknown},
		{`result("request_2", "s")`, ReasonGrouped},
		{`result("cells", "s")`, ReasonGrouped},
		{`1 + result("current", "tv") + result("nope", "x")`, ReasonSlotUnknown},
	}
	for _, tc := range cases {
		if got := reasonOf(t, Refusal(tc.src, cat)); got != tc.reason {
			t.Errorf("%s: reason %q, want %q", tc.src, got, tc.reason)
		}
	}
	cerr := Refusal(`result("nope", "tv")`, cat)
	if want := []string{"current", "request_2", "cells"}; !reflect.DeepEqual(cerr.Details["available"], want) {
		t.Errorf("available %v, want %v", cerr.Details["available"], want)
	}
	cerr = Refusal(`result("current", "radio")`, cat)
	if want := []string{"tv", "AGG_COUNT_week"}; !reflect.DeepEqual(cerr.Details["available"], want) {
		t.Errorf("aggregation available %v, want %v", cerr.Details["available"], want)
	}
}

// TestRefusal_NotNumeric: a catalog built with a non-numeric judgement
// refuses reading an aggregation it marks (after the grouped check),
// naming slot and aggregation; CatalogOf marks none.
func TestRefusal_NotNumeric(t *testing.T) {
	t.Parallel()

	marks := func(a *types.Aggregation) bool { return a.Type == types.AGG_COUNT }
	cat := CatalogWith(batch(), marks)
	cerr := Refusal(`result("current", "AGG_COUNT_week") + 1`, cat)
	if got := reasonOf(t, cerr); got != ReasonNotNumeric {
		t.Fatalf("reason %q, want not_numeric", got)
	}
	if want := map[string]any{"slot": "current", "aggregation": "AGG_COUNT_week", "reason": ReasonNotNumeric}; !reflect.DeepEqual(cerr.Details, want) {
		t.Errorf("details %v, want %v", cerr.Details, want)
	}
	if cerr := Refusal(`result("current", "tv")`, cat); cerr != nil {
		t.Errorf("a numeric aggregation refused: %v", cerr)
	}
	if got := reasonOf(t, Refusal(`result("current", "AGG_COUNT_week")`, CatalogOf(batch()))); got != "" {
		t.Errorf("CatalogOf judged a slot non-numeric: %q", got)
	}
}

// TestRefusal_OutOfScope: a nil catalog refuses any result() call —
// literal or not, any arity — and passes an expression making none.
func TestRefusal_OutOfScope(t *testing.T) {
	t.Parallel()

	for _, src := range []string{`result("current", "tv")`, `result(params.s, "tv")`, `result("a")`, `2 * result("a", "b", "c")`} {
		if got := reasonOf(t, Refusal(src, nil)); got != ReasonOutOfScope {
			t.Errorf("%s: reason %q, want out_of_scope", src, got)
		}
	}
	if cerr := Refusal(`coef("m", "x") + 1`, nil); cerr != nil {
		t.Errorf("no result() call refused: %v", cerr)
	}
	if cerr := Refusal(`result + 1`, nil); cerr != nil {
		t.Errorf("a field named result (not a call) refused: %v", cerr)
	}
}

// TestSlots: the pre-phase run set is the known slots the literal calls
// name, sorted and unique, across every source.
func TestSlots(t *testing.T) {
	t.Parallel()

	reqs := append(batch(), &types.Request{Label: "base", Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}})
	cat := CatalogOf(reqs)
	got := Slots([]string{`result("base", "s")`, `result("current", "tv") + result("base", "s")`, `result("nope", "x")`, `result(params.a, "b")`, `1`}, cat)
	if want := []int{0, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("slots %v, want %v", got, want)
	}
	if got := Slots([]string{`params.x`}, cat); got != nil {
		t.Fatalf("no reader: %v", got)
	}
}

// TestReader_Value: a finished ungrouped response's value, in each
// numeric Go form the engine emits, and each run-time refusal.
func TestReader_Value(t *testing.T) {
	t.Parallel()

	cat := CatalogOf(batch())
	row := func(v any) []*types.Response {
		return []*types.Response{{Data: []map[string]any{{"tv": v}}}}
	}
	for _, tc := range []struct {
		v      any
		want   float64
		reason string
	}{
		{1250.5, 1250.5, ""},
		{int64(12), 12, ""},
		{uint64(7), 7, ""},
		{json.Number("3.25"), 3.25, ""},
		{nil, 0, ReasonNull},
		{math.NaN(), 0, ReasonNull},
		{math.Inf(1), 0, ReasonNotFinite},
		{"x", 0, ReasonNotNumeric},
		{map[string]any{"a": 1.0}, 0, ReasonNotNumeric},
	} {
		r := &Reader{Catalog: cat, Responses: row(tc.v)}
		got, cerr := r.Value("current", "tv")
		if reasonOf(t, cerr) != tc.reason || (cerr == nil && got != tc.want) {
			t.Errorf("%#v: got %v %v, want %v %q", tc.v, got, cerr, tc.want, tc.reason)
		}
	}
	// More than one row (never ungrouped) is grouped; an unrun slot is
	// an internal fault.
	r := &Reader{Catalog: cat, Responses: []*types.Response{{Data: []map[string]any{{"tv": 1.0}, {"tv": 2.0}}}}}
	if _, cerr := r.Value("current", "tv"); reasonOf(t, cerr) != ReasonGrouped {
		t.Errorf("two rows: %v", cerr)
	}
	r = &Reader{Catalog: cat, Responses: []*types.Response{nil}}
	if _, cerr := r.Value("current", "tv"); cerr == nil || cerr.Code != errors.PROCESSING_INTERNAL {
		t.Errorf("unrun slot: %v", cerr)
	}
	// Func is the bound implementation: the float64 value.
	r = &Reader{Catalog: cat, Responses: row(int64(40))}
	if v, err := r.Func("current", "tv"); err != nil || v != 40.0 {
		t.Errorf("Func = %v, %v", v, err)
	}
}

// TestFuncIsExprenvName: the reader's name is the declaration's.
func TestFuncIsExprenvName(t *testing.T) {
	t.Parallel()

	if Func != exprenv.ResultFunc {
		t.Fatalf("Func %q, exprenv.ResultFunc %q", Func, exprenv.ResultFunc)
	}
	if got := Calls(`result("a", "b")`); len(got) != 1 || !got[0].Literal {
		t.Fatalf("result(\"a\", \"b\") not found as a call: %+v", got)
	}
}

// TestResultCall_ImportBoundary: a no-execute leaf (predict imports
// it) — types, errors and exprenv only.
func TestResultCall_ImportBoundary(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{
		"github.com/frankbardon/pulse/types":            true,
		"github.com/frankbardon/pulse/errors":           true,
		"github.com/frankbardon/pulse/internal/exprenv": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, "github.com/frankbardon/pulse") && !allowed[path] {
				t.Errorf("%s imports %q", e.Name(), path)
			}
		}
	}
}
