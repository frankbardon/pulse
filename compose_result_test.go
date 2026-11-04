package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/optfixture"
	"github.com/frankbardon/pulse/types"
)

// compose_result_test.go pins result(slot, aggregation) (library-gaps
// E5-S1, G7) at the facade: an optimize block reads an ungrouped
// aggregation of a Compose slot; the slot runs once, in a pre-phase
// before the blocks, and keeps its place in `responses`; predict judges
// the slot dependency statically and lists the bound unresolved; every
// static refusal is predict ≡ runtime, and a null value is refused at
// run time.

// resultBatch is the saved-model price block over the fixture, its
// "weeks" slot carrying AGG_COUNT n (80 rows) and AGG_MAX top_price
// (15), plus a second ungrouped slot "after" no block reads, and the
// price variable's upper bound set to upper.
func resultBatch(t *testing.T, upper string) *ComposedRequest {
	t.Helper()
	return decodeOpt(t, optfixture.PriceModel, func(c *ComposedRequest) {
		c.Requests[0].Aggregations = append(c.Requests[0].Aggregations,
			&types.Aggregation{Type: types.AGG_MAX, Field: "price", Label: "top_price"})
		c.Requests = append(c.Requests, &types.Request{Label: "after", Cohort: &types.Cohort{Filename: optfixture.CohortFile},
			Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "units", Label: "mean_units"}}})
		c.Optimize[0].Variables[0].Upper = types.BoundExpr(upper)
	})
}

// TestComposeResult_BoundFromData: the upper bound result("weeks", "n")
// / 8 = 10 caps the price below its optimum 17.5 on both orchestrators;
// predict lists it unresolved, reading result; every slot answers once,
// in index order.
func TestComposeResult_BoundFromData(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	upper := `result("weeks", "n") / 8`
	c := resultBatch(t, upper)
	want := []descriptor.UnresolvedScalar{{Path: "optimize[0].variables[0].upper", Expression: upper, Reads: []string{"result"}}}
	if got := predictUnresolved(t, p, c); !reflect.DeepEqual(got, want) {
		t.Fatalf("predict unresolved_scalars = %+v, want %+v", got, want)
	}
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		v := oneOptimum(t, name, resp).Variables[0]
		if math.Abs(v.Value-10) > 1e-9 || v.Active != types.OptimizeActiveUpper {
			t.Errorf("%s: price %+v, want 10 on its upper bound", name, v)
		}
		if len(resp.Responses) != 2 || resp.Responses[0].Label != "weeks" || resp.Responses[1].Label != "after" {
			t.Fatalf("%s: responses %+v", name, resp.Responses)
		}
		if n, ok := toFloat(resp.Responses[0].Data[0]["n"]); !ok || n != float64(optfixture.Weeks) {
			t.Errorf("%s: weeks.n = %v", name, n)
		}
	}
}

// TestComposeResult_ObjectiveAndConstraint: result() also reads in a
// start, the objective and a constraint — the constraint
// result("weeks", "top_price") - p >= 0 caps the price at the cohort's
// top price, 15.
func TestComposeResult_ObjectiveAndConstraint(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	c := resultBatch(t, "30")
	c.Optimize[0].Variables[0].Start = types.BoundExpr(`result("weeks", "top_price") - 1`)
	c.Optimize[0].Constraints = []types.OptimizeConstraint{{Expr: `result("weeks", "top_price") - p`, Type: types.OptimizeConstraintIneq}}
	c.Optimize[0].Objective = `(p - params.cost) * (coef("demand", "(intercept)") + coef("demand", "price") * p) + 0 * result("after", "mean_units")`
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v := oneOptimum(t, name, resp).Variables[0]; math.Abs(v.Value-15) > 1e-6 {
			t.Errorf("%s: price %+v, want the top price 15", name, v)
		}
		if len(resp.Responses) != 2 {
			t.Fatalf("%s: %d responses", name, len(resp.Responses))
		}
	}
}

