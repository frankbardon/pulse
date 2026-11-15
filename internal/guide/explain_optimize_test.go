package guide

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// optimizeBatch is a Compose batch over the fixture cohort: one counting
// slot `s` (aggregation `n`), the params budget / floor and the optimize
// blocks in raw (a JSON array).
func optimizeBatch(t *testing.T, raw string) *types.ComposedRequest {
	t.Helper()
	var c types.ComposedRequest
	body := `{"requests":[{"label":"s","cohort":{"filename":"fixture.pulse"},"aggregations":[{"type":"AGG_COUNT","field":"score","label":"n"}]}],
		"params":{"budget":100,"floor":5},"optimize":` + raw + `}`
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

const optimizeBlocks = `[
 {"name":"plain","direction":"maximize","variables":[{"name":"x","lower":"params.floor","upper":"params.budget","start":10}],
  "objective":"result(\"s\", \"n\") * hill(x, 20, 1.5) - 0.1 * x",
  "constraints":[{"expr":"params.budget - x","type":"ineq"}],"starts":3},
 {"name":"fan_{{k}}","direction":"minimize","variables":[{"name":"y","lower":0,"upper":{"$var":"k"}}],
  "objective":"(y - 3) * (y - 3)","for_each":{"axes":[{"name":"k","values":[10,20]}]}},
 {"name":"fd","direction":"minimize","variables":[{"name":"z","lower":0,"upper":1}],"objective":"z * z","gradient":"fd"}
]`

func stepSlots(res *descriptor.ExplainResult) []string {
	var out []string
	for _, s := range res.Steps {
		out = append(out, s.Slot)
	}
	return out
}

func stepAt(t *testing.T, res *descriptor.ExplainResult, slot string) string {
	t.Helper()
	for _, s := range res.Steps {
		if s.Slot == slot {
			return s.Text
		}
	}
	t.Fatalf("no step %s in %v", slot, stepSlots(res))
	return ""
}

// TestExplainOptimize_StepPerBlock: one step per block as it RUNS — the
// for_each block once per combination at its expanded index, after one
// fan-out step at its written path — naming the objective, direction,
// variables with bounds, constraints, starts, what it reads and the
// gradient mode predict judged.
func TestExplainOptimize_StepPerBlock(t *testing.T) {
	t.Parallel()

	c := optimizeBatch(t, optimizeBlocks)
	res := explain(t, nil, descriptor.ExplainRequest{Composed: c}, explainChecks(t, nil))
	if res.Valid == nil || !*res.Valid {
		t.Fatalf("batch refused: %+v", res.Refusals)
	}
	var got []string
	for _, s := range stepSlots(res) {
		if strings.HasPrefix(s, "optimize") {
			got = append(got, s)
		}
	}
	if want := []string{"optimize[0]", "optimize[1].for_each", "optimize[1]", "optimize[2]", "optimize[3]"}; !slices.Equal(got, want) {
		t.Fatalf("optimize steps %v, want %v", got, want)
	}
	plain := stepAt(t, res, "optimize[0]")
	for _, want := range []string{
		"Optimize block 1 `plain` maximizes", "`result(\"s\", \"n\") * hill(x, 20, 1.5) - 0.1 * x`",
		"`x` between `params.floor` and `params.budget` (starting at 10)", "`params.budget - x` kept at or above 0",
		"from 3 starts", "the params `budget` and `floor`", "the result `n` of the slot `s` and the functions `hill`.",
		"differentiates it analytically",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("optimize[0] text lacks %q:\n%s", want, plain)
		}
	}
	if fan := stepAt(t, res, "optimize[1].for_each"); !strings.Contains(fan, "expands it into 2 optimize blocks") {
		t.Errorf("fan-out step = %q", fan)
	}
	if s := stepAt(t, res, "optimize[1]"); !strings.Contains(s, "`fan_10` minimizes") || !strings.Contains(s, "and 10") {
		t.Errorf("optimize[1] = %q", s)
	}
	if s := stepAt(t, res, "optimize[2]"); !strings.Contains(s, "`fan_20`") || !strings.Contains(s, "and 20") {
		t.Errorf("optimize[2] = %q", s)
	}
	if s := stepAt(t, res, "optimize[3]"); !strings.Contains(s, "by finite differences, as its gradient fd asks") {
		t.Errorf("optimize[3] = %q", s)
	}
	if !strings.Contains(res.Summary, "4 optimize blocks") {
		t.Errorf("summary = %q", res.Summary)
	}
}

