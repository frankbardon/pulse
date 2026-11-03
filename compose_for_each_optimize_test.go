package pulse

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compose_for_each_optimize_test.go pins the optimize-block fan-out
// (library-gaps E7-S2, G4): a `for_each` on a Compose `optimize` block
// expands at the top of the Compose expansion, right after the base
// merge, so the optimize rules, the result() catalog and the slot
// limit all judge the expanded blocks — on predict and both
// orchestrators.

// forEachOptBody states two near-identical budget blocks — one with
// social spend capped by a param, one with a looser numeric cap and a
// tighter budget — ONCE: the name carries the axis, a bound varies as a
// whole {"$var"} value (a number or an expression) and the budget
// constraint as a whole expression.
const forEachOptBody = `{
  "requests": [{"label": "weeks", "cohort": {"filename": "opt_price.pulse"}, "aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}]}],
  "params": {"budget": 120, "cap_social": 30},
  "optimize": [{
    "name": "allocation_{{plan}}",
    "direction": "maximize",
    "variables": [
      {"name": "tv", "lower": 0, "upper": 100},
      {"name": "search", "lower": 0, "upper": 100},
      {"name": "social", "lower": 0, "upper": {"$var": "cap"}},
      {"name": "display", "lower": 0, "upper": 100}
    ],
    "objective": "120 * hill(tv, 40, 2) + 60 * hill(search, 10, 1) + 150 * hill(social, 60, 1.5) + 30 * hill(display, 50, 1)",
    "constraints": [{"expr": {"$var": "con"}, "type": "eq"}],
    "starts": 4,
    "for_each": {"mode": "zip", "axes": [
      {"name": "plan", "values": ["capped", "lean"]},
      {"name": "cap", "values": ["params.cap_social", 60]},
      {"name": "con", "values": ["tv + search + social + display - params.budget", "tv + search + social + display - 90"]}
    ]}
  }]
}`

// forEachOptTwin is forEachOptBody with the two blocks written by hand.
const forEachOptTwin = `{
  "requests": [{"label": "weeks", "cohort": {"filename": "opt_price.pulse"}, "aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}]}],
  "params": {"budget": 120, "cap_social": 30},
  "optimize": [{
    "name": "allocation_capped",
    "direction": "maximize",
    "variables": [
      {"name": "tv", "lower": 0, "upper": 100},
      {"name": "search", "lower": 0, "upper": 100},
      {"name": "social", "lower": 0, "upper": "params.cap_social"},
      {"name": "display", "lower": 0, "upper": 100}
    ],
    "objective": "120 * hill(tv, 40, 2) + 60 * hill(search, 10, 1) + 150 * hill(social, 60, 1.5) + 30 * hill(display, 50, 1)",
    "constraints": [{"expr": "tv + search + social + display - params.budget", "type": "eq"}],
    "starts": 4
  }, {
    "name": "allocation_lean",
    "direction": "maximize",
    "variables": [
      {"name": "tv", "lower": 0, "upper": 100},
      {"name": "search", "lower": 0, "upper": 100},
      {"name": "social", "lower": 0, "upper": 60},
      {"name": "display", "lower": 0, "upper": 100}
    ],
    "objective": "120 * hill(tv, 40, 2) + 60 * hill(search, 10, 1) + 150 * hill(social, 60, 1.5) + 30 * hill(display, 50, 1)",
    "constraints": [{"expr": "tv + search + social + display - 90", "type": "eq"}],
    "starts": 4
  }]
}`

