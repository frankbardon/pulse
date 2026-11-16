package pulse

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// explain_compose_optimize_test.go pins Explain's Compose root on the
// `optimize` blocks to PredictCompose and both orchestrators: a block's
// refusal is the same code and details on all three, located at
// details.slot `optimize[i]`; a valid block's step names the gradient
// mode predict's PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK decides, and
// the runtime result's gradient echo (read back by response-side
// Explain) is that same verdict.

func TestExplain_ComposeOptimizeMatchesPredictAndRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := sweepPulse(t, Options{Extensions: Extensions{ExprFunctions: []ExprFunction{{Name: "boost", Fn: func(x float64) float64 { return 2 * x }}}}})
	block := func(name, vars, objective string) string {
		return `{"name":"` + name + `","direction":"maximize","variables":` + vars + `,"objective":` + objective + `}`
	}
	x := `[{"name":"x","lower":0,"upper":"params.budget"}]`
	fan := `{"name":"f_{{k}}","direction":"minimize","variables":[{"name":"y","lower":0,"upper":{"$var":"k"}}],"objective":"y * y","for_each":{"axes":[{"name":"k","values":[1,2]}]}}`
	cases := []struct {
		name, raw string
		code      perr.Code
		slot      string
	}{
		{"bound crossed", `[` + block("o", `[{"name":"x","lower":"params.budget","upper":"params.floor"}]`, `"x"`) + `]`, perr.PULSE_OPTIMIZE_VARIABLE_INVALID, "optimize[0]"},
		{"unknown fit after a fan-out", `[` + fan + `,` + block("o", x, `"coef(\"n\", \"age\") * x"`) + `]`, perr.PULSE_MODEL_UNKNOWN, "optimize[2]"},
		{"unknown term", `[` + block("o", x, `"coef(\"m\", \"region\") * x"`) + `]`, perr.PULSE_MODEL_TERM_UNKNOWN, "optimize[0]"},
		{"undeclared bound param", `[` + block("o", `[{"name":"x","lower":0,"upper":"params.cap"}]`, `"x"`) + `]`, perr.PULSE_PARAMS_UNDECLARED, "optimize[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := optimizeComposed(t, tc.raw)
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) {
				t.Fatalf("predict errors %+v, want %s", env.Errors, tc.code)
			}
			res, err := p.Explain(ctx, descriptor.ExplainRequest{Composed: c})
			if err != nil {
				t.Fatal(err)
			}
			if res.Valid == nil || *res.Valid || len(res.Refusals) == 0 {
				t.Fatalf("explain valid %v refusals %+v", res.Valid, res.Refusals)
			}
			r := res.Refusals[0]
			if r.Code != env.Errors[0].Code || r.Message != env.Errors[0].Message || r.Details["slot"] != tc.slot {
				t.Fatalf("explain refusal %+v, predict %+v, want slot %s", r, env.Errors[0], tc.slot)
			}
			for k, v := range env.Errors[0].Details {
				if !reflect.DeepEqual(r.Details[k], v) {
					t.Errorf("details[%s] = %v, predict %v", k, r.Details[k], v)
				}
			}
			for name, run := range composeRuns(p) {
				_, rerr := run(c)
				ce := requireCoded(t, rerr, tc.code)
				if ce.Details["optimize"] != r.Details["optimize"] {
					t.Errorf("%s: runtime optimize %v, explain %v", name, ce.Details["optimize"], r.Details["optimize"])
				}
			}
		})
	}

	t.Run("gradient mode follows predict's advisory, and the runtime echo agrees", func(t *testing.T) {
		c := optimizeComposed(t, `[`+block("fb", x, `"boost(x) - x * x"`)+`,`+block("an", x, `"coef(\"m\", \"age\") * x - x * x"`)+`]`)
		res, err := p.Explain(ctx, descriptor.ExplainRequest{Composed: c})
		if err != nil {
			t.Fatal(err)
		}
		if res.Valid == nil || !*res.Valid {
			t.Fatalf("refused: %+v", res.Refusals)
		}
		var fired []int
		var functions []string
		for _, a := range res.Advisories {
			if a.Code == string(perr.PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK) {
				fired = append(fired, a.Details["optimize"].(int))
				functions = a.Details["functions"].([]string)
			}
		}
		if !slices.Equal(fired, []int{0}) {
			t.Fatalf("fallback advisories on %v, want [0]", fired)
		}
		// The runtime echo is the advisory's verdict on both
		// orchestrators, and response-side Explain reports it.
		for name, run := range composeRuns(p) {
			resp, err := run(c)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			o := resp.Optimizations
			if len(o) != 2 || o[0].Gradient != types.OptimizeGradientFD || !slices.Equal(o[0].GradientFallback, functions) ||
				o[1].Gradient != types.OptimizeGradientAnalytic || o[1].GradientFallback != nil {
				t.Fatalf("%s: runtime echo %+v, want fd %v then analytic", name, o, functions)
			}
			rex, err := p.Explain(ctx, descriptor.ExplainRequest{ComposedResponse: resp, Detail: descriptor.ExplainFull})
			if err != nil {
				t.Fatal(err)
			}
			found := map[string]string{}
			for _, s := range rex.Sentences {
				switch {
				case strings.Contains(s, "`fb`"):
					found["fb"] = s
				case strings.Contains(s, "`an`"):
					found["an"] = s
				}
			}
			if !strings.Contains(found["fb"], "finite differences, because `boost` has no derivative rule") {
				t.Errorf("%s: fallback finding %q", name, found["fb"])
			}
			if !strings.Contains(found["an"], "solved with the analytic gradient") {
				t.Errorf("%s: analytic finding %q", name, found["an"])
			}
		}
		texts := map[string]string{}
		for _, s := range res.Steps {
			texts[s.Slot] = s.Text
		}
		if s := texts["optimize[0]"]; !strings.Contains(s, "`boost` has no derivative rule") {
			t.Errorf("fallback block: %q", s)
		}
		if s := texts["optimize[1]"]; !strings.Contains(s, "analytically") || !strings.Contains(s, "1 fit `m`") {
			t.Errorf("analytic block: %q", s)
		}
	})
}