// TestExplainOptimize_GradientUnjudgedWithoutCheck: without the Compose
// check Explain does not claim an analytic gradient it never judged.
func TestExplainOptimize_GradientUnjudgedWithoutCheck(t *testing.T) {
	t.Parallel()

	res := explain(t, nil, descriptor.ExplainRequest{Composed: optimizeBatch(t, optimizeBlocks)}, Checks{})
	s := stepAt(t, res, "optimize[0]")
	if strings.Contains(s, "analytically") || !strings.Contains(s, "judged when the batch is checked") {
		t.Errorf("unchecked gradient sentence: %q", s)
	}
}

// TestExplainOptimize_GradientFallbackFromAdvisory: the fallback is
// read off predict's PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK
// (details.optimize, functions), never recomputed; an instance that
// suppresses the advisory gets the unjudged sentence, not "analytic".
func TestExplainOptimize_GradientFallbackFromAdvisory(t *testing.T) {
	t.Parallel()

	c := optimizeBatch(t, `[{"name":"o","direction":"maximize","variables":[{"name":"x","lower":0,"upper":1}],"objective":"x"}]`)
	env := func(advs ...descriptor.Advisory) Checks {
		return Checks{Compose: func(*types.ComposedRequest) (*descriptor.Envelope, error) {
			return descriptor.NewEnvelope(&descx.ComposeValidationResult{Valid: true, Optimize: []descriptor.OptimizeSummary{{Name: "o"}}, Advisories: advs}), nil
		}}
	}
	fb := descriptor.Advisory{Code: string(errors.PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK),
		Details: map[string]any{"optimize": 0, "functions": []string{"boost"}}}
	s := stepAt(t, explain(t, nil, descriptor.ExplainRequest{Composed: c}, env(fb)), "optimize[0]")
	if !strings.Contains(s, "`boost` has no derivative rule") || !strings.Contains(s, "finite differences") {
		t.Errorf("fallback sentence: %q", s)
	}
	if s := stepAt(t, explain(t, nil, descriptor.ExplainRequest{Composed: c}, env()), "optimize[0]"); !strings.Contains(s, "analytically") {
		t.Errorf("no advisory: %q", s)
	}
	inst := (*descx.InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{string(errors.PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK)})
	if s := stepAt(t, explain(t, inst, descriptor.ExplainRequest{Composed: c}, env()), "optimize[0]"); strings.Contains(s, "analytically") {
		t.Errorf("suppressed advisory still claims analytic: %q", s)
	}
}

