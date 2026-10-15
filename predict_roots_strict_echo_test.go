package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// rootsStrictPulse opens the vector cohort (v.pulse) under opts.
func rootsStrictPulse(t *testing.T, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 96)
	opts.FS = fsys
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// unsuitedMetricRequest is a request whose explicit metric fits its
// vector's kind poorly (cosine on a scale battery, in a chain-mergeable
// filter expression):
// PULSE_VECTOR_METRIC_UNSUITED, a warning the runtime escalates under
// Strict into a refusal before any record is read.
func unsuitedMetricRequest() *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: "v.pulse"},
		Vectors:      []types.VectorSpec{{Name: "battery", Fields: []string{"b*"}, Kind: types.VectorKindScale}},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "cosine(battery, battery) > 0"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
	}
}

func entryCodes(es []*descriptor.EnvelopeEntry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Code)
	}
	return out
}

// rootVerdict is one non-Request predict root's verdict.
type rootVerdict struct {
	env   *descriptor.Envelope
	valid bool
}

func predictRoot(t *testing.T, p *pulse.Pulse, root string) rootVerdict {
	t.Helper()
	ctx := context.Background()
	var (
		env *descriptor.Envelope
		err error
	)
	switch root {
	case "compose":
		env, err = p.PredictCompose(ctx, &pulse.ComposedRequest{Requests: []*pulse.Request{unsuitedMetricRequest()}})
	case "chain":
		env, err = p.PredictChain(ctx, &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Stages: []*pulse.ChainStage{{Request: unsuitedMetricRequest()}}})
	case "facet":
		// discrete_top_k under 5 is a facet warning.
		env, err = p.PredictFacet(ctx, &pulse.FacetRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Fields: []string{"qty"}, DiscreteTopK: 3})
	}
	if err != nil {
		t.Fatalf("%s: %v", root, err)
	}
	var valid bool
	switch d := env.Data.(type) {
	case *pulse.ComposePredictResult:
		valid = d.Valid
	case *pulse.ChainPredictResult:
		valid = d.Valid
	case *pulse.FacetPredictResult:
		valid = d.Valid
	default:
		t.Fatalf("%s: Data = %T", root, env.Data)
	}
	return rootVerdict{env: env, valid: valid}
}

// TestPredictRoots_StrictEscalatesWarnings (#255): each non-Request
// predict root reports a warning without Strict and, under
// Options.Strict, the same entry as an error with Valid false — once,
// never twice (the per-slot body call keeps Strict off; the root
// promotes).
func TestPredictRoots_StrictEscalatesWarnings(t *testing.T) {
	want := map[string]string{
		"compose": string(perr.PULSE_VECTOR_METRIC_UNSUITED),
		"chain":   string(perr.PULSE_VECTOR_METRIC_UNSUITED),
		"facet":   string(perr.PULSE_FACET_VALUES_TRUNCATED),
	}
	lax := rootsStrictPulse(t, pulse.Options{})
	strict := rootsStrictPulse(t, pulse.Options{Strict: true})
	for root, code := range want {
		t.Run(root, func(t *testing.T) {
			v := predictRoot(t, lax, root)
			if !v.valid || len(v.env.Errors) != 0 {
				t.Fatalf("lax: valid = %v, errors %v", v.valid, entryCodes(v.env.Errors))
			}
			if got := entryCodes(v.env.Warnings); len(got) != 1 || got[0] != code {
				t.Fatalf("lax warnings = %v, want [%s]", got, code)
			}
			s := predictRoot(t, strict, root)
			if s.valid {
				t.Errorf("strict: valid = true, want false")
			}
			if got := entryCodes(s.env.Errors); len(got) != 1 || got[0] != code {
				t.Errorf("strict errors = %v, want exactly [%s]", got, code)
			}
			if len(s.env.Warnings) != 0 {
				t.Errorf("strict warnings = %v, want none", entryCodes(s.env.Warnings))
			}
		})
	}
}

// TestPredictRoots_StrictSlotWarningMatchesRuntime: a Compose slot's
// and a chain stage's unsuited metric is ONE error under Strict on
// predict, located like the runtime's one refusal of the same request
// (details.request / details.stage = 0), with the same code.
func TestPredictRoots_StrictSlotWarningMatchesRuntime(t *testing.T) {
	ctx := context.Background()
	p := rootsStrictPulse(t, pulse.Options{Strict: true})
	cases := []struct {
		root, key string
		run       func() error
	}{
		{"compose", "request", func() error {
			_, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*pulse.Request{unsuitedMetricRequest()}})
			return err
		}},
		{"chain", "stage", func() error {
			_, err := p.ProcessChain(ctx, &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Stages: []*pulse.ChainStage{{Request: unsuitedMetricRequest()}}})
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.root, func(t *testing.T) {
			rerr := c.run()
			var ce *perr.CodedError
			if !stderrors.As(rerr, &ce) {
				t.Fatalf("runtime error = %v, want a coded refusal", rerr)
			}
			v := predictRoot(t, p, c.root)
			if len(v.env.Errors) != 1 {
				t.Fatalf("predict errors = %v, want exactly the runtime's one (%s)", entryCodes(v.env.Errors), ce.Code)
			}
			got := v.env.Errors[0]
			if got.Code != string(ce.Code) {
				t.Errorf("predict code %s, runtime %s", got.Code, ce.Code)
			}
			if got.Details[c.key] != 0 || ce.Details[c.key] != 0 {
				t.Errorf("location: predict %v, runtime %v; want %s = 0 on both", got.Details, ce.Details, c.key)
			}
		})
	}
}