// TestComposeResult_RefusalParity: each static refusal — and a result()
// read out of scope, in a slot's AGG_CONSTANT expr or a fit request's
// window param — is predict ≡ both orchestrators: code, message and
// details.
func TestComposeResult_RefusalParity(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	grouped := func(c *ComposedRequest) {
		c.Requests[1].Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "week"}}
	}
	cases := []struct {
		name   string
		upper  string
		edit   func(*ComposedRequest)
		code   perr.Code
		reason string
	}{
		{"unknown slot", `result("nope", "n")`, nil, perr.PULSE_RESULT_INVALID, "slot_unknown"},
		{"unknown aggregation", `result("weeks", "radio")`, nil, perr.PULSE_RESULT_INVALID, "aggregation_unknown"},
		{"grouped slot", `result("after", "mean_units")`, grouped, perr.PULSE_RESULT_INVALID, "grouped"},
		{"computed argument", `result("weeks", "n" + "")`, nil, perr.PULSE_RESULT_INVALID, "args_not_literal"},
		{"default label", `result("request_3", "n")`, nil, perr.PULSE_RESULT_INVALID, "slot_unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := resultBatch(t, tc.upper)
			if tc.edit != nil {
				tc.edit(c)
			}
			env, err := p.PredictCompose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) || env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict errors %+v, want %s %s", env.Errors, tc.code, tc.reason)
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
}

// TestComposeResult_NotNumericRefusedStatically: reading an
// aggregation that writes no single number — a map-valued AGG_WELFORD,
// a label AGG_CONSTANT — is refused before any slot runs, predict ≡
// both orchestrators (fail-fast and not): PULSE_RESULT_INVALID reason
// not_numeric, same message and details, no unresolved bound.
func TestComposeResult_NotNumericRefusedStatically(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	cases := map[string]*types.Aggregation{
		"map valued":     {Type: types.AGG_WELFORD, Field: "units", Label: "moments"},
		"label constant": {Type: types.AGG_CONSTANT, Label: "moments", Params: json.RawMessage(`{"type":"categorical_u8","value":"hi"}`)},
	}
	for name, agg := range cases {
		t.Run(name, func(t *testing.T) {
			c := resultBatch(t, `result("after", "moments")`)
			c.Requests[1].Aggregations = append(c.Requests[1].Aggregations, agg)
			env, err := p.PredictCompose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_RESULT_INVALID) {
				t.Fatalf("predict errors %+v, want PULSE_RESULT_INVALID", env.Errors)
			}
			if d := env.Errors[0].Details; d["reason"] != "not_numeric" || d["slot"] != "after" || d["aggregation"] != "moments" || d["field"] != "optimize[0].variables[0].upper" {
				t.Fatalf("predict details %v", d)
			}
			runs := composeRuns(p)
			for k, v := range failFastRuns(p) {
				runs[k+"/fail-fast"] = v
			}
			for run, fn := range runs {
				resp, rerr := fn(c)
				ce := requireCoded(t, rerr, perr.PULSE_RESULT_INVALID)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", run, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}

// TestComposeResult_OutOfScope: result() is read only by an optimize
// block. A slot's AGG_CONSTANT expr, a slot's window param and a fit
// request's window param each refuse it — PULSE_RESULT_INVALID reason
// out_of_scope, at predict and on both orchestrators (the scalar slot
// locates it under its own prefix, so code and reason are compared) —
// and a slot's ATTR_FORMULA, which declares no result(), fails to
// compile (PULSE_FORMULA_COMPILE_ERROR) on every side.
func TestComposeResult_OutOfScope(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	read := `result(\"weeks\", \"n\")`
	window := func(r *Request) {
		r.Windows = []*types.Window{{Type: types.WIN_EWMA, Field: "units", Label: "u_ew", OrderBy: []types.OrderKey{{Field: "week"}},
			Params: json.RawMessage(`{"alpha":"` + read + ` / 100"}`)}}
	}
	cases := []struct {
		name, reason string
		edit         func(*ComposedRequest)
	}{
		{"constant expr", "out_of_scope", func(c *ComposedRequest) {
			c.Requests[1].Aggregations = append(c.Requests[1].Aggregations, &types.Aggregation{Type: types.AGG_CONSTANT, Label: "k",
				Params: json.RawMessage(`{"type":"f64","expr":"` + read + `"}`)})
		}},
		{"slot window param", "out_of_scope", func(c *ComposedRequest) {
			r := &Request{Label: "smoothed", Cohort: &types.Cohort{Filename: optfixture.CohortFile},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "u_ew", Label: "s"}}}
			window(r)
			c.Requests = append(c.Requests, r)
		}},
		{"fit request window param", "out_of_scope", func(c *ComposedRequest) {
			fit := &Request{Cohort: &types.Cohort{Filename: optfixture.CohortFile},
				Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "units", Predictors: []string{"price"}}}}
			window(fit)
			c.Fits = []types.FitSpec{{Name: "f", Request: fit}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := resultBatch(t, "30")
			tc.edit(c)
			env, err := p.PredictCompose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_RESULT_INVALID) || env.Errors[0].Details["reason"] != tc.reason {
				for _, e := range env.Errors {
					t.Logf("predict: %s %s %v", e.Code, e.Message, e.Details)
				}
				t.Fatalf("predict errors, want PULSE_RESULT_INVALID %s", tc.reason)
			}
			for name, run := range failFastRuns(p) {
				resp, rerr := run(c)
				ce := requireCoded(t, rerr, perr.PULSE_RESULT_INVALID)
				if resp != nil || ce.Details["reason"] != tc.reason {
					t.Fatalf("%s: %v %+v", name, ce, ce.Details)
				}
			}
		})
	}

	c := resultBatch(t, "30")
	c.Requests[1].Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "r", Expression: strings.ReplaceAll(read, `\"`, `"`) + " + units"}}
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil || len(env.Errors) == 0 {
		t.Fatalf("predict accepted a formula reading result(): %v", err)
	}
	for name, run := range failFastRuns(p) {
		_, rerr := run(c)
		if ce := requireCoded(t, rerr, perr.PULSE_FORMULA_COMPILE_ERROR); env.Errors[0].Code != string(ce.Code) {
			t.Fatalf("%s: predict %s, runtime %s", name, env.Errors[0].Code, ce.Code)
		}
	}
}

