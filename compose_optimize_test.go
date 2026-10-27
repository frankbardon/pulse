package pulse

import (
	"context"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// optimizeComposed is a batch over the parity cohort: one count slot,
// a regression fit `m` (age on age — never run before a refusal), the
// batch params and the optimize block in raw (a JSON array).
func optimizeComposed(t *testing.T, raw string) *ComposedRequest {
	t.Helper()
	return decodeComposedJSON(t, `{"requests":[{"cohort":{"filename":"`+parityCohort+`"},"aggregations":[{"type":"AGG_COUNT","field":"age","label":"n"}]}],
		"params":{"budget":100,"floor":5,"tag":"x"},
		"fits":[{"name":"m","request":{"cohort":{"filename":"`+parityCohort+`"},"regressions":[{"type":"REG_OLS","target":"age","predictors":["age"]}]}}],
		"optimize":`+raw+`}`)
}

// TestOptimize_RefusalParity: predict and both orchestrators refuse
// every malformed optimize block with the same code, message and
// details, before any fit or slot runs.
func TestOptimize_RefusalParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := sweepPulse(t, Options{Extensions: Extensions{ExprFunctions: []ExprFunction{{Name: "boost", Fn: func(x float64) float64 { return 2 * x }}}}})
	block := func(vars, objective, extra string) string {
		return `[{"name":"o","direction":"maximize","variables":` + vars + `,"objective":` + objective + extra + `}]`
	}
	x := `[{"name":"x","lower":0,"upper":"params.budget"}]`
	cases := []struct {
		name string
		raw  string
		code perr.Code
	}{
		{"unknown identifier", block(x, `"x * spend"`, ``), perr.PULSE_OPTIMIZE_EXPRESSION_INVALID},
		{"unknown function", block(x, `"lift(x)"`, ``), perr.PULSE_OPTIMIZE_EXPRESSION_INVALID},
		{"unknown fit", block(x, `"coef(\"n\", \"age\") * x"`, ``), perr.PULSE_MODEL_UNKNOWN},
		{"unknown term", block(x, `"coef(\"m\", \"region\") * x"`, ``), perr.PULSE_MODEL_TERM_UNKNOWN},
		{"duplicate variable", block(`[{"name":"x","lower":0,"upper":1},{"name":"x","lower":0,"upper":1}]`, `"x"`, ``), perr.PULSE_OPTIMIZE_VARIABLE_INVALID},
		{"lower above upper", block(`[{"name":"x","lower":"params.budget","upper":"params.floor"}]`, `"x"`, ``), perr.PULSE_OPTIMIZE_VARIABLE_INVALID},
		{"bound param not numeric", block(`[{"name":"x","lower":0,"upper":"params.tag"}]`, `"x"`, ``), perr.PULSE_OPTIMIZE_VARIABLE_INVALID},
		{"bound param undeclared", block(`[{"name":"x","lower":0,"upper":"params.cap"}]`, `"x"`, ``), perr.PULSE_PARAMS_UNDECLARED},
		{"empty objective", block(x, `""`, ``), perr.PULSE_OPTIMIZE_SPEC_INVALID},
		{"bad constraint type", block(x, `"x"`, `,"constraints":[{"expr":"params.budget - x","type":"le"}]`), perr.PULSE_OPTIMIZE_SPEC_INVALID},
		{"starts below one", block(x, `"x"`, `,"starts":0`), perr.PULSE_OPTIMIZE_SPEC_INVALID},
		{"not numeric", block(x, `"x > 1"`, ``), perr.PULSE_OPTIMIZE_EXPRESSION_INVALID},
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
			for name, run := range composeRuns(p) {
				resp, rerr := run(c)
				ce := requireCoded(t, rerr, tc.code)
				if resp != nil {
					t.Fatalf("%s: a refused batch returned a response", name)
				}
				if env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}

	// A valid block — an embedder function, params, a constraint and a
	// coef() over the fit — passes on every surface.
	c := optimizeComposed(t, block(`[{"name":"x","lower":"params.floor","upper":"params.budget","start":10}]`,
		`"boost(x) - coef(\"m\", \"age\") * x * x"`, `,"constraints":[{"expr":"params.budget - x","type":"ineq"}],"starts":3,"rng_seed":11`))
	env, err := p.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range env.Errors {
		t.Errorf("predict refused the valid block: %+v", e)
	}
	for name, run := range composeRuns(p) {
		if _, err := run(c); err != nil {
			t.Errorf("%s refused the valid block: %v", name, err)
		}
	}
}

// TestOptimize_HiddenParity: an instance hiding
// capability:compose_optimize refuses a set `optimize` as an unknown
// field on Compose, ComposeParallel and PredictCompose alike; an
// instance offering it accepts the same batch.
func TestOptimize_HiddenParity(t *testing.T) {
	t.Parallel()

	fsys := parityFS(t)
	control := newParityHostWith(t, fsys, "with-optimize", parityHostConfig{
		profiles: map[string][]string{"with-optimize": {"capability:compose", "capability:compose_optimize", "capability:params", string(types.AGG_COUNT)}},
	})
	h := newParityHostWith(t, fsys, "no-optimize", parityHostConfig{
		profiles: map[string][]string{"no-optimize": {"capability:compose", "capability:params", string(types.FILTER_EXPRESSION), string(types.AGG_COUNT)}},
	})
	ctx := context.Background()
	build := func() *ComposedRequest {
		return decodeComposedJSON(t, `{"requests":[{"cohort":{"filename":"`+parityCohort+`"},"aggregations":[{"type":"AGG_COUNT","field":"age","label":"n"}]}],
			"params":{"budget":10},
			"optimize":[{"name":"o","direction":"minimize","variables":[{"name":"x","lower":0,"upper":"params.budget"}],"objective":"(x - 3) * (x - 3)"}]}`)
	}
	_, err := h.p.Compose(ctx, build())
	serial := slotGateFromErr(err)
	if serial == nil || serial.code != perr.PULSE_REQUEST_UNKNOWN_FIELD || !reflect.DeepEqual(toStrings(serial.details["unknown_keys"]), []string{"optimize"}) {
		t.Fatalf("Compose: got %+v (%v)", serial, err)
	}
	_, err = h.p.ComposeParallel(ctx, build(), ComposeOptions{MaxWorkers: 2})
	if par := slotGateFromErr(err); par == nil || par.code != serial.code || !reflect.DeepEqual(par.details, serial.details) {
		t.Fatalf("ComposeParallel %+v, Compose %+v", par, serial)
	}
	env, err := h.p.PredictCompose(ctx, build())
	if err != nil {
		t.Fatal(err)
	}
	if pred := slotGateFromEnv(env); pred == nil || pred.code != serial.code ||
		!reflect.DeepEqual(toStrings(pred.details["unknown_keys"]), toStrings(serial.details["unknown_keys"])) {
		t.Fatalf("PredictCompose %+v, Compose %+v", pred, serial)
	}
	if _, err := control.p.Compose(ctx, build()); err != nil {
		t.Fatalf("the enabled instance refused the block: %v", err)
	}
	env, err = control.p.PredictCompose(ctx, build())
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("enabled predict: %v %+v", err, env)
	}
}
