package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optfixture"
	"github.com/frankbardon/pulse/types"
)

// compose_optimize_inline_test.go pins the inline `models` arm (U79
// E3-S3, G2) at the facade: one coef() objective bound to the saved fit
// in one block and to an inline truth model in the other — its
// intercept a params expression — lands on each model's own analytic
// optimum, on both orchestrators; predict and the runtime refuse a bad
// inline body and a score of an inline model identically.

// inlineTruth is the demand model declared inline: intercept
// params.a + params.lift, slope params.b.
const inlineTruth = `{"model_format_version": 1, "type": "REG_OLS", "target": "units",
	"predictors": ["price"], "terms": ["price"],
	"coefficients": {"(intercept)": "params.a + params.lift", "price": "params.b"}}`

// inlineOptBody is PriceModel with a second block over the inline
// truth, the same objective text.
func inlineOptBody(t *testing.T, truth string) *ComposedRequest {
	t.Helper()
	c := decodeOpt(t, optfixture.PriceModel, nil)
	c.Params["a"], c.Params["lift"], c.Params["b"] = json.Number("130"), json.Number("10"), json.Number("-4")
	truthBlock := c.Optimize[0]
	truthBlock.Name = "price_truth"
	truthBlock.Models = []types.ModelRef{{Name: "demand", Inline: json.RawMessage(truth)}}
	c.Optimize = append(c.Optimize, truthBlock)
	return c
}

// TestOptimize_InlineTruthVsSavedFit: block 0 reads the saved demand
// model (intercept 120, slope −4: optimum 17.5); block 1 the inline
// truth (intercept 130 + 10, slope −4: optimum (−4·5 − 140)/(−8) = 20)
// through the very same objective — each lands on its own model's
// optimum; predict accepts the batch.
func TestOptimize_InlineTruthVsSavedFit(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	const truthOptimum = 20.0
	for name, run := range composeRuns(p) {
		resp, err := run(inlineOptBody(t, inlineTruth))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(resp.Optimizations) != 2 {
			t.Fatalf("%s: optimizations %+v", name, resp.Optimizations)
		}
		for i, want := range []float64{optfixture.ModelOptimum, truthOptimum} {
			o := resp.Optimizations[i]
			if o.Error != nil || !o.Converged || math.Abs(o.Variables[0].Value-want) > 1e-9 {
				t.Errorf("%s: block %d %+v, want p = %v", name, i, o, want)
			}
		}
	}
	env, err := p.PredictCompose(context.Background(), inlineOptBody(t, inlineTruth))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
}

// TestOptimize_InlineRefusalParity: a bad inline body — an unknown
// term, an expression calling coef(), an unsupported type, both path
// and inline — is refused by predict exactly as by both orchestrators
// (code, message, details incl. details.optimize).
func TestOptimize_InlineRefusalParity(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	ctx := context.Background()
	cases := []struct {
		name, from, to string
		path           bool
		code           perr.Code
		reason         string
	}{
		{"unknown term", `"price": "params.b"`, `"price": "params.b", "region": 1`, false, perr.PULSE_MODEL_REF_INVALID, model.ReasonInlineTermUnknown},
		{"coef inside a number", `"params.b"`, `"coef(\"demand\", \"price\")"`, false, perr.PULSE_MODEL_REF_INVALID, "inline_invalid"},
		{"type unsupported", `REG_OLS`, `REG_MULTINOMIAL`, false, perr.PULSE_MODEL_REF_INVALID, model.ReasonInlineTypeUnsupported},
		{"undeclared param", `params.lift`, `params.nope`, false, perr.PULSE_PARAMS_UNDECLARED, ""},
		{"path and inline", "", "", true, perr.PULSE_MODEL_REF_INVALID, model.ReasonRefPathAndInline},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := func() *ComposedRequest {
				c := inlineOptBody(t, strings.Replace(inlineTruth, tc.from, tc.to, 1))
				if tc.path {
					c.Optimize[1].Models[0].Path = optfixture.ModelPath
				}
				return c
			}
			env, err := p.PredictCompose(ctx, body())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) || env.Errors[0].Details["optimize"] != 1 {
				t.Fatalf("predict %+v, want %s at block 1", env.Errors, tc.code)
			}
			if tc.reason != "" && env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict reason %v, want %s", env.Errors[0].Details["reason"], tc.reason)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(body())
				ce := requireCoded(t, rerr, tc.code)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v, runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}

// TestModels_InlineScoreRefusedParity: a request scoring an inline
// model with ATTR_REG_PREDICT is refused inline_not_scorable by predict
// and Process alike; a coef() filter over the same inline model runs.
func TestModels_InlineScoreRefusedParity(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	ctx := context.Background()
	req := func(attr string) *Request {
		var r Request
		raw := `{"cohort": {"filename": "` + optfixture.CohortFile + `"},
			"params": {"a": 130, "lift": 10, "b": -4},
			"models": [{"name": "demand", "inline": ` + inlineTruth + `}],
			"attributes": [` + attr + `],
			"aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}]}`
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatal(err)
		}
		return &r
	}
	score := `{"type": "ATTR_REG_PREDICT", "label": "hat", "params": {"fit": "demand"}}`
	env, err := p.Predict(ctx, req(score))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_MODEL_SCORE_INVALID) || env.Errors[0].Details["reason"] != model.ReasonInlineNotScorable {
		t.Fatalf("predict %+v", env.Errors)
	}
	_, rerr := p.Process(ctx, req(score))
	ce := requireCoded(t, rerr, perr.PULSE_MODEL_SCORE_INVALID)
	if env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
		t.Fatalf("predict %+v, runtime %s %v", env.Errors[0], ce.Message, ce.Details)
	}
	formula := `{"type": "ATTR_FORMULA", "label": "hat", "expression": "coef(\"demand\", \"(intercept)\") + coef(\"demand\", \"price\") * price"}`
	resp, err := p.Process(ctx, req(formula))
	if err != nil {
		t.Fatal(err)
	}
	if env, err := p.Predict(ctx, req(formula)); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict coef(): %v %+v", err, env)
	}
	if resp == nil {
		t.Fatal("no response")
	}
}
