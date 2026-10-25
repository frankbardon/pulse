package optblock_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/types"
)

func intp(n int) *int { return &n }

// demandFits is a batch with one regression fit, `demand`, of units on
// price — the names coef() resolves.
func demandFits() []types.FitSpec {
	return []types.FitSpec{{
		Name: "demand",
		Request: &types.Request{
			Cohort:      &types.Cohort{Filename: "sales.pulse"},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "units", Predictors: []string{"price"}}},
		},
	}}
}

func testEnv() optblock.Env {
	params, cerr := exprenv.NormalizeParams(map[string]any{"budget": 100.0, "floor": 10, "label": "x"})
	if cerr != nil {
		panic(cerr)
	}
	return optblock.Env{Params: params, Fits: model.CatalogFromFits(demandFits())}
}

// validBlock is a price optimisation over the fitted demand curve plus
// a budget-style constraint.
func validBlock() types.OptimizeSpec {
	return types.OptimizeSpec{
		Name: "price",
		Variables: []types.OptimizeVariable{
			{Name: "p", Lower: types.BoundValue(1), Upper: types.BoundValue(50), Start: types.BoundValue(10)},
			{Name: "q", Lower: types.BoundParam("floor"), Upper: types.BoundParam("budget")},
		},
		Objective:   `p * (coef("demand", "(intercept)") + coef("demand", "price") * p) + hill(q, 20, 2)`,
		Direction:   types.OptimizeMaximize,
		Constraints: []types.OptimizeConstraint{{Expr: "params.budget - p - q", Type: types.OptimizeConstraintIneq}},
	}
}

