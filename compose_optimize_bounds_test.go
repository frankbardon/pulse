package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/optfixture"
	"github.com/frankbardon/pulse/types"
)

// compose_optimize_bounds_test.go pins the scalar-expression optimize
// bound (library-gaps E1-S1, G11) at the facade: a bound or start is a
// literal, a params expression, a coef() expression or a sweep_axis()
// expression; predict resolves and range-checks the first two — and a
// coef() read of the block's own loaded `models` (folded, U67 E5-S2) —
// and lists the others under unresolved_scalars; both orchestrators
// evaluate every one and range-check it at runtime.

// predictUnresolved is PredictCompose's verdict on c: no error, and the
// unresolved scalar slots it lists.
func predictUnresolved(t *testing.T, p *Pulse, c *ComposedRequest) []descriptor.UnresolvedScalar {
	t.Helper()
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict refused: %+v", env.Errors)
	}
	return env.Data.(*ComposePredictResult).UnresolvedScalars
}

// priceUpper is the saved-model price block (a fixed demand curve, its
// unconstrained optimum optfixture.ModelOptimum = 17.5) with params and
// the price variable's upper bound replaced.
func priceUpper(t *testing.T, params map[string]any, upper types.OptimizeBound) *ComposedRequest {
	t.Helper()
	return decodeOpt(t, optfixture.PriceModel, func(c *ComposedRequest) {
		for k, v := range params {
			c.Params[k] = v
		}
		c.Optimize[0].Variables[0].Upper = upper
	})
}

// TestOptimize_ExpressionBounds: a literal, a params expression and a
// coef() expression upper bound each bind the solve on both
// orchestrators — a cap below the optimum leaves the price on it — and
// predict resolves all three (nothing unresolved): the coef() one reads
// the block's loaded model, whose figures predict folds.
func TestOptimize_ExpressionBounds(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	coefCap := `-coef("demand", "(intercept)") / coef("demand", "price") / 8`
	cases := []struct {
		name       string
		params     map[string]any
		upper      types.OptimizeBound
		want       float64 // the price at the optimum
		active     types.OptimizeActiveBound
		unresolved []descriptor.UnresolvedScalar
	}{
		{"literal", nil, types.BoundValue(30), optfixture.ModelOptimum, "", nil},
		{"literal cap", nil, types.BoundValue(12), 12, types.OptimizeActiveUpper, nil},
		{"params", map[string]any{"cap_share": json.Number("2")}, types.BoundExpr("params.cost * params.cap_share"), 10, types.OptimizeActiveUpper, nil},
		{"coef", nil, types.BoundExpr(coefCap), 3.75, types.OptimizeActiveUpper, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := priceUpper(t, tc.params, tc.upper)
			if got := predictUnresolved(t, p, c); !reflect.DeepEqual(got, tc.unresolved) {
				t.Fatalf("predict unresolved_scalars = %+v, want %+v", got, tc.unresolved)
			}
			for name, run := range composeRuns(p) {
				resp, err := run(c)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				v := oneOptimum(t, name, resp).Variables[0]
				if math.Abs(v.Value-tc.want) > 1e-9 || v.Active != tc.active {
					t.Errorf("%s: price %+v, want %v active %q", name, v, tc.want, tc.active)
				}
			}
		})
	}
}