// TestComposeForEachOptimize_MatchesHandWrittenTwin: the fanned-out
// block solves exactly as its hand-written twin on both orchestrators
// (the derived seed is the expanded block's own, so the starts match),
// the echoed batch carries the expanded blocks and no for_each, and
// predict judges the same blocks — its optimize summary equals the
// twin's, and its for_each summary names the block.
func TestComposeForEachOptimize_MatchesHandWrittenTwin(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{EchoRequest: true})
	for name, run := range composeRuns(p) {
		got, err := run(decodeComposedJSON(t, forEachOptBody))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, err := run(decodeComposedJSON(t, forEachOptTwin))
		if err != nil {
			t.Fatalf("%s twin: %v", name, err)
		}
		if g, w := mustJSON(t, "opt", got.Optimizations), mustJSON(t, "opt", want.Optimizations); g != w {
			t.Fatalf("%s: optimizations differ from the twin\ngot:  %s\nwant: %s", name, g, w)
		}
		if len(got.Optimizations) != 2 || got.Optimizations[1].Name != "allocation_lean" {
			t.Fatalf("%s: optimizations %+v", name, got.Optimizations)
		}
		nr := got.NormalizedRequest
		if nr == nil || len(nr.Optimize) != 2 || nr.Optimize[0].ForEach != nil || nr.Optimize[1].Name != "allocation_lean" {
			t.Fatalf("%s: normalized request %s, want the expanded blocks", name, mustJSON(t, "opt", nr))
		}
	}

	ctx := context.Background()
	env, err := p.PredictCompose(ctx, decodeComposedJSON(t, forEachOptBody))
	if err != nil {
		t.Fatal(err)
	}
	twin, err := p.PredictCompose(ctx, decodeComposedJSON(t, forEachOptTwin))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 || len(twin.Errors) != 0 {
		t.Fatalf("predict refused: %+v / %+v", env.Errors, twin.Errors)
	}
	res, tres := env.Data.(*ComposePredictResult), twin.Data.(*ComposePredictResult)
	if !reflect.DeepEqual(res.Optimize, tres.Optimize) {
		t.Fatalf("predict optimize %+v, twin %+v", res.Optimize, tres.Optimize)
	}
	wantFE := []descriptor.ForEachSummary{{Path: "optimize[0]", Mode: types.SweepModeZip, ExpandedCount: 2,
		Axes: []descriptor.SweepAxisSummary{{Name: "plan", Count: 2}, {Name: "cap", Count: 2}, {Name: "con", Count: 2}}}}
	if !reflect.DeepEqual(res.ForEach, wantFE) {
		t.Fatalf("predict for_each %+v, want %+v", res.ForEach, wantFE)
	}
	if tres.ForEach != nil {
		t.Fatalf("twin predict for_each %+v, want none", tres.ForEach)
	}
	// The request echo keeps the batch as written.
	if res.Request == nil || len(res.Request.Optimize) != 1 || res.Request.Optimize[0].ForEach == nil {
		t.Fatalf("predict request echo %s, want the block as written", mustJSON(t, "opt", res.Request))
	}
}

// TestComposeForEachOptimize_SlotSummaries: the Compose predict result
// lists every fan-out the batch expands — the optimize blocks', then
// each slot's item fan-outs located requests[i]., then each fit
// request's located fits[i].request. — in PredictResult.ForEach's shape.
func TestComposeForEachOptimize_SlotSummaries(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	c := decodeComposedJSON(t, `{
	  "requests": [
	    {"label": "a", "cohort": {"filename": "opt_price.pulse"}, "aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}]},
	    {"label": "b", "cohort": {"filename": "opt_price.pulse"}, "aggregations": [{"type": "AGG_MAX", "field": "{{f}}", "label": "top_{{f}}",
	      "for_each": {"axes": [{"name": "f", "values": ["price", "units"]}]}}]}
	  ],
	  "fits": [{"name": "m", "request": {"cohort": {"filename": "opt_price.pulse"},
	    "windows": [{"type": "WIN_LAG", "field": "price", "label": "price_l{{k}}", "params": {"offset": {"$var": "k"}}, "order_by": [{"field": "week"}],
	      "for_each": {"axes": [{"name": "k", "values": [1, 2, 3]}]}}],
	    "regressions": [{"type": "REG_OLS", "target": "units", "predictors": ["price"]}]}}],
	  "optimize": [{"name": "o_{{v}}", "direction": "minimize", "variables": [{"name": "x", "lower": 0, "upper": {"$var": "v"}}],
	    "objective": "(x - 3) * (x - 3)", "for_each": {"axes": [{"name": "v", "values": [5, 10]}]}}]
	}`)
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict refused: %+v", env.Errors)
	}
	var got []string
	for _, f := range env.Data.(*ComposePredictResult).ForEach {
		got = append(got, f.Path)
	}
	if want := []string{"optimize[0]", "requests[1].aggregations[0]", "fits[0].request.windows[0]"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("for_each paths %v, want %v", got, want)
	}
}