// echoJSON marshals v for a structural comparison.
func echoJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// defaultedRequest is a request whose aggregation takes its Type from
// the smart defaults (f64 -> AGG_SUM).
func defaultedRequest() *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: "v.pulse"},
		Aggregations: []*types.Aggregation{{Field: "score"}},
	}
}

// TestPredictRoots_EchoRequest (#255): under Options.EchoRequest each
// non-Request predict root echoes its normalized request on
// env.Request — the form the runtime echoes for the same request
// (Compose: the effective batch, every slot defaults-resolved; chain:
// every stage defaults-resolved; facet: the request as passed) — and
// leaves it nil otherwise.
func TestPredictRoots_EchoRequest(t *testing.T) {
	ctx := context.Background()
	off := rootsStrictPulse(t, pulse.Options{})
	on := rootsStrictPulse(t, pulse.Options{EchoRequest: true})
	sweepBatch := func() *pulse.ComposedRequest {
		return &pulse.ComposedRequest{
			Requests: []*pulse.Request{defaultedRequest()},
			Sweep: &types.SweepSpec{
				Axes:    []types.SweepAxis{{Name: "f", Values: []any{"score", "b1"}}},
				Request: json.RawMessage(`{"cohort":{"filename":"v.pulse"},"aggregations":[{"field":"{{f}}"}]}`),
			},
		}
	}
	t.Run("compose", func(t *testing.T) {
		for name, batch := range map[string]func() *pulse.ComposedRequest{
			"plain": func() *pulse.ComposedRequest {
				return &pulse.ComposedRequest{Requests: []*pulse.Request{defaultedRequest()}}
			},
			"sweep": sweepBatch,
		} {
			t.Run(name, func(t *testing.T) {
				env, err := off.PredictCompose(ctx, batch())
				if err != nil {
					t.Fatal(err)
				}
				if env.Request != nil {
					t.Fatalf("echo off: env.Request = %v, want nil", env.Request)
				}
				env, err = on.PredictCompose(ctx, batch())
				if err != nil {
					t.Fatal(err)
				}
				echoed, ok := env.Request.(*pulse.ComposedRequest)
				if !ok {
					t.Fatalf("env.Request = %T, want *ComposedRequest", env.Request)
				}
				resp, err := on.Compose(ctx, batch())
				if err != nil {
					t.Fatal(err)
				}
				if got, want := echoJSON(t, echoed), echoJSON(t, resp.NormalizedRequest); got != want {
					t.Errorf("predict echo\n %s\nruntime echo\n %s", got, want)
				}
				for i, slot := range echoed.Requests {
					if slot.Aggregations[0].Type == "" {
						t.Errorf("slot %d aggregation not defaults-resolved", i)
					}
				}
			})
		}
	})
	t.Run("chain", func(t *testing.T) {
		chain := func() *pulse.ChainRequest {
			return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Stages: []*pulse.ChainStage{{Name: "s0", Request: defaultedRequest()}}}
		}
		env, err := off.PredictChain(ctx, chain())
		if err != nil {
			t.Fatal(err)
		}
		if env.Request != nil {
			t.Fatalf("echo off: env.Request = %v, want nil", env.Request)
		}
		env, err = on.PredictChain(ctx, chain())
		if err != nil {
			t.Fatal(err)
		}
		echoed, ok := env.Request.(*pulse.ChainRequest)
		if !ok {
			t.Fatalf("env.Request = %T, want *ChainRequest", env.Request)
		}
		resp, err := on.ProcessChain(ctx, chain())
		if err != nil {
			t.Fatal(err)
		}
		if got, want := echoJSON(t, echoed), echoJSON(t, resp.NormalizedRequest); got != want {
			t.Errorf("predict echo\n %s\nruntime echo\n %s", got, want)
		}
	})
	t.Run("facet", func(t *testing.T) {
		req := &pulse.FacetRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Fields: []string{"qty"}}
		env, err := off.PredictFacet(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if env.Request != nil {
			t.Fatalf("echo off: env.Request = %v, want nil", env.Request)
		}
		env, err = on.PredictFacet(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := echoJSON(t, env.Request), echoJSON(t, req); got != want {
			t.Errorf("echo = %s, want %s", got, want)
		}
	})
}

// TestPredict_ResultCarriesReasons (#256): Pulse.Predict's result
// carries the envelope's coded errors and warnings, so a Valid false
// result names its reason.
func TestPredict_ResultCarriesReasons(t *testing.T) {
	ctx := context.Background()
	p := rootsStrictPulse(t, pulse.Options{})
	bad := &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "nope"}}}
	res, err := p.Predict(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	if res.Valid || len(res.Errors) == 0 {
		t.Fatalf("valid = %v, errors %v; want a coded reason", res.Valid, entryCodes(res.Errors))
	}
	good := unsuitedMetricRequest()
	res, err = p.Predict(ctx, good)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || len(res.Errors) != 0 {
		t.Fatalf("good: valid = %v, errors %v", res.Valid, entryCodes(res.Errors))
	}
	found := false
	for _, w := range res.Warnings {
		if w.Code == string(perr.PULSE_VECTOR_METRIC_UNSUITED) {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings %v lack %s", entryCodes(res.Warnings), perr.PULSE_VECTOR_METRIC_UNSUITED)
	}
}
