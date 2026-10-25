package optsolve

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/types"
)

// block decodes one OptimizeSpec.
func block(t *testing.T, raw string) *types.OptimizeSpec {
	t.Helper()
	var s types.OptimizeSpec
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

// solve compiles spec over params and bound, then solves it with the
// spec's effective seed.
func solve(t *testing.T, spec *types.OptimizeSpec, params map[string]any, env optblock.Env, bound exprenv.Bound) (*Outcome, error) {
	t.Helper()
	if params != nil {
		p, cerr := exprenv.NormalizeParams(params)
		if cerr != nil {
			t.Fatal(cerr)
		}
		env.Params = p
	}
	c, cerr := optblock.Compile(spec, 0, env, bound)
	if cerr != nil {
		t.Fatal(cerr)
	}
	return Solve(spec, 0, c, optblock.EffectiveSeed(spec))
}

func mustSolve(t *testing.T, spec *types.OptimizeSpec, params map[string]any) *types.OptimizeResult {
	t.Helper()
	out, err := solve(t, spec, params, optblock.Env{}, exprenv.Bound{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Failed {
		t.Fatalf("block failed: %+v", out.Result.Error)
	}
	return &out.Result
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestOptimize_SolveQuadraticBoundAndMarginal: an interior minimum is
// reached with a zero marginal; a minimum outside the bounds stops on
// the bound, reports it active and reads the one-sided marginal there
// (2·(0 + 1) for (x + 1)² at x = 0).
func TestOptimize_SolveQuadraticBoundAndMarginal(t *testing.T) {
	r := mustSolve(t, block(t, `{"name":"q","direction":"minimize","variables":[{"name":"x","lower":-10,"upper":10},{"name":"y","lower":0,"upper":5}],
		"objective":"(x - 3) * (x - 3) + (y + 1) * (y + 1)","starts":3,"rng_seed":5}`), nil)
	x, y := r.Variables[0], r.Variables[1]
	if !near(x.Value, 3, 1e-6) || x.Active != "" || !near(x.Marginal, 0, 1e-6) {
		t.Errorf("x = %+v, want 3, free, marginal 0", x)
	}
	if y.Value != 0 || y.Active != types.OptimizeActiveLower || !near(y.Marginal, 2, 1e-6) {
		t.Errorf("y = %+v, want 0 at its lower bound, marginal 2", y)
	}
	if !near(r.Objective, 1, 1e-9) || !r.Converged || r.Starts != 3 || r.StartsAgreeing != 3 || r.RNGSeed != 5 {
		t.Errorf("result %+v", r)
	}
}

// TestOptimize_MultiplierDirection: the multiplier is reported in the
// objective's own direction — ∇f = μ∇Expr — for maximize and minimize:
// a budget eq constraint's multiplier equals every free variable's
// marginal, and a binding ineq is ≥ 0 under minimize, ≤ 0 under
// maximize.
func TestOptimize_MultiplierDirection(t *testing.T) {
	// max 4·sqrt(x) + 2·sqrt(y), x + y = 10: x* = 8, y* = 2, μ = 2/sqrt(8).
	r := mustSolve(t, block(t, `{"name":"b","direction":"maximize","variables":[{"name":"x","lower":0,"upper":10},{"name":"y","lower":0,"upper":10}],
		"objective":"4 * sqrt(x) + 2 * sqrt(y)","constraints":[{"expr":"x + y - params.budget","type":"eq"}],"starts":2,"tolerance":1e-12}`),
		map[string]any{"budget": 10})
	mu := r.Constraints[0].Multiplier
	// The stop is on the objective change, so x* is good to about
	// sqrt(tolerance) on this flat optimum (at the default 1e-6: off by
	// 9e-4, the marginals by 1.6e-4); at 1e-12 it is good to 1e-6.
	if want := 2 / math.Sqrt(8); !near(mu, want, 1e-6) || !near(r.Variables[0].Value, 8, 1e-5) {
		t.Fatalf("multiplier %v (want %v), x %v", mu, want, r.Variables[0].Value)
	}
	for _, v := range r.Variables {
		if !near(v.Marginal, mu, 1e-6) {
			t.Errorf("%s marginal %v differs from the multiplier %v", v.Name, v.Marginal, mu)
		}
	}
	// min (x - 5)², x <= 2 as 2 - x >= 0: binding, μ = ∂f/∂x / ∂(2-x)/∂x = -6 / -1 = 6 >= 0.
	r = mustSolve(t, block(t, `{"name":"m","direction":"minimize","variables":[{"name":"x","lower":-10,"upper":10}],
		"objective":"(x - 5) * (x - 5)","constraints":[{"expr":"2 - x","type":"ineq"}],"starts":1}`), nil)
	if c := r.Constraints[0]; !near(c.Multiplier, 6, 1e-4) || !near(c.Value, 0, 1e-6) {
		t.Errorf("minimize ineq %+v, want multiplier 6 at value 0", c)
	}
	// max -(x - 5)², the same constraint: μ = ∂f/∂x / -1 = 6 / -1 = -6 <= 0.
	r = mustSolve(t, block(t, `{"name":"m","direction":"maximize","variables":[{"name":"x","lower":-10,"upper":10}],
		"objective":"-(x - 5) * (x - 5)","constraints":[{"expr":"2 - x","type":"ineq"}],"starts":1}`), nil)
	if c := r.Constraints[0]; !near(c.Multiplier, -6, 1e-4) {
		t.Errorf("maximize ineq %+v, want multiplier -6", c)
	}
	// A slack ineq carries no price.
	r = mustSolve(t, block(t, `{"name":"m","direction":"minimize","variables":[{"name":"x","lower":-10,"upper":10}],
		"objective":"(x - 1) * (x - 1)","constraints":[{"expr":"2 - x","type":"ineq"}],"starts":1}`), nil)
	if c := r.Constraints[0]; c.Multiplier != 0 {
		t.Errorf("slack ineq %+v, want multiplier 0", c)
	}
}

// TestOptimize_StartsAgreeing: a two-well objective solved from many
// starts reports every start, and only the ones that found the deeper
// well agree; the best is the deeper well.
func TestOptimize_StartsAgreeing(t *testing.T) {
	// (x² - 1)² + 0.3·x: wells near -1 (deeper) and +1.
	r := mustSolve(t, block(t, `{"name":"w","direction":"minimize","variables":[{"name":"x","lower":-2,"upper":2,"start":1}],
		"objective":"(x * x - 1) * (x * x - 1) + 0.3 * x","starts":12,"rng_seed":3}`), nil)
	if r.Variables[0].Value > -0.9 {
		t.Fatalf("best x %v: want the deeper well near -1", r.Variables[0].Value)
	}
	if r.Starts != 12 || r.StartsAgreeing < 1 || r.StartsAgreeing >= 12 {
		t.Fatalf("starts %d agreeing %d: want some, not all, starts in the deeper well", r.Starts, r.StartsAgreeing)
	}
	// One start from the shallow well alone agrees with itself.
	r = mustSolve(t, block(t, `{"name":"w","direction":"minimize","variables":[{"name":"x","lower":-2,"upper":2,"start":1}],
		"objective":"(x * x - 1) * (x * x - 1) + 0.3 * x","starts":1}`), nil)
	if r.Variables[0].Value < 0.9 || r.StartsAgreeing != 1 {
		t.Fatalf("one start: %+v", r)
	}
}

// TestOptimize_SeedDeterminism: the same block twice is identical bits;
// another seed draws other starts (the iterate path differs) yet the
// same optimum.
func TestOptimize_SeedDeterminism(t *testing.T) {
	raw := `{"name":"w","direction":"minimize","variables":[{"name":"x","lower":-2,"upper":2},{"name":"y","lower":-2,"upper":2}],
		"objective":"(x * x - 1) * (x * x - 1) + (y - 0.5) * (y - 0.5) + 0.3 * x","starts":6,"rng_seed":SEED}`
	enc := func(seed string) []byte {
		r := mustSolve(t, block(t, strings.ReplaceAll(raw, "SEED", seed)), nil)
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if a, b := enc("9"), enc("9"); string(a) != string(b) {
		t.Fatalf("same seed differs:\n%s\n%s", a, b)
	}
	s9, s10 := startPoints(6, compiled(t, strings.ReplaceAll(raw, "SEED", "9")), 9), startPoints(6, compiled(t, strings.ReplaceAll(raw, "SEED", "10")), 10)
	if reflect.DeepEqual(s9[1:], s10[1:]) || !reflect.DeepEqual(s9[0], s10[0]) {
		t.Fatalf("starts 9 %v, 10 %v: want the same first point and other drawn points", s9, s10)
	}
}

// TestOptimize_StartPointsInBounds: the first start is the declared
// start (else the midpoint); the drawn starts lie inside the bounds and
// cover each dimension's strata once; a fixed variable stays fixed.
func TestOptimize_StartPointsInBounds(t *testing.T) {
	c := compiled(t, `{"name":"s","direction":"minimize","variables":[{"name":"x","lower":2,"upper":6},{"name":"y","lower":-1,"upper":-1},{"name":"z","lower":0,"upper":10,"start":7}],"objective":"x + y + z"}`)
	pts := startPoints(9, c, 4)
	if len(pts) != 9 || !reflect.DeepEqual(pts[0], []float64{4, -1, 7}) {
		t.Fatalf("starts %v", pts)
	}
	strata := map[int]bool{}
	for _, p := range pts[1:] {
		if p[0] < 2 || p[0] > 6 || p[1] != -1 || p[2] < 0 || p[2] > 10 {
			t.Fatalf("start %v outside the bounds", p)
		}
		strata[int((p[0]-2)/4*8)] = true
	}
	if len(strata) != 8 {
		t.Fatalf("x strata %v: want each of 8 drawn once", strata)
	}
}

// TestOptimize_FailsOnItsNumbers: infeasible constraints and a capped
// iteration count fail the block alone — Failed, the solver's own
// code, the block and start located, every figure absent on the wire.
func TestOptimize_FailsOnItsNumbers(t *testing.T) {
	cases := []struct {
		name, raw string
		code      errors.Code
	}{
		{"infeasible", `{"name":"f","direction":"minimize","variables":[{"name":"x","lower":0,"upper":10}],"objective":"x * x",
			"constraints":[{"expr":"x - 5","type":"ineq"},{"expr":"2 - x","type":"ineq"}],"starts":3}`, errors.PULSE_OPTIMIZE_INFEASIBLE},
		{"not converged", `{"name":"f","direction":"minimize","variables":[{"name":"x","lower":-3,"upper":3},{"name":"y","lower":-3,"upper":3}],
			"objective":"(1 - x) * (1 - x) + 100 * (y - x * x) * (y - x * x)","max_iterations":1,"starts":2}`, errors.PULSE_OPTIMIZE_NOT_CONVERGED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := solve(t, block(t, tc.raw), nil, optblock.Env{}, exprenv.Bound{})
			if err != nil {
				t.Fatal(err)
			}
			se := out.Result.Error
			if !out.Failed || se == nil || se.Code != string(tc.code) || se.Details["optimize"] != 0 || se.Details["starts"] == nil || se.Details["start"] == nil {
				t.Fatalf("outcome %+v (error %+v), want a %s failure", out, se, tc.code)
			}
			wire, err := json.Marshal(out.Result)
			if err != nil {
				t.Fatal(err)
			}
			var keys map[string]any
			_ = json.Unmarshal(wire, &keys)
			if len(keys) != 3 || keys["name"] != "f" || keys["error"] == nil {
				t.Fatalf("wire %s: want {name, direction, error}", wire)
			}
		})
	}
}

// TestOptimize_FailedFitFailsAlone: a block reading a Compose fit that
// failed on its data fails alone with the fit's own code; any other
// evaluation fault is fatal, located at the block.
func TestOptimize_FailedFitFailsAlone(t *testing.T) {
	set := &model.Set{}
	se := &types.SlotError{Code: string(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA), Message: "too few rows"}
	if err := set.AddFailed(model.Signature{Name: "m", Kind: types.FitKindRegression, Type: types.REG_OLS, Terms: []string{"x"}}, se); err != nil {
		t.Fatal(err)
	}
	cat := model.FitCatalog{"m": {Name: "m", Kind: types.FitKindRegression, Type: types.REG_OLS, Terms: []string{"x"}}}
	bound := exprenv.Bound{Coef: func(args ...any) (any, error) {
		v, cerr := model.Coef(set, args[0].(string), args[1].(string), model.StatPoint)
		if cerr != nil {
			return nil, cerr
		}
		return v, nil
	}}
	spec := block(t, `{"name":"p","direction":"maximize","variables":[{"name":"q","lower":0,"upper":4}],"objective":"coef(\"m\", \"x\") * q"}`)
	out, err := solve(t, spec, nil, optblock.Env{Fits: cat}, bound)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Failed || out.Result.Error == nil || out.Result.Error.Code != se.Code || out.Result.Error.Details["fit"] != "m" || out.Result.Error.Details["optimize"] != 0 {
		t.Fatalf("outcome %+v / %+v: want the fit's own code", out, out.Result.Error)
	}

	boom := exprenv.Bound{Extensions: []exprenv.NamedFunc{{Name: "boom", Fn: func(args ...any) (any, error) {
		return nil, stderrors.New("no such rate")
	}}}}
	spec = block(t, `{"name":"p","direction":"maximize","variables":[{"name":"q","lower":0,"upper":4}],"objective":"boom(q)"}`)
	_, err = solve(t, spec, nil, optblock.Env{Extensions: []string{"boom"}}, boom)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_RUNTIME || ce.Details["optimize"] != 0 || ce.Details["field"] != "optimize[0].objective" {
		t.Fatalf("fatal fault: %v (%+v)", err, ce)
	}
}

// compiled compiles one block with no params.
func compiled(t *testing.T, raw string) *optblock.Compiled {
	t.Helper()
	c, cerr := optblock.Compile(block(t, raw), 0, optblock.Env{}, exprenv.Bound{})
	if cerr != nil {
		t.Fatal(cerr)
	}
	return c
}