// TestComposeForEachOptimize_RefusalParity: every fault of an optimize
// block's for_each — and the rules the expanded blocks then meet — is
// refused with the same code, message and details by predict and both
// orchestrators, before any fit or slot runs.
func TestComposeForEachOptimize_RefusalParity(t *testing.T) {
	t.Parallel()

	block := func(name, upper, objective, axes string) string {
		return `{"requests":[{"label":"weeks","cohort":{"filename":"opt_price.pulse"},"aggregations":[{"type":"AGG_COUNT","field":"week","label":"n"}]}],
			"params":{"hi":10},
			"optimize":[{"name":` + name + `,"direction":"minimize","variables":[{"name":"x","lower":0,"upper":` + upper + `}],
			"objective":` + objective + `,"for_each":{"axes":` + axes + `}}]}`
	}
	v := `[{"name":"v","values":[5,10]}]`
	all, err := InitFeatureProfile("")
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	for _, f := range all.Features {
		if f != "capability:for_each" {
			keep = append(keep, f)
		}
	}
	all.Features = keep

	cases := []struct {
		name   string
		opts   Options
		body   string
		code   perr.Code
		reason string
		field  string
	}{
		{"name without axis", Options{}, block(`"o"`, `{"$var":"v"}`, `"x * x"`, v),
			perr.PULSE_FOR_EACH_INVALID, "name_without_axis", "optimize[0].name"},
		{"axis in objective", Options{}, block(`"o_{{v}}"`, `10`, `"(x - {{v}}) * (x - {{v}})"`, v),
			perr.PULSE_FOR_EACH_EXPR_SPLICE, "", "optimize[0].objective"},
		{"axis in bound expression", Options{}, block(`"o_{{v}}"`, `"params.hi * {{v}}"`, `"x * x"`, v),
			perr.PULSE_FOR_EACH_EXPR_SPLICE, "", "optimize[0].variables[0].upper"},
		{"axis unreferenced", Options{}, block(`"o_{{v}}"`, `10`, `"x * x"`, `[{"name":"v","values":[5,10]},{"name":"w","values":[1]}]`),
			perr.PULSE_FOR_EACH_INVALID, "axis_unreferenced", ""},
		{"value does not decode", Options{}, block(`"o_{{v}}"`, `{"$var":"v"}`, `"x * x"`, `[{"name":"v","values":[true,false]}]`),
			perr.PULSE_FOR_EACH_INVALID, "item_decode", "optimize[0]"},
		// The expanded blocks meet the optimize rules: a name naming one
		// axis of a grid of two repeats, refused as a duplicate block.
		{"expanded names collide", Options{}, block(`"o_{{v}}"`, `{"$var":"w"}`, `"x * x"`, `[{"name":"v","values":[5]},{"name":"w","values":[1,2]}]`),
			perr.PULSE_OPTIMIZE_SPEC_INVALID, "", ""},
		{"over MaxExpandedItems", Options{Limits: Limits{MaxExpandedItems: 1}}, block(`"o_{{v}}"`, `{"$var":"v"}`, `"x * x"`, v),
			perr.PULSE_LIMIT_EXCEEDED, "", ""},
		// No params, base, sweep or banner: only the block's own
		// for_each opens the pre-expansion gate.
		{"hidden capability:for_each", Options{FeatureProfile: all},
			strings.Replace(block(`"o_{{v}}"`, `{"$var":"v"}`, `"x * x"`, v), `"params":{"hi":10},`, "", 1),
			perr.PULSE_REQUEST_UNKNOWN_FIELD, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := optPulse(t, tc.opts)
			c := decodeComposedJSON(t, tc.body)
			env, err := p.PredictCompose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) {
				t.Fatalf("predict errors %+v, want %s", env.Errors, tc.code)
			}
			d := env.Errors[0].Details
			if tc.reason != "" && d["reason"] != tc.reason {
				t.Fatalf("predict details %v, want reason %s", d, tc.reason)
			}
			if tc.field != "" && d["field"] != tc.field {
				t.Fatalf("predict details %v, want field %s", d, tc.field)
			}
			if tc.code == perr.PULSE_LIMIT_EXCEEDED {
				res := env.Data.(*ComposePredictResult)
				if len(res.LimitFindings) != 1 || res.LimitFindings[0].Limit != "max_expanded_items" {
					t.Fatalf("predict limit findings %+v", res.LimitFindings)
				}
			}
			if tc.code == perr.PULSE_REQUEST_UNKNOWN_FIELD && !strings.Contains(env.Errors[0].Message, "for_each") {
				t.Fatalf("predict message %q does not name for_each", env.Errors[0].Message)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(c)
				ce := requireCoded(t, rerr, tc.code)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(d, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}

// TestComposeForEachOptimize_ResultBoundRunsInPrePhase: a fanned-out
// block whose bound is a result() call through a {"$var"} value is
// expanded before the result() catalog and the pre-phase are computed —
// predict lists the expanded block's bound as not statically resolved,
// and both orchestrators solve it against the slot's count.
func TestComposeForEachOptimize_ResultBoundRunsInPrePhase(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	body := `{
	  "requests": [{"label": "weeks", "cohort": {"filename": "opt_price.pulse"}, "aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}]}],
	  "optimize": [{"name": "cap_{{k}}", "direction": "maximize", "variables": [{"name": "x", "lower": 0, "upper": {"$var": "u"}}],
	    "objective": "x", "starts": 1,
	    "for_each": {"mode": "zip", "axes": [{"name": "k", "values": ["data", "fixed"]}, {"name": "u", "values": ["result(\"weeks\", \"n\") / 4", 7]}]}}]
	}`
	want := []descriptor.UnresolvedScalar{{Path: "optimize[0].variables[0].upper", Expression: `result("weeks", "n") / 4`, Reads: []string{"result"}}}
	if got := predictUnresolved(t, p, decodeComposedJSON(t, body)); !reflect.DeepEqual(got, want) {
		t.Fatalf("predict unresolved_scalars = %+v, want %+v", got, want)
	}
	for name, run := range composeRuns(p) {
		resp, err := run(decodeComposedJSON(t, body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(resp.Optimizations) != 2 {
			t.Fatalf("%s: optimizations %+v", name, resp.Optimizations)
		}
		for i, w := range []float64{20, 7} {
			if o := resp.Optimizations[i]; o.Error != nil || len(o.Variables) != 1 || o.Variables[0].Value < w-1e-6 || o.Variables[0].Value > w+1e-6 {
				t.Errorf("%s: block %d %+v, want x = %v", name, i, o, w)
			}
		}
	}
}

// TestComposeForEachOptimize_MarkerPositionParity: a `{"$var"}` marker
// stands only for one whole value of a fanned-out block or of one entry
// of its lists — exactly where the payload schema (and the MCP
// pulse_compose schema, TestBind_OptimizeVarMarkerPositions) admits it.
// A marker standing for a whole list, a list entry or a value inside a
// model's inline body is refused PULSE_FOR_EACH_INVALID reason
// marker_position by predict, Compose and ComposeParallel identically,
// and the payload schema rejects the same request; the admitted forms
// validate and run.
func TestComposeForEachOptimize_MarkerPositionParity(t *testing.T) {
	t.Parallel()

	block := func(variables, constraints, extra, axes string) string {
		return `{"requests":[{"label":"weeks","cohort":{"filename":"opt_price.pulse"},"aggregations":[{"type":"AGG_COUNT","field":"week","label":"n"}]}],
			"optimize":[{"name":"o_{{v}}","direction":"minimize","variables":` + variables + `,"objective":"x * x",` +
			`"constraints":` + constraints + extra + `,"for_each":{"axes":` + axes + `}}]}`
	}
	vars := `[{"name":"x","lower":0,"upper":10}]`
	cons := `[{"expr":"x - 1","type":"ineq"}]`
	vAxis := func(values string) string { return `[{"name":"v","values":` + values + `}]` }
	cases := []struct{ name, body, field string }{
		// An axis value is a scalar (a list is value_not_scalar), so a
		// list-position marker could never render a list: refused by its
		// position before anything renders.
		{"whole variables list", block(`{"$var":"v"}`, cons, "", vAxis(`["a","b"]`)), "optimize[0].variables"},
		{"whole constraints list", block(vars, `{"$var":"v"}`, "", vAxis(`["a","b"]`)), "optimize[0].constraints"},
		{"whole models list", block(vars, cons, `,"models":{"$var":"v"}`, vAxis(`["a","b"]`)), "optimize[0].models"},
		{"variables entry", block(`[{"$var":"v"}]`, cons, "", vAxis(`["a","b"]`)), "optimize[0].variables[0]"},
		{"constraints entry", block(vars, `[{"expr":"x - 1","type":"ineq"},{"$var":"v"}]`, "", vAxis(`["a","b"]`)), "optimize[0].constraints[1]"},
		{"models entry", block(vars, cons, `,"models":[{"$var":"v"}]`, vAxis(`["a","b"]`)), "optimize[0].models[0]"},
	}
	p := optPulse(t, Options{})
	validate := composeSchemaValidator(t, p)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if validate(tc.body) == nil {
				t.Fatal("the payload schema admits the marker the runtime refuses")
			}
			c := decodeComposedJSON(t, tc.body)
			env, err := p.PredictCompose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_FOR_EACH_INVALID) {
				t.Fatalf("predict errors %+v, want PULSE_FOR_EACH_INVALID", env.Errors)
			}
			d := env.Errors[0].Details
			if d["reason"] != "marker_position" || d["field"] != tc.field {
				t.Fatalf("predict details %v, want reason marker_position at %s", d, tc.field)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(c)
				ce := requireCoded(t, rerr, perr.PULSE_FOR_EACH_INVALID)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(d, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
	// The admitted positions: a whole value of the block (starts), of a
	// list entry (a bound, a constraint's expr) and anywhere inside a
	// free-form value (a number of a model's inline body).
	ok := block(`[{"name":"x","lower":0,"upper":{"$var":"v"}}]`, `[{"expr":{"$var":"e"},"type":"ineq"}]`,
		`,"starts":{"$var":"s"},"models":[{"name":"m","inline":{"model_format_version":1,"type":"REG_OLS","target":"y","predictors":["a"],"terms":["a"],"coefficients":{"(intercept)":1,"a":{"$var":"s"}}}}]`,
		`[{"name":"v","values":[10,20]},{"name":"e","values":["x - 1"]},{"name":"s","values":[2]}]`)
	if err := validate(ok); err != nil {
		t.Fatalf("the payload schema rejects an admitted marker: %v", err)
	}
	c := decodeComposedJSON(t, ok)
	if env, err := p.PredictCompose(context.Background(), c); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict = %v %+v", err, env.Errors)
	}
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		if err != nil || len(resp.Optimizations) != 2 {
			t.Fatalf("%s = %v", name, err)
		}
	}
}