// TestExplainOptimize_RefusalsLocated: a block's refusal is a refusal
// located at its block — details.slot `optimize[i]`, the EXPANDED index
// predict names (a for_each block before it shifts it) — with predict's
// code and details.field kept.
func TestExplainOptimize_RefusalsLocated(t *testing.T) {
	t.Parallel()

	fan := `{"name":"f_{{k}}","direction":"minimize","variables":[{"name":"y","lower":0,"upper":{"$var":"k"}}],"objective":"y","for_each":{"axes":[{"name":"k","values":[1,2]}]}}`
	cases := []struct {
		name, raw, code, slot string
	}{
		{"bounds crossed", `[{"name":"o","direction":"maximize","variables":[{"name":"x","lower":"params.budget","upper":"params.floor"}],"objective":"x"}]`,
			string(errors.PULSE_OPTIMIZE_VARIABLE_INVALID), "optimize[0]"},
		{"unknown fit", `[` + fan + `,{"name":"o","direction":"maximize","variables":[{"name":"x","lower":0,"upper":1}],"objective":"coef(\"nope\", \"x\") * x"}]`,
			string(errors.PULSE_MODEL_UNKNOWN), "optimize[2]"},
		{"unknown identifier", `[{"name":"o","direction":"maximize","variables":[{"name":"x","lower":0,"upper":1}],"objective":"x * spend"}]`,
			string(errors.PULSE_OPTIMIZE_EXPRESSION_INVALID), "optimize[0]"},
		{"undeclared bound param", `[{"name":"o","direction":"maximize","variables":[{"name":"x","lower":0,"upper":"params.cap"}],"objective":"x"}]`,
			string(errors.PULSE_PARAMS_UNDECLARED), "optimize[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := optimizeBatch(t, tc.raw)
			env := descx.ValidateComposeWithOptions(c, nil)
			res := explain(t, nil, descriptor.ExplainRequest{Composed: c}, explainChecks(t, nil))
			if res.Valid == nil || *res.Valid || len(res.Refusals) != len(env.Errors) || len(res.Refusals) == 0 {
				t.Fatalf("explain refusals %+v, predict %+v", res.Refusals, env.Errors)
			}
			r := res.Refusals[0]
			if r.Code != tc.code || r.Code != env.Errors[0].Code {
				t.Fatalf("code %s, want %s (predict %s)", r.Code, tc.code, env.Errors[0].Code)
			}
			if r.Details["slot"] != tc.slot {
				t.Errorf("slot %v, want %s", r.Details["slot"], tc.slot)
			}
			if f, _ := r.Details["field"].(string); !strings.HasPrefix(f, tc.slot) {
				t.Errorf("field %q not under %s", f, tc.slot)
			}
		})
	}
}

// TestExplainOptimize_UnresolvedBoundNamed: a bound the static check
// leaves to the runtime (it reads a fit) is said to be resolved when
// the batch runs.
func TestExplainOptimize_UnresolvedBoundNamed(t *testing.T) {
	t.Parallel()

	c := optimizeBatch(t, `[{"name":"o","direction":"maximize","variables":[{"name":"x","lower":0,"upper":"result(\"s\", \"n\")"}],"objective":"x"}]`)
	res := explain(t, nil, descriptor.ExplainRequest{Composed: c}, explainChecks(t, nil))
	if res.Valid == nil || !*res.Valid {
		t.Fatalf("refused: %+v", res.Refusals)
	}
	if s := stepAt(t, res, "optimize[0]"); !strings.Contains(s, "1 bound or start reads") {
		t.Errorf("unresolved bound not named: %q", s)
	}
}

// optimizeResponse carries a converged block, a posterior block with
// intervals and a failed block.
func optimizeResponse() *types.ComposedResponse {
	return &types.ComposedResponse{Optimizations: []types.OptimizeResult{
		{Name: "plain", Direction: types.OptimizeMaximize, Objective: 12.5, Converged: true, Status: "ok", Iterations: 7, Starts: 3, StartsAgreeing: 2,
			Gradient: types.OptimizeGradientFD, GradientFallback: []string{"boost", "let"},
			Variables:   []types.OptimizeVariableResult{{Name: "x", Value: 40, Marginal: 0.01, Active: types.OptimizeActiveUpper}},
			Constraints: []types.OptimizeConstraintResult{{Expr: "100 - x", Type: types.OptimizeConstraintIneq, Value: 60}}},
		{Name: "post", Direction: types.OptimizeMaximize, Objective: 3, Converged: true, Iterations: 4, Starts: 1, StartsAgreeing: 1,
			Gradient:  types.OptimizeGradientAnalytic,
			Variables: []types.OptimizeVariableResult{{Name: "tv", Value: 20, Marginal: math.NaN(), Interval: &types.OptimizeInterval{Lower: 15, Upper: 25}}},
			Posterior: &types.OptimizePosteriorResult{Fit: "b", Objective: types.OptimizePosteriorMean, Draws: 100, DrawsAvailable: 400, DrawsResolved: 95, CredibleLevel: 0.9}},
		{Name: "bad", Direction: types.OptimizeMinimize, Error: &types.SlotError{Code: string(errors.PROCESSING_RUNTIME), Message: "no start converged"}},
	}}
}