// TestOptimize_ExpressionBoundRefusalParity: a bound over literals and
// params is range-checked by predict exactly as by both orchestrators
// (code, message, details); so is a coef() bound reading the block's
// loaded model, which predict folds to the value the runtime's Compile
// evaluates — while the runtime's own pre-fit check
// (descx.OptimizeRefusal) leaves it to that Compile.
func TestOptimize_ExpressionBoundRefusalParity(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	static := []struct {
		name   string
		upper  types.OptimizeBound
		code   perr.Code
		reason string
	}{
		{"params bound below lower", types.BoundExpr("params.cost - 5"), perr.PULSE_OPTIMIZE_VARIABLE_INVALID, "bounds_crossed"},
		{"non-finite", types.BoundExpr("params.cost / 0"), perr.PULSE_OPTIMIZE_VARIABLE_INVALID, "bound_not_finite"},
		{"not numeric", types.BoundExpr(`params.cost > 1`), perr.PULSE_OPTIMIZE_VARIABLE_INVALID, "bound_not_numeric"},
		{"reads a variable", types.BoundExpr("p + 1"), perr.PULSE_OPTIMIZE_VARIABLE_INVALID, "bound_invalid"},
		{"undeclared param", types.BoundExpr("params.cost * params.share"), perr.PULSE_PARAMS_UNDECLARED, ""},
		{"unknown term", types.BoundExpr(`coef("demand", "cost")`), perr.PULSE_MODEL_TERM_UNKNOWN, ""},
	}
	for _, tc := range static {
		t.Run(tc.name, func(t *testing.T) {
			c := priceUpper(t, nil, tc.upper)
			env, err := p.PredictCompose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) {
				t.Fatalf("predict errors %+v, want %s", env.Errors, tc.code)
			}
			if tc.reason != "" && env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict reason %v, want %s", env.Errors[0].Details["reason"], tc.reason)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(c)
				ce := requireCoded(t, rerr, tc.code)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}

	// coef("demand", "(intercept)") = 120 as the LOWER bound crosses the
	// literal upper 30: predict folds the loaded model's figure and
	// refuses it exactly as both orchestrators' Compile does; a start
	// folded outside its bounds likewise.
	folded := []struct {
		name   string
		edit   func(c *ComposedRequest)
		reason string
	}{
		{"folded lower crosses upper", func(c *ComposedRequest) {
			c.Optimize[0].Variables[0].Lower = types.BoundExpr(`coef("demand", "(intercept)")`)
		}, "bounds_crossed"},
		{"folded start out of bounds", func(c *ComposedRequest) {
			c.Optimize[0].Variables[0].Start = types.BoundExpr(`-coef("demand", "price") * 10`)
		}, "start_out_of_bounds"},
	}
	for _, tc := range folded {
		t.Run(tc.name, func(t *testing.T) {
			c := decodeOpt(t, optfixture.PriceModel, tc.edit)
			env, err := p.PredictCompose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_OPTIMIZE_VARIABLE_INVALID) || env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict errors %+v, want %s", env.Errors, tc.reason)
			}
			if got := env.Data.(*ComposePredictResult).UnresolvedScalars; got != nil {
				t.Fatalf("predict left the folded bound unresolved: %+v", got)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(c)
				ce := requireCoded(t, rerr, perr.PULSE_OPTIMIZE_VARIABLE_INVALID)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}

// TestOptimize_BoundFailedFitFailsAlone: a block whose upper bound reads
// coef() of a Compose fit that failed on its data fails alone with the
// fit's own code — as an objective read does — on both orchestrators.
func TestOptimize_BoundFailedFitFailsAlone(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	edit := func(c *ComposedRequest) {
		c.Fits[0].Request.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{"1000", "2000"}}}
		c.Optimize[0].Objective = "p"
		c.Optimize[0].Variables[0].Upper = types.BoundExpr(`-coef("demand", "(intercept)") / coef("demand", "price")`)
	}
	for name, run := range composeRuns(p) {
		resp, err := run(decodeOpt(t, optfixture.Price, edit))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		fitErr := resp.Fits["demand"].RegressionResult.Error
		r := resp.Optimizations[0]
		if fitErr == nil || r.Error == nil || r.Error.Code != fitErr.Code || r.Error.Details["fit"] != "demand" ||
			r.Error.Details["field"] != "optimize[0].variables[0].upper" {
			t.Fatalf("%s: fit %+v, block %+v", name, fitErr, r.Error)
		}
	}
}

// TestOptimize_SweepAxisBound: a bound read off the sweep-bound fit's
// winning axis — sweep_axis("best", "hi") / 10 — caps the variable at
// the winner's value on both orchestrators; predict lists it unresolved.
func TestOptimize_SweepAxisBound(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	c := sweepAxisBatch(t)
	c.Optimize[0].Variables[0].Upper = types.BoundExpr(`sweep_axis("best", "hi") / 10`)
	want := []descriptor.UnresolvedScalar{{Path: "optimize[0].variables[0].upper", Expression: `sweep_axis("best", "hi") / 10`, Reads: []string{"sweep_axis"}}}
	if got := predictUnresolved(t, p, c); !reflect.DeepEqual(got, want) {
		t.Fatalf("predict unresolved_scalars = %+v, want %+v", got, want)
	}
	for name, run := range composeRuns(p) {
		out, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		hi, err := out.Ranking[0].Axes["hi"].(json.Number).Float64()
		if err != nil {
			t.Fatalf("%s: winning axes %v", name, out.Ranking[0].Axes)
		}
		v := oneOptimum(t, name, out).Variables[0]
		if math.Abs(v.Value-hi/10) > 1e-12 || v.Active != types.OptimizeActiveUpper {
			t.Errorf("%s: v %+v, want %v at its upper bound", name, v, hi/10)
		}
	}
}
