package sweep

import (
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

const optBlocks = `{"requests":[],"optimize":[
  {"name":"plain","direction":"minimize","variables":[{"name":"x","lower":0,"upper":1}],"objective":"x"},
  {"name":"b_{{ch}}","direction":"maximize","starts":{"$var":"s"},
   "variables":[{"name":"x","lower":0,"upper":{"$var":"hi"}}],
   "objective":"log(x + 1)","constraints":[{"expr":{"$var":"con"},"type":"ineq"}],
   "for_each":{"mode":"zip","axes":[{"name":"ch","values":["tv","radio"]},{"name":"hi","values":[5,"params.cap"]},
     {"name":"s","values":[1,3]},{"name":"con","values":["4 - x","params.cap - x"]}]}}]}`

// TestExpandOptimize_RendersBlocksInPlace: a fanned-out block — kept as
// written for its {"$var"} markers in typed fields — expands in place
// into one typed block per combination, a plain block kept by value; the
// preflight sees the blocks the fan-out adds; the input is untouched.
func TestExpandOptimize_RendersBlocksInPlace(t *testing.T) {
	t.Parallel()

	c := decodeComposed(t, optBlocks)
	written, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	out, fan, err := ExpandOptimize(c, Options{}, func(n int) error { seen = n; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("preflight saw %d, want 2", seen)
	}
	if len(fan) != 1 || fan[0].Path != "optimize[1]" || fan[0].Count != 2 || fan[0].Mode != types.SweepModeZip {
		t.Fatalf("fan-outs %+v", fan)
	}
	if len(out.Optimize) != 3 || out.Optimize[0].Name != "plain" || out.Optimize[1].Name != "b_tv" || out.Optimize[2].Name != "b_radio" {
		t.Fatalf("blocks %s", mustMarshal(t, out.Optimize))
	}
	b := out.Optimize[2]
	if b.ForEach != nil || b.EffectiveStarts() != 3 || b.Variables[0].Upper.Param() != "cap" || b.Constraints[0].Expr != "params.cap - x" {
		t.Fatalf("rendered block %s", mustMarshal(t, b))
	}
	if v := out.Optimize[1].Variables[0].Upper; v.IsExpr() || v.Value() != 5 {
		t.Fatalf("rendered numeric bound %+v", v)
	}
	if again, _ := json.Marshal(c); string(again) != string(written) {
		t.Fatalf("input mutated:\n%s\n%s", again, written)
	}

	// No for_each: c itself, no preflight.
	plain := decodeComposed(t, `{"requests":[],"optimize":[{"name":"o","direction":"minimize","variables":[{"name":"x","lower":0,"upper":1}],"objective":"x"}]}`)
	got, fan, err := ExpandOptimize(plain, Options{}, func(int) error { t.Fatal("preflight called"); return nil })
	if err != nil || got != plain || fan != nil {
		t.Fatalf("plain batch: %v %v %v", got == plain, fan, err)
	}
}

// TestExpandOptimize_Refusals: the name must carry an axis, an axis in
// an objective / constraint / bound expression is an expression splice,
// and the preflight's error comes back unchanged before anything
// renders.
func TestExpandOptimize_Refusals(t *testing.T) {
	t.Parallel()

	block := func(name, upper, objective string) string {
		return `{"requests":[],"optimize":[{"name":` + name + `,"direction":"minimize","variables":[{"name":"x","lower":0,"upper":` + upper +
			`}],"objective":` + objective + `,"for_each":{"axes":[{"name":"v","values":[1,2]}]}}]}`
	}
	_, _, err := ExpandOptimize(decodeComposed(t, block(`"o"`, `{"$var":"v"}`, `"x"`)), Options{}, nil)
	requireForEachInvalid(t, err, ReasonNameWithoutAxis, "optimize[0].name")
	// A name supplied whole through a marker names the axis.
	if _, _, err := ExpandOptimize(decodeComposed(t, block(`{"$var":"v"}`, `3`, `"x"`)), Options{}, nil); err == nil {
		t.Fatal("a numeric marker name decoded")
	} else if ce := requireForEachInvalid(t, err, ReasonItemDecode, ""); ce == nil {
		t.Fatal("unreachable")
	}
	for _, tc := range []struct{ body, field, slot string }{
		{block(`"o_{{v}}"`, `3`, `"x * {{v}}"`), "optimize[0].objective", "optimize_objective"},
		{block(`"o_{{v}}"`, `"params.cap * {{v}}"`, `"x"`), "optimize[0].variables[0].upper", "optimize_bound"},
	} {
		_, _, err := ExpandOptimize(decodeComposed(t, tc.body), Options{}, nil)
		requireSplice(t, err, errors.PULSE_FOR_EACH_EXPR_SPLICE, tc.field, "v", tc.slot)
	}
	stop := stderrors.New("stop")
	if _, _, err := ExpandOptimize(decodeComposed(t, block(`"o_{{v}}"`, `{"$var":"v"}`, `"x"`)), Options{}, func(int) error { return stop }); err != stop {
		t.Fatalf("preflight error %v, want it unchanged", err)
	}
}

// TestRenderItemCombinations: a raw item renders once per combination;
// a malformed or oversized for_each, or none, is read as written.
func TestRenderItemCombinations(t *testing.T) {
	t.Parallel()

	var item map[string]any
	if err := json.Unmarshal([]byte(`{"type":"AGG_CONSTANT","label":"k_{{ch}}","params":{"expr":{"$var":"e"}},
		"for_each":{"mode":"zip","axes":[{"name":"ch","values":["a","b"]},{"name":"e","values":["params.a","params.b + 1"]}]}}`), &item); err != nil {
		t.Fatal(err)
	}
	got, ok := RenderItemCombinations(item, 10)
	if !ok || len(got) != 2 || got[1]["label"] != "k_b" || got[1]["params"].(map[string]any)["expr"] != "params.b + 1" {
		t.Fatalf("rendered %v %v", got, ok)
	}
	if _, ok := got[0]["for_each"]; ok {
		t.Fatal("a rendered combination kept its for_each")
	}
	if _, ok := RenderItemCombinations(item, 1); ok {
		t.Fatal("rendered past max")
	}
	delete(item, "for_each")
	if _, ok := RenderItemCombinations(item, 10); ok {
		t.Fatal("rendered an item without for_each")
	}
	item["for_each"] = map[string]any{"axes": []any{}}
	if _, ok := RenderItemCombinations(item, 10); ok {
		t.Fatal("rendered a malformed for_each")
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
