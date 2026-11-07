package processing

import (
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// expr_env_response_test.go pins response(fit, channel, spend) in the
// engine: ATTR_FORMULA reads model.Response bit for bit per row (an
// integer spend as its float64, a null spend as null), a literal call
// on a channel with no adstock record fails the build and a computed
// one fails at evaluation with the very same error.

// responseModels binds the REG_MMM_HILL "mmm" over channel x1 (control
// x2) with x1's geometric record (decay 0.5, max_lag 3, unnormalised).
func responseModels(t *testing.T) *model.Set {
	t.Helper()
	b, err := model.Bind("mmm", &types.RegressionSpec{Type: types.REG_MMM_HILL, Target: "y", Predictors: []string{"x1", "x2"}},
		&types.RegressionResult{Type: types.REG_MMM_HILL, Coefficients: map[string]float64{model.InterceptKey: 1,
			"x1": 2.5, "x2": -0.5, "k:x1": 3, "s:x1": 1.4}})
	if err != nil {
		t.Fatal(err)
	}
	d := 0.5
	b.Adstocks = map[string]model.Adstock{"x1": {Kernel: "geometric", Decay: &d, MaxLag: 3}}
	set := &model.Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	return set
}

// TestResponse_FormulaEqualsModelResponse: per row, response() in
// ATTR_FORMULA is model.Response at the row's spend, bit for bit; a
// literal integer spend reads as its float64; a null spend is null.
func TestResponse_FormulaEqualsModelResponse(t *testing.T) {
	t.Parallel()

	set := responseModels(t)
	schema := predictSchema()
	p := NewProcessor(schema)
	p.SetModels(set)
	recs := predictRecords(schema)
	attrs := []*types.Attribute{
		{Type: types.ATTR_FORMULA, Label: "r", Expression: `response("mmm", "x1", x3)`},
		{Type: types.ATTR_FORMULA, Label: "r2", Expression: `response("mmm", "x1", 2)`},
		{Type: types.ATTR_FORMULA, Label: "rn", Expression: `response("mmm", "x1", x2)`},
	}
	if _, _, err := p.applyAttributes(attrs, recs); err != nil {
		t.Fatal(err)
	}
	at2, _ := model.Response(set, "mmm", "x1", 2)
	defined := 0
	for i, r := range recs {
		x3, _ := r.NumericValue("x3")
		want, cerr := model.Response(set, "mmm", "x1", x3)
		got, ok := r.NumericValue("r")
		if cerr != nil {
			t.Fatal(cerr)
		}
		if math.IsNaN(want) {
			// A negative spend is outside the curve: null, never a figure.
			if ok && !math.IsNaN(got) {
				t.Fatalf("row %d: spend %v gave %v, want null", i, x3, got)
			}
			continue
		}
		defined++
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("row %d: %v, want %v", i, got, want)
		}
		if got, _ := r.NumericValue("r2"); math.Float64bits(got) != math.Float64bits(at2) {
			t.Fatalf("row %d: integer spend %v, want %v", i, got, at2)
		}
		if _, null := r.NumericValue("x2"); !null {
			if v, ok := r.NumericValue("rn"); ok && !math.IsNaN(v) {
				t.Fatalf("row %d: null spend gave %v", i, v)
			}
		}
	}
	if defined == 0 {
		t.Fatal("no row has a non-negative spend; the fixture no longer discriminates")
	}
}

// TestResponse_Refusals: a channel with no record and an unknown fit are
// refused at build when literal and at evaluation when computed, with
// the identical coded error, in a formula and a filter.
func TestResponse_Refusals(t *testing.T) {
	t.Parallel()

	set := responseModels(t)
	cases := []struct {
		name, fit, channel string
		want               *perr.CodedError
	}{
		{"no record", "mmm", "x2", model.AdstockUnknown("mmm", "x2", []string{"x1"})},
		{"unknown fit", "nope", "x1", model.UnknownModel("nope", []string{"mmm"})},
	}
	schema := predictSchema()
	for _, tc := range cases {
		literal := `response("` + tc.fit + `", "` + tc.channel + `", x3)`
		computed := `let c = "` + tc.channel + `"; response("` + tc.fit + `", c, x3)`
		for form, src := range map[string]string{"literal": literal, "computed": computed} {
			reqs := map[string]*types.Request{
				"formula": {Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "c", Expression: src}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "c"}}},
				"filter": {Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: src + ` > 0`}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y"}}},
			}
			for kind, req := range reqs {
				t.Run(tc.name+"/"+form+"/"+kind, func(t *testing.T) {
					_, err := processCoef(t, set, nil, req, predictRecords(schema))
					var ce *perr.CodedError
					if !stderrors.As(err, &ce) || !reflect.DeepEqual(ce, tc.want) {
						t.Fatalf("err = %#v\nwant %#v", err, tc.want)
					}
				})
			}
		}
	}
}

// TestResponseName_MatchesExprenv: the env declares response under
// exprenv.ResponseFunc (the name predict declares too); it must be the
// model rule's own name.
func TestResponseName_MatchesExprenv(t *testing.T) {
	t.Parallel()

	if exprenv.ResponseFunc != model.ResponseFunc {
		t.Fatalf("exprenv.ResponseFunc = %q, model.ResponseFunc = %q", exprenv.ResponseFunc, model.ResponseFunc)
	}
}

// TestResponseBuiltin_SpendForms: an integer spend (a scalar slot's
// params.<name> folds to int64, an expr-lang literal to int) reads as
// its float64, a nil one is null (NaN), and a non-number is a runtime
// fault, never a figure.
func TestResponseBuiltin_SpendForms(t *testing.T) {
	t.Parallel()

	set := responseModels(t)
	fn := model.ResponseBuiltin(set)
	want, _ := model.Response(set, "mmm", "x1", 4)
	for _, spend := range []any{4, int64(4), int32(4), uint8(4), float32(4), 4.0} {
		got, err := fn("mmm", "x1", spend)
		if err != nil || got.(float64) != want {
			t.Errorf("spend %T: %v %v, want %v", spend, got, err, want)
		}
	}
	if got, err := fn("mmm", "x1", nil); err != nil || !math.IsNaN(got.(float64)) {
		t.Errorf("nil spend: %v %v", got, err)
	}
	if _, err := fn("mmm", "x1", "4"); err == nil {
		t.Error("string spend accepted")
	}
}
