package processing

import (
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// expr_env_coef_test.go pins coef(fit, term[, stat]) in the engine:
// every stat reads the bound fit's RegressionResult float64 bit for bit
// in ATTR_FORMULA, a FILTER_EXPRESSION compares against it, an
// all-literal refusal fails the build before any record is read, a
// computed one fails at evaluation with the very same error, a filter
// never reads a coef() refusal as a null-row UNKNOWN, and an embedder
// function named coef owns the name.

func coefFit(t *testing.T) (*model.Set, *types.RegressionResult) {
	t.Helper()
	return bindFit(t, &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}})
}

func processCoef(t *testing.T, set *model.Set, exts *ExtensionRegistry, req *types.Request, recs []*Record) (*types.Response, error) {
	t.Helper()
	p := NewProcessorWithExtensions(predictSchema(), exts)
	p.SetModels(set)
	return p.Process(context.Background(), req, NewSliceIterator(recs))
}

// TestCoef_FormulaEqualsRegressionResult: coef() in ATTR_FORMULA is the
// fit's own coefficient, bound and standard error — exact float
// equality with RegressionResult, the intercept addressable.
func TestCoef_FormulaEqualsRegressionResult(t *testing.T) {
	set, res := coefFit(t)
	if res.ConfLevel == 0 || len(res.CILower) == 0 {
		t.Fatal("the fixture fit reports no intervals")
	}
	cases := []struct {
		expr string
		want float64
	}{
		{`coef("m", "x1")`, res.Coefficients["x1"]},
		{`coef("m", "x1", "point")`, res.Coefficients["x1"]},
		{`coef("m", "x2", "lower")`, res.CILower["x2"]},
		{`coef("m", "x2", "upper")`, res.CIUpper["x2"]},
		{`coef("m", "x1", "se")`, res.StdErrors["x1"]},
		{`coef("m", "(intercept)")`, res.Coefficients[model.InterceptKey]},
		{`coef("m", "(intercept)", "lower")`, res.CILower[model.InterceptKey]},
	}
	schema := predictSchema()
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			p := NewProcessor(schema)
			p.SetModels(set)
			recs := predictRecords(schema)
			if _, err := p.applyAttributes([]*types.Attribute{{Type: types.ATTR_FORMULA, Label: "c", Expression: tc.expr}}, recs); err != nil {
				t.Fatal(err)
			}
			for i, r := range recs {
				got, _ := r.NumericValue("c")
				if math.Float64bits(got) != math.Float64bits(tc.want) {
					t.Fatalf("row %d: %v, want %v", i, got, tc.want)
				}
			}
		})
	}
	// A marginal-return formula multiplies the live coefficient.
	resp, err := processCoef(t, set, nil, &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "roi", Expression: `coef("m", "x1") * x3 / (1 + x3)`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "roi", Label: "s"}},
	}, predictRecords(schema))
	if err != nil {
		t.Fatal(err)
	}
	var want float64
	c := res.Coefficients["x1"]
	for _, r := range predictRecords(schema) {
		x3, _ := r.NumericValue("x3")
		want += c * x3 / (1 + x3)
	}
	if got := resp.Data[0]["s"].(float64); math.Abs(got-want) > 1e-12*math.Abs(want) {
		t.Fatalf("sum of marginal returns %v, want %v", got, want)
	}
}

// TestCoef_FilterExpression: a filter keeps exactly the rows above the
// bound fit's coefficient.
func TestCoef_FilterExpression(t *testing.T) {
	set, res := coefFit(t)
	schema := predictSchema()
	want := 0
	for _, r := range predictRecords(schema) {
		if x1, _ := r.NumericValue("x1"); x1 > res.Coefficients["x1"] {
			want++
		}
	}
	if want == 0 || want == 40 {
		t.Fatalf("the threshold keeps %d of 40 rows; the fixture no longer discriminates", want)
	}
	resp, err := processCoef(t, set, nil, &types.Request{
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `x1 > coef("m", "x1")`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}},
	}, predictRecords(schema))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["n"]; got != float64(want) {
		t.Fatalf("kept %v rows, want %d", got, want)
	}
}

