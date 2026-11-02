package processing

import (
	"errors"
	"reflect"
	"testing"

	"github.com/expr-lang/expr"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/tablecall"
)

// TestTableCall_StaticMatchesEvaluation: for every literal lookup() /
// range_label() fault tablecall judges statically, evaluating the
// expression through the registry's builtins raises the same code,
// message and details — so a static refusal is exactly the run-time one
// it forestalls — and an expression it passes evaluates without error.
func TestTableCall_StaticMatchesEvaluation(t *testing.T) {
	t.Parallel()

	reg := rangeLabelRegistry()
	reg.LookupTables = map[string]LookupTable{
		"grid": {
			Rows: map[string]float64{"low|a": 1, "mid|b": 2},
			Axes: [][]string{{"low", "mid", "high"}, {"a", "b"}},
		},
		"free": {Rows: map[string]float64{"x": 1}},
	}
	env := map[string]any{"score": 15.0, "region": "north", "tags": []string{"a"}}
	cat := reg.TablesCatalog()
	for _, c := range []struct {
		src  string
		code perr.Code // "" = passes
	}{
		{`lookup("nope", "low", "a")`, perr.PULSE_LOOKUP_TABLE_UNKNOWN},
		{`lookup("grid", "low", "zz")`, perr.PULSE_LOOKUP_MISS},
		{`lookup("grid", "low")`, perr.PULSE_LOOKUP_MISS},
		{`lookup("grid", "low", "a", "b")`, perr.PULSE_LOOKUP_MISS},
		{`lookup("grid", 1.5, "a")`, perr.PULSE_LOOKUP_MISS},
		{`lookup("grid", nil, "zz")`, perr.PULSE_LOOKUP_MISS},
		{`lookup("free", "y")`, ""}, // keyless: left to evaluation (it misses there)
		{`lookup("grid", "low", "a")`, ""},
		{`lookup("grid", "mid|b")`, ""},
		{`lookup("grid", region, "a")`, ""}, // computed key: left to evaluation
		{`range_label("nope", score)`, perr.PULSE_RANGE_TABLE_UNKNOWN},
		{`range_label("bands", region)`, perr.PULSE_RANGE_TABLE_KIND_MISMATCH},
		{`range_label("fy", "2024-01-01")`, perr.PULSE_RANGE_TABLE_KIND_MISMATCH},
		{`range_label("bands", true)`, perr.PULSE_RANGE_TABLE_KIND_MISMATCH},
		{`range_label("bands", tags)`, perr.PULSE_RANGE_TABLE_KIND_MISMATCH},
		{`range_label("bands", score)`, ""},
		{`range_label("fy", 19800)`, ""},
	} {
		prog, err := expr.Compile(c.src, append([]expr.Option{expr.Env(env)}, reg.ExprOptions()...)...)
		if err != nil {
			t.Fatalf("%s: compile: %v", c.src, err)
		}
		static := tablecall.Refusal(prog.Node(), cat)
		_, runErr := expr.Run(prog, env)
		if c.code == "" {
			if static != nil {
				t.Errorf("%s: static refusal %v, want none", c.src, static)
			}
			continue
		}
		if static == nil || static.Code != c.code {
			t.Errorf("%s: static %v, want %s", c.src, static, c.code)
			continue
		}
		var ce *perr.CodedError
		if !errors.As(runErr, &ce) {
			t.Errorf("%s: evaluation %v is not coded", c.src, runErr)
			continue
		}
		if ce.Code != static.Code || ce.Message != static.Message || !reflect.DeepEqual(ce.Details, static.Details) {
			t.Errorf("%s:\nstatic %s %q %v\nrun    %s %q %v", c.src, static.Code, static.Message, static.Details, ce.Code, ce.Message, ce.Details)
		}
	}
}

// TestTableCall_SourceRefusalSeesOnlyLiterals: over a parse tree (the
// overlay gate's view) a field's type is not visible, so only a literal
// value is judged by kind.
func TestTableCall_SourceRefusalSeesOnlyLiterals(t *testing.T) {
	t.Parallel()

	cat := rangeLabelRegistry().TablesCatalog()
	if ce := tablecall.SourceRefusal(`range_label("bands", region)`, cat); ce != nil {
		t.Errorf("untyped field judged: %v", ce)
	}
	if ce := tablecall.SourceRefusal(`range_label("bands", "x")`, cat); ce == nil || ce.Code != perr.PULSE_RANGE_TABLE_KIND_MISMATCH {
		t.Errorf("string literal: %v, want PULSE_RANGE_TABLE_KIND_MISMATCH", ce)
	}
	if ce := tablecall.SourceRefusal(`lookup("nope", "a")`, cat); ce != nil {
		t.Errorf("lookup judged with no lookup table registered: %v", ce)
	}
	if ce := tablecall.SourceRefusal(`range_label("bands", score)`, nil); ce != nil {
		t.Errorf("nil catalog judged: %v", ce)
	}
}
