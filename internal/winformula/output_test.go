package winformula

import (
	"encoding/json"
	"reflect"
	"testing"

	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/types"
)

// TestOutputColumns_DeclaresBuiltinCells: each built-in group key is a
// string under its key column (a vector grouper's derived name), each
// built-in aggregation label its cell type; an extension, hidden or
// unparsable slot's column is left undeclared (`any`), as is every
// name when nothing groups or aggregates.
func TestOutputColumns_DeclaresBuiltinCells(t *testing.T) {
	t.Parallel()
	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
			{Type: types.GROUP_KMEANS, Fields: []string{"a", "b"}},
			{Type: "GROUP_ACME_BAND", Field: "band"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x"},
			{Type: types.AGG_SUM, Field: "amt", Label: "fees"},
			{Type: types.AGG_SET_UNION, Field: "tags", Label: "u"},
			{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "fr"},
			{Type: types.AGG_WELFORD, Field: "x", Label: "w"},
			{Type: types.AGG_VEC_MEAN, Fields: []string{"a", "b"}, Label: "v"},
			{Type: types.AGG_CONSTANT, Label: "tier", Params: json.RawMessage(`{"type":"categorical_u8","value":"gold"}`)},
			{Type: types.AGG_CONSTANT, Label: "bad", Params: json.RawMessage(`{"type":"nope","value":1}`)},
			{Type: types.AGG_MEDIAN, Field: "x", Label: "med"},
			{Type: "AGG_ACME_SCORE", Field: "x", Label: "score"},
		},
	}
	got := OutputColumns(req, func(t string) bool { return t == string(types.AGG_MEDIAN) }, nil)
	want := map[string]reflect.Type{
		"region":         typeString,
		"GROUP_KMEANS_a": typeString,
		"AGG_SUM_x":      typeFloat,
		"fees":           typeFloat,
		"u":              typeLabels,
		"fr":             typeCountMap,
		"w":              typeWelford,
		"v":              typeVector,
		"tier":           typeString,
	}
	if len(got) != len(want) {
		t.Errorf("declared %d columns %v, want %d", len(got), got, len(want))
	}
	for name, typ := range want {
		d, ok := got[name]
		if !ok || !d.Equal(declType(typ)) || d.String() != declType(typ).String() {
			t.Errorf("%s declared %v, want %v", name, d, typ)
		}
	}
	for _, name := range []string{"band", "bad", "med", "score"} {
		if d, ok := got[name]; ok {
			t.Errorf("%s declared %v, want undeclared", name, d)
		}
	}
	if OutputColumns(&types.Request{Windows: []*types.Window{{Type: types.WIN_FORMULA}}}, nil, nil) != nil || OutputColumns(nil, nil, nil) != nil {
		t.Error("a request that groups and aggregates nothing declared output columns")
	}
}

// TestCompile_OutputColumns: over the declared output cells arithmetic
// on a label key, a label list, a count map or the Welford triple is
// PULSE_FORMULA_COMPILE_ERROR; a key comparison, a list length, a map
// lookup and a Welford member read compile, and the member read
// evaluates against the engine's own triple (same layout, another type).
func TestCompile_OutputColumns(t *testing.T) {
	t.Parallel()
	req := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			{Type: types.AGG_SET_UNION, Field: "tags", Label: "u"},
			{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "fr"},
			{Type: types.AGG_WELFORD, Field: "x", Label: "w"},
		},
	}
	cols := OutputColumns(req, nil, nil)
	for _, src := range []string{"region * 2", "n + region", "u * 2", "fr / n", "w + 1", "region"} {
		if _, ce := Compile(src, cols, exprenv.Bound{}, false); ce == nil || ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR {
			t.Errorf("%q: %v, want PULSE_FORMULA_COMPILE_ERROR", src, ce)
		}
	}
	type engineTriple struct {
		Mean, Variance float64
		N              uint64
	}
	env := map[string]any{"region": "north", "n": 4.0, "u": []string{"a", "b"}, "fr": map[string]int{"a": 2}, "w": engineTriple{Mean: 1.5, Variance: 2, N: 4}}
	for src, want := range map[string]float64{
		`region == "north" ? n : 0`: 4, "len(u) * n": 8, `fr["a"] / n`: 0.5, "w.Mean * w.N": 6,
	} {
		prog, ce := Compile(src, cols, exprenv.Bound{}, false)
		if ce != nil {
			t.Fatalf("%q: %v", src, ce)
		}
		if got, ok, err := prog.Eval(env); err != nil || !ok || got != want {
			t.Errorf("%q = %v, %v, %v; want %v", src, got, ok, err, want)
		}
	}
	if !(structType{t: typeWelford}).Equal(exprtypes.Any) || (structType{t: typeWelford}).Equal(exprtypes.Float64) {
		t.Error("the struct declaration's Equal is not any-or-itself")
	}
}

// TestOutputColumns_SetFrequencyByResolvedWeight: AGG_SET_FREQUENCY's
// cell is the integer count map unweighted and the Σw map under the
// weight its slot resolves to — its own, the request's or the default
// def (weighting.Stamp) — and `weight: null` on the slot keeps the
// count map under either.
func TestOutputColumns_SetFrequencyByResolvedWeight(t *testing.T) {
	t.Parallel()
	wt := &types.WeightSpec{Field: "wt", Kind: types.WeightKindFrequency}
	req := func(slot types.SlotWeight, reqW *types.WeightSpec) *types.Request {
		return &types.Request{Weight: reqW, Aggregations: []*types.Aggregation{{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "fr", Weight: slot}}}
	}
	for name, c := range map[string]struct {
		req  *types.Request
		def  *types.WeightSpec
		want reflect.Type
	}{
		"unweighted":         {req(types.SlotWeight{}, nil), nil, typeCountMap},
		"slot":               {req(types.SlotWeightOf(*wt), nil), nil, typeSumWMap},
		"request":            {req(types.SlotWeight{}, wt), nil, typeSumWMap},
		"default":            {req(types.SlotWeight{}, nil), wt, typeSumWMap},
		"null_under_request": {req(types.NullSlotWeight(), wt), nil, typeCountMap},
		"null_under_default": {req(types.NullSlotWeight(), nil), wt, typeCountMap},
	} {
		got := OutputColumns(c.req, nil, c.def)["fr"]
		if got == nil || got.String() != declType(c.want).String() {
			t.Errorf("%s: fr declared %v, want %v", name, got, c.want)
		}
	}
	if AggregationCellType(&types.Aggregation{Type: types.AGG_SET_FREQUENCY, Field: "tags", Weight: types.SlotWeightOf(*wt)}) != typeSumWMap {
		t.Error("a weighted slot's cell is not the Σw map")
	}
}