// TestExplainOptimize_Response: one finding per Optimizations entry at
// `optimizations[i]` — descriptive with its solution figures, the
// posterior's intervals and draw counts, the gradient each block solved
// with (and a fallback's functions), a failed block not_computable —
// and the caveats a reader must not lose.
func TestExplainOptimize_Response(t *testing.T) {
	t.Parallel()

	res := explain(t, nil, descriptor.ExplainRequest{ComposedResponse: optimizeResponse(), Detail: descriptor.ExplainFull}, Checks{})
	if len(res.Findings) != 3 {
		t.Fatalf("findings %+v", res.Findings)
	}
	want := []struct {
		slot    string
		verdict descriptor.Verdict
	}{{"optimizations[0]", descriptor.VerdictDescriptive}, {"optimizations[1]", descriptor.VerdictDescriptive}, {"optimizations[2]", descriptor.VerdictNotComputable}}
	for i, w := range want {
		if f := res.Findings[i]; f.Slot != w.slot || f.Verdict != w.verdict {
			t.Errorf("finding %d = %s %s, want %s %s", i, f.Slot, f.Verdict, w.slot, w.verdict)
		}
	}
	n := res.Findings[0].Numbers
	if *n["objective"] != 12.5 || *n["variables.x.value"] != 40 || *n["starts_agreeing"] != 2 || *n["constraints[0].value"] != 60 {
		t.Errorf("plain numbers %v", n)
	}
	p := res.Findings[1].Numbers
	if *p["variables.tv.interval.lower"] != 15 || *p["variables.tv.interval.upper"] != 25 || *p["posterior.draws_resolved"] != 95 {
		t.Errorf("posterior numbers %v", p)
	}
	if p["variables.tv.marginal"] != nil {
		t.Errorf("undefined marginal not null: %v", *p["variables.tv.marginal"])
	}
	all := strings.Join(res.Sentences, "\n")
	for _, s := range []string{"(on its upper bound)", "`tv` from 15 to 25", "0.9 level", "fit `b`",
		"It solved by finite differences, because `boost` and `let` have no derivative rule (" + string(errors.PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK) + ")",
		"It solved with the analytic gradient."} {
		if !strings.Contains(all, s) {
			t.Errorf("sentences lack %q:\n%s", s, all)
		}
	}
	cav := strings.Join(res.Caveats, "\n")
	for _, s := range []string{"only 2 of 3 starts", "failed with " + string(errors.PROCESSING_RUNTIME), "5 of its per-draw re-solves"} {
		if !strings.Contains(cav, s) {
			t.Errorf("caveats lack %q:\n%s", s, cav)
		}
	}
	if !strings.Contains(res.Summary, "3 optimize results") {
		t.Errorf("summary = %q", res.Summary)
	}
}

// TestExplainOptimize_ProseLint: every optimize sentence passes the
// guidance lint, request and response mode, both details.
func TestExplainOptimize_ProseLint(t *testing.T) {
	t.Parallel()

	reqs := []descriptor.ExplainRequest{
		{Composed: optimizeBatch(t, optimizeBlocks)},
		{Composed: optimizeBatch(t, `[{"name":"p","direction":"maximize","variables":[{"name":"x","lower":0,"upper":1}],"objective":"x","posterior":{"objective":"quantile","q":0.1,"draws":50}}]`)},
		{ComposedResponse: optimizeResponse()},
	}
	n := 0
	for _, req := range reqs {
		for _, d := range []descriptor.ExplainDetail{descriptor.ExplainTerse, descriptor.ExplainFull} {
			req.Detail = d
			res := explain(t, nil, req, explainChecks(t, nil))
			for _, s := range explainTexts(res) {
				n++
				for _, h := range descx.LintGuidanceText(s) {
					t.Errorf("%s/%s: lint %s on %q in %q", res.Root, d, h.Rule, h.Span, s)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("vacuous: no text linted")
	}
}