// TestCoef_Refusals: an all-literal call is refused at build, a computed
// one (a let binding) at evaluation — with the identical coded error,
// never PROCESSING_RUNTIME — in a formula and in a filter.
func TestCoef_Refusals(t *testing.T) {
	set, _ := coefFit(t)
	cases := []struct {
		name            string
		fit, term, stat string
		set             *model.Set
		want            *perr.CodedError
	}{
		{"unknown fit", "nope", "x1", "point", set, model.UnknownModel("nope", []string{"m"})},
		{"nothing bound", "m", "x1", "point", nil, model.UnknownModel("m", nil)},
		{"unknown term", "m", "x9", "point", set, model.TermUnknown("m", "x9", []string{"x1", "x2"})},
		{"unknown stat", "m", "x1", "median", set, model.StatUnknown("m", "median")},
	}
	schema := predictSchema()
	for _, tc := range cases {
		literal := `coef("` + tc.fit + `", "` + tc.term + `", "` + tc.stat + `")`
		computed := `let s = "` + tc.stat + `"; coef("` + tc.fit + `", "` + tc.term + `", s)`
		for form, src := range map[string]string{"literal": literal, "computed": computed} {
			reqs := map[string]*types.Request{
				"formula": {Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "c", Expression: src}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "c"}}},
				"filter": {Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: src + ` > 0`}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y"}}},
			}
			for kind, req := range reqs {
				t.Run(tc.name+"/"+form+"/"+kind, func(t *testing.T) {
					_, err := processCoef(t, tc.set, nil, req, predictRecords(schema))
					var ce *perr.CodedError
					if !stderrors.As(err, &ce) || !reflect.DeepEqual(ce, tc.want) {
						t.Fatalf("err = %#v\nwant %#v", err, tc.want)
					}
				})
			}
		}
	}
	// No standard errors: runtime only (predict cannot see the numbers).
	bare, err := model.Bind("bare", &types.RegressionSpec{Type: types.REG_GLM, Target: "y"},
		&types.RegressionResult{Type: types.REG_GLM, Coefficients: map[string]float64{model.InterceptKey: 1}})
	if err != nil {
		t.Fatal(err)
	}
	bareSet := &model.Set{}
	if err := bareSet.Add(bare); err != nil {
		t.Fatal(err)
	}
	_, err = processCoef(t, bareSet, nil, &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "c", Expression: `coef("bare", "(intercept)", "upper")`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "c"}},
	}, predictRecords(schema))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || !reflect.DeepEqual(ce, model.IntervalsUnavailable("bare", "stat", "upper")) {
		t.Fatalf("no intervals: err = %#v", err)
	}
}

// TestCoef_FilterNullRowRefusalSurfaces: on a row whose referenced
// field is null a filter drops the row for an evaluation error — but a
// coef() refusal is the request's fault and surfaces under its code.
func TestCoef_FilterNullRowRefusalSurfaces(t *testing.T) {
	set, _ := coefFit(t)
	// Only a null-x2 row reaches coef(): a non-null one short-circuits.
	recs := predictRecords(predictSchema())
	_, err := processCoef(t, set, nil, &types.Request{
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `let s = "median"; x2 == nil && coef("m", "x1", s) > 0`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y"}},
	}, recs)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_STAT_UNKNOWN {
		t.Fatalf("err = %v, want PULSE_MODEL_STAT_UNKNOWN", err)
	}
}

// TestCoef_EmbedderFunctionShadows: an embedder ExprFunction named coef
// owns the name — its value, no static bound-model check.
func TestCoef_EmbedderFunctionShadows(t *testing.T) {
	exts := &ExtensionRegistry{ExprFunctions: []ExprFunction{{Name: "coef", Fn: func(args ...any) (any, error) { return 42.0, nil }}}}
	resp, err := processCoef(t, nil, exts, &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "c", Expression: `coef("nope", "x")`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "c", Label: "mx"}},
	}, predictRecords(predictSchema()))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["mx"]; got != 42.0 {
		t.Fatalf("embedder coef = %v, want 42", got)
	}
}