// TestOptimize_RefusalsTable: every rule refuses with its code, reason
// and the optimize-rooted field, and the first fault wins.
func TestOptimize_RefusalsTable(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(s *types.OptimizeSpec)
		code   errors.Code
		reason string
		field  string
	}{
		{"name missing", func(s *types.OptimizeSpec) { s.Name = "" }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonNameMissing, "optimize[0].name"},
		{"no variables", func(s *types.OptimizeSpec) { s.Variables = nil }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonVariablesMissing, "optimize[0].variables"},
		{"empty objective", func(s *types.OptimizeSpec) { s.Objective = "" }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonObjectiveMissing, "optimize[0].objective"},
		{"direction missing", func(s *types.OptimizeSpec) { s.Direction = "" }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonDirectionMissing, "optimize[0].direction"},
		{"direction unknown", func(s *types.OptimizeSpec) { s.Direction = "maximise" }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonDirectionUnknown, "optimize[0].direction"},
		{"constraint expr missing", func(s *types.OptimizeSpec) { s.Constraints[0].Expr = "" }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonConstraintExprMissing, "optimize[0].constraints[0].expr"},
		{"bad constraint type", func(s *types.OptimizeSpec) { s.Constraints[0].Type = "le" }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonConstraintTypeUnknown, "optimize[0].constraints[0].type"},
		{"constraint type missing", func(s *types.OptimizeSpec) { s.Constraints[0].Type = "" }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonConstraintTypeUnknown, "optimize[0].constraints[0].type"},
		{"too many equalities", func(s *types.OptimizeSpec) {
			s.Constraints = []types.OptimizeConstraint{{Expr: "p - 1", Type: "eq"}, {Expr: "q - 1", Type: "eq"}, {Expr: "p - q", Type: "eq"}}
		}, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonTooManyEqualities, "optimize[0].constraints"},
		{"starts zero", func(s *types.OptimizeSpec) { s.Starts = intp(0) }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonStartsInvalid, "optimize[0].starts"},
		{"starts negative", func(s *types.OptimizeSpec) { s.Starts = intp(-3) }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonStartsInvalid, "optimize[0].starts"},
		{"starts over cap", func(s *types.OptimizeSpec) { s.Starts = intp(types.OptimizeMaxStarts + 1) }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonStartsInvalid, "optimize[0].starts"},
		{"negative tolerance", func(s *types.OptimizeSpec) { s.Tolerance = -1e-9 }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonToleranceInvalid, "optimize[0].tolerance"},
		{"negative max_iterations", func(s *types.OptimizeSpec) { s.MaxIterations = -1 }, errors.PULSE_OPTIMIZE_SPEC_INVALID, optblock.ReasonMaxIterationsInvalid, "optimize[0].max_iterations"},
		{"variable name missing", func(s *types.OptimizeSpec) { s.Variables[1].Name = "" }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonNameMissing, "optimize[0].variables[1].name"},
		{"variable name not identifier", func(s *types.OptimizeSpec) { s.Variables[1].Name = "tv-spend" }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonNameInvalid, "optimize[0].variables[1].name"},
		{"variable named params", func(s *types.OptimizeSpec) { s.Variables[1].Name = "params" }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonNameReserved, "optimize[0].variables[1].name"},
		{"variable named hill", func(s *types.OptimizeSpec) { s.Variables[1].Name = "hill" }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonNameReserved, "optimize[0].variables[1].name"},
		{"variable named coef", func(s *types.OptimizeSpec) { s.Variables[1].Name = "coef" }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonNameReserved, "optimize[0].variables[1].name"},
		{"variable named abs", func(s *types.OptimizeSpec) { s.Variables[1].Name = "abs" }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonNameReserved, "optimize[0].variables[1].name"},
		{"variable named a keyword", func(s *types.OptimizeSpec) { s.Variables[1].Name = "nil" }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonNameReserved, "optimize[0].variables[1].name"},
		{"duplicate variable", func(s *types.OptimizeSpec) { s.Variables[1].Name = "p" }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonNameDuplicate, "optimize[0].variables[1].name"},
		{"lower missing", func(s *types.OptimizeSpec) { s.Variables[0].Lower = types.OptimizeBound{} }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonBoundMissing, "optimize[0].variables[0].lower"},
		{"upper missing", func(s *types.OptimizeSpec) { s.Variables[0].Upper = types.OptimizeBound{} }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonBoundMissing, "optimize[0].variables[0].upper"},
		{"lower above upper", func(s *types.OptimizeSpec) { s.Variables[0].Lower = types.BoundValue(60) }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonBoundsCrossed, "optimize[0].variables[0]"},
		{"param bounds crossed", func(s *types.OptimizeSpec) {
			s.Variables[1].Lower, s.Variables[1].Upper = types.BoundParam("budget"), types.BoundParam("floor")
		}, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonBoundsCrossed, "optimize[0].variables[1]"},
		{"non-finite bound", func(s *types.OptimizeSpec) { s.Variables[0].Upper = types.BoundValue(math.Inf(1)) }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonBoundNotNumeric, "optimize[0].variables[0].upper"},
		{"param bound not numeric", func(s *types.OptimizeSpec) { s.Variables[1].Upper = types.BoundParam("label") }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonBoundNotNumeric, "optimize[0].variables[1].upper"},
		{"param bound not a name", func(s *types.OptimizeSpec) { s.Variables[1].Upper = types.BoundParam("a-b") }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonBoundInvalid, "optimize[0].variables[1].upper"},
		{"param bound undeclared", func(s *types.OptimizeSpec) { s.Variables[1].Upper = types.BoundParam("ceiling") }, errors.PULSE_PARAMS_UNDECLARED, "", "optimize[0].variables[1].upper"},
		{"start outside bounds", func(s *types.OptimizeSpec) { s.Variables[0].Start = types.BoundValue(0) }, errors.PULSE_OPTIMIZE_VARIABLE_INVALID, optblock.ReasonStartOutOfBounds, "optimize[0].variables[0].start"},
		{"unknown identifier", func(s *types.OptimizeSpec) { s.Objective = "p * units" }, errors.PULSE_OPTIMIZE_EXPRESSION_INVALID, optblock.ReasonUnknownIdentifier, "optimize[0].objective"},
		{"unknown identifier in constraint", func(s *types.OptimizeSpec) { s.Constraints[0].Expr = "budget - p" }, errors.PULSE_OPTIMIZE_EXPRESSION_INVALID, optblock.ReasonUnknownIdentifier, "optimize[0].constraints[0].expr"},
		{"undeclared param in objective", func(s *types.OptimizeSpec) { s.Objective = "p * params.margin" }, errors.PULSE_PARAMS_UNDECLARED, "", "optimize[0].objective"},
		{"compile error", func(s *types.OptimizeSpec) { s.Objective = "p * (q" }, errors.PULSE_OPTIMIZE_EXPRESSION_INVALID, optblock.ReasonCompileError, "optimize[0].objective"},
		{"unknown function", func(s *types.OptimizeSpec) { s.Objective = "saturate(p)" }, errors.PULSE_OPTIMIZE_EXPRESSION_INVALID, optblock.ReasonCompileError, "optimize[0].objective"},
		{"boolean objective", func(s *types.OptimizeSpec) { s.Objective = "p > q" }, errors.PULSE_OPTIMIZE_EXPRESSION_INVALID, optblock.ReasonNotNumeric, "optimize[0].objective"},
		{"string constraint", func(s *types.OptimizeSpec) { s.Constraints[0].Expr = `"x"` }, errors.PULSE_OPTIMIZE_EXPRESSION_INVALID, optblock.ReasonNotNumeric, "optimize[0].constraints[0].expr"},
		{"unknown fit", func(s *types.OptimizeSpec) { s.Objective = `p * coef("supply", "price")` }, errors.PULSE_MODEL_UNKNOWN, "", "optimize[0].objective"},
		{"unknown term", func(s *types.OptimizeSpec) { s.Objective = `p * coef("demand", "cost")` }, errors.PULSE_MODEL_TERM_UNKNOWN, "", "optimize[0].objective"},
		{"unknown stat", func(s *types.OptimizeSpec) { s.Objective = `p * coef("demand", "price", "median")` }, errors.PULSE_MODEL_STAT_UNKNOWN, "", "optimize[0].objective"},
	}
	env := testEnv()
	if cerr := optblock.Check([]types.OptimizeSpec{validBlock()}, env); cerr != nil {
		t.Fatalf("the valid block is refused: %v", cerr)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := validBlock()
			c.edit(&spec)
			cerr := optblock.Check([]types.OptimizeSpec{spec}, env)
			if cerr == nil {
				t.Fatal("not refused")
			}
			if cerr.Code != c.code {
				t.Fatalf("code = %s, want %s (%s)", cerr.Code, c.code, cerr.Message)
			}
			if got, _ := cerr.Details["reason"].(string); c.reason != "" && got != c.reason {
				t.Errorf("reason = %q, want %q (%s)", got, c.reason, cerr.Message)
			}
			if got := cerr.Details["field"]; got != c.field {
				t.Errorf("field = %v, want %q", got, c.field)
			}
			if got := cerr.Details["optimize"]; got != 0 {
				t.Errorf("optimize = %v, want 0", got)
			}
		})
	}
}