// TestComposeResult_NullRefusedAtRuntime: a slot whose filter keeps no
// row has a null average; predict accepts the bound (unresolved), both
// orchestrators refuse it — PULSE_RESULT_INVALID reason null, located
// at the bound.
func TestComposeResult_NullRefusedAtRuntime(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	c := resultBatch(t, `result("after", "mean_units")`)
	c.Requests[1].Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{"1000", "2000"}}}
	if got := predictUnresolved(t, p, c); len(got) != 1 {
		t.Fatalf("predict unresolved_scalars = %+v", got)
	}
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		ce := requireCoded(t, err, perr.PULSE_RESULT_INVALID)
		if resp != nil || ce.Details["reason"] != "null" || ce.Details["slot"] != "after" || ce.Details["field"] != "optimize[0].variables[0].upper" {
			t.Fatalf("%s: %v %+v", name, ce, ce.Details)
		}
	}
}

// TestComposeResult_ReservedName: an embedder function named result is
// refused at New, and a decision variable may not take the name.
func TestComposeResult_ReservedName(t *testing.T) {
	t.Parallel()

	_, err := New(Options{FS: optFixtureFS(t), Extensions: Extensions{ExprFunctions: []ExprFunction{{Name: "result", Fn: func(args ...any) (any, error) { return 1.0, nil }}}}})
	requireCoded(t, err, perr.PULSE_EXTENSION_NAME_RESERVED)

	p := optPulse(t, Options{})
	c := resultBatch(t, "30")
	c.Optimize[0].Variables[0].Name = "result"
	c.Optimize[0].Objective = "result"
	env, perrPredict := p.PredictCompose(context.Background(), c)
	if perrPredict != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OPTIMIZE_VARIABLE_INVALID) || env.Errors[0].Details["reason"] != "name_reserved" {
		t.Fatalf("decision variable named result: %+v %v", env.Errors, perrPredict)
	}
}

// failFastRuns are both orchestrators, the parallel one failing fast,
// so a slot's own refusal comes back with its code (a non-fail-fast
// pool aggregates slot failures).
func failFastRuns(p *Pulse) map[string]func(*ComposedRequest) (*ComposedResponse, error) {
	ctx := context.Background()
	return map[string]func(*ComposedRequest) (*ComposedResponse, error){
		"Compose": func(c *ComposedRequest) (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(c *ComposedRequest) (*ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 4, FailFast: true})
		},
	}
}
