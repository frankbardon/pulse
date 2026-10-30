package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ATTR_FORMULA reads its expression from the `expression` slot only
// (matrix-extensions-hardening E4-S5 item 4). An expression placed in
// params.expression — or none at all — is PROCESSING_CONFIG "formula
// attribute requires an expression" at runtime; predict used to pass
// it. Predict now refuses it with the runtime's code and message, on
// the request, Compose-slot and chain-stage roots.
func TestFormulaAttribute_ExpressionSlotPredictParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	const wantMsg = "formula attribute requires an expression"
	for _, c := range []struct {
		name   string
		params json.RawMessage
		code   perr.Code
	}{
		// params.expression is not an ATTR_FORMULA params key: the
		// params allowlist refuses it on every arm (U44 #301).
		{"params.expression", json.RawMessage(`{"expression":"qty * 2"}`), perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{"no expression", nil, perr.PROCESSING_CONFIG},
	} {
		attr := func() *types.Attribute {
			return &types.Attribute{Type: types.ATTR_FORMULA, Label: "f", Params: c.params}
		}
		req := func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"}, Attributes: []*types.Attribute{attr()},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}}}
		}
		t.Run(c.name+"/request", func(t *testing.T) {
			pe := predictCodedError(t, p, data, req())
			if pe == nil || pe.Code != string(c.code) || (c.code == perr.PROCESSING_CONFIG && pe.Message != wantMsg) {
				t.Fatalf("Predict = %+v, want %s %q", pe, c.code, wantMsg)
			}
			_, err := p.Process(ctx, req())
			ce := codeOf(t, err)
			if ce.Code != c.code || ce.Message != pe.Message {
				t.Fatalf("Process = %s %q, want predict's %s %q", ce.Code, ce.Message, pe.Code, pe.Message)
			}
		})
		t.Run(c.name+"/compose", func(t *testing.T) {
			r := req()
			r.Label = "a"
			creq := func() *pulse.ComposedRequest { return &pulse.ComposedRequest{Requests: []*types.Request{r}} }
			env, err := p.PredictCompose(ctx, creq())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Fatalf("PredictCompose errors = %+v, want %s first", env.Errors, c.code)
			}
			if _, err := p.Compose(ctx, creq()); codeOf(t, err).Code != c.code {
				t.Fatalf("Compose = %v, want %s", err, c.code)
			}
		})
		t.Run(c.name+"/chain", func(t *testing.T) {
			chain := func() *pulse.ChainRequest {
				return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Stages: []*types.ChainStage{
					{Name: "s", Request: &types.Request{Attributes: []*types.Attribute{attr()},
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}}}},
				}}
			}
			env, err := p.PredictChain(ctx, chain())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Fatalf("PredictChain errors = %+v, want %s first", env.Errors, c.code)
			}
			if _, err := p.ProcessChain(ctx, chain()); codeOf(t, err).Code != c.code {
				t.Fatalf("ProcessChain = %v, want %s", err, c.code)
			}
		})
	}

	// The expression slot itself stays accepted on both sides.
	ok := &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"},
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "qty * 2"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}}}
	if pe := predictCodedError(t, p, data, ok); pe != nil {
		t.Fatalf("Predict on the expression slot = %+v, want clean", pe)
	}
	if _, err := p.Process(ctx, ok); err != nil {
		t.Fatalf("Process on the expression slot: %v", err)
	}
}