// TestOptimize_BlockNames: a missing or repeated block name is refused at
// the second block, located by its index.
func TestOptimize_BlockNames(t *testing.T) {
	a, b := validBlock(), validBlock()
	cerr := optblock.Check([]types.OptimizeSpec{a, b}, testEnv())
	if cerr == nil || cerr.Code != errors.PULSE_OPTIMIZE_SPEC_INVALID || cerr.Details["reason"] != optblock.ReasonNameDuplicate || cerr.Details["optimize"] != 1 {
		t.Fatalf("duplicate block name: %v", cerr)
	}
	b.Name = "other"
	b.Objective = "p * nope"
	cerr = optblock.Check([]types.OptimizeSpec{a, b}, testEnv())
	if cerr == nil || cerr.Details["optimize"] != 1 || cerr.Details["field"] != "optimize[1].objective" {
		t.Fatalf("second block's fault not located at it: %v", cerr)
	}
}

// TestOptimize_UnknownIdentifierNamesTheVariables: the refusal names the
// offending identifier and lists the variables in reach.
func TestOptimize_UnknownIdentifierNamesTheVariables(t *testing.T) {
	spec := validBlock()
	spec.Objective = "p + spend"
	cerr := optblock.Check([]types.OptimizeSpec{spec}, testEnv())
	if cerr == nil || cerr.Details["identifier"] != "spend" {
		t.Fatalf("got %v", cerr)
	}
	b, _ := json.Marshal(cerr.Details["valid"])
	if string(b) != `["p","q"]` {
		t.Fatalf("valid = %s", b)
	}
}

// TestOptimize_ExtensionsAndLookupInReach: an embedder function and
// lookup() compile when the env declares them, and only then; a variable
// may not take an embedder function's name.
func TestOptimize_ExtensionsAndLookupInReach(t *testing.T) {
	spec := validBlock()
	spec.Objective = `boost(p) + lookup("t", "k")`
	if cerr := optblock.Check([]types.OptimizeSpec{spec}, testEnv()); cerr == nil {
		t.Fatal("undeclared functions compiled")
	}
	env := testEnv()
	env.Extensions, env.Lookup = []string{"boost"}, true
	if cerr := optblock.Check([]types.OptimizeSpec{spec}, env); cerr != nil {
		t.Fatalf("declared functions refused: %v", cerr)
	}
	spec = validBlock()
	spec.Variables[1].Name = "boost"
	if cerr := optblock.Check([]types.OptimizeSpec{spec}, env); cerr == nil || cerr.Details["reason"] != optblock.ReasonNameReserved {
		t.Fatalf("a variable named after an embedder function: %v", cerr)
	}
}

// TestOptimize_CompileResolvesAndEvaluates: Compile resolves the bounds
// (literals and params) and the start (NaN when absent), and the
// compiled objective and constraint evaluate per iterate over Vars,
// the params folded.
func TestOptimize_CompileResolvesAndEvaluates(t *testing.T) {
	spec := validBlock()
	spec.Objective = "p * q + params.budget"
	env := testEnv()
	c, cerr := optblock.Compile(&spec, 0, env, exprenv.Bound{})
	if cerr != nil {
		t.Fatal(cerr)
	}
	if c.Lower[0] != 1 || c.Upper[0] != 50 || c.Start[0] != 10 {
		t.Fatalf("literal bounds = %v %v %v", c.Lower[0], c.Upper[0], c.Start[0])
	}
	if c.Lower[1] != 10 || c.Upper[1] != 100 || !math.IsNaN(c.Start[1]) {
		t.Fatalf("param bounds = %v %v %v", c.Lower[1], c.Upper[1], c.Start[1])
	}
	for _, x := range [][]float64{{2, 3}, {5, 7}} {
		got, err := expr.Run(c.Objective, c.Vars(x))
		if err != nil {
			t.Fatal(err)
		}
		if want := x[0]*x[1] + 100; got != want {
			t.Fatalf("objective(%v) = %v, want %v", x, got, want)
		}
		got, err = expr.Run(c.Constraints[0], c.Vars(x))
		if err != nil {
			t.Fatal(err)
		}
		if want := 100 - x[0] - x[1]; got != want {
			t.Fatalf("constraint(%v) = %v, want %v", x, got, want)
		}
	}
}

// TestOptimize_EffectiveSeed: a pinned seed is echoed; a zero seed is
// derived from the block alone — stable, independent of rng_seed, moved
// by any other change.
func TestOptimize_EffectiveSeed(t *testing.T) {
	spec := validBlock()
	derived := optblock.EffectiveSeed(&spec)
	if derived == 0 || derived != optblock.EffectiveSeed(&spec) {
		t.Fatalf("derived seed %d not stable", derived)
	}
	pinned := spec
	pinned.RNGSeed = 42
	if got := optblock.EffectiveSeed(&pinned); got != 42 {
		t.Fatalf("pinned seed = %d", got)
	}
	other := spec
	other.Objective += " + 1"
	if optblock.EffectiveSeed(&other) == derived {
		t.Fatal("a different block derives the same seed")
	}
}
