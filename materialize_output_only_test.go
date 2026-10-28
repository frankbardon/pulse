package pulse

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestMaterializeOutput_RefusedOutsideMaterialize: every entry point
// that runs or predicts a Request other than Materialize refuses the
// materialize-only `output` slot with PULSE_MATERIALIZE_OUTPUT_ONLY
// (details.slot output; a Compose slot / fit or chain stage located),
// and the same request without it runs.
func TestMaterializeOutput_RefusedOutsideMaterialize(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	cohort := &types.Cohort{Filename: "src.pulse"}
	req := func(withOutput bool) *Request {
		r := &Request{Cohort: cohort, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}}
		if withOutput {
			r.Output = &types.MaterializeOutput{Exclude: []string{"tags"}}
		}
		return r
	}
	envErr := func(env *descriptor.Envelope, err error) error {
		if err != nil {
			return err
		}
		if len(env.Errors) == 0 {
			return nil
		}
		e := env.Errors[0]
		return errors.NewCodedErrorWithDetails(errors.Code(e.Code), e.Message, e.Details)
	}
	data, err := afero.ReadFile(fsys, "src.pulse")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		run      func(withOutput bool) error
		locKey   string
		locIndex int
	}{
		{"Process", func(o bool) error { _, err := p.Process(ctx, req(o)); return err }, "", 0},
		{"ProcessStream", func(o bool) error { _, err := p.ProcessStream(ctx, req(o)); return err }, "", 0},
		{"ProcessStreamResult", func(o bool) error { _, err := p.ProcessStreamResult(ctx, req(o)); return err }, "", 0},
		{"Compose", func(o bool) error {
			_, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{req(false), req(o)}})
			return err
		}, "request", 1},
		{"ComposeParallel", func(o bool) error {
			_, err := p.ComposeParallel(ctx, &ComposedRequest{Requests: []*Request{req(o)}}, ComposeOptions{MaxWorkers: 2})
			return err
		}, "request", 0},
		{"ProcessChain", func(o bool) error {
			_, err := p.ProcessChain(ctx, &ChainRequest{Cohort: cohort, Stages: []*types.ChainStage{{Name: "a", Request: req(o)}}})
			return err
		}, "stage", 0},
		{"Predict", func(o bool) error {
			res, err := p.Predict(ctx, req(o))
			if err != nil || res.Valid {
				return err
			}
			return envErr(p.PredictBytes(ctx, data, req(o)))
		}, "", 0},
		{"PredictEnvelope", func(o bool) error { return envErr(p.PredictEnvelope(ctx, req(o))) }, "", 0},
		{"PredictBytes", func(o bool) error { return envErr(p.PredictBytes(ctx, data, req(o))) }, "", 0},
		{"PredictCompose", func(o bool) error {
			return envErr(p.PredictCompose(ctx, &ComposedRequest{Requests: []*Request{req(o)}}))
		}, "request", 0},
		{"PredictChain", func(o bool) error {
			b := &Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}}}
			if o {
				b.Output = &types.MaterializeOutput{}
			}
			return envErr(p.PredictChain(ctx, &ChainRequest{Cohort: cohort, Stages: []*types.ChainStage{{Name: "a", Request: req(false)}, {Name: "b", Request: b}}}))
		}, "stage", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(false); err != nil {
				t.Fatalf("without output: %v", err)
			}
			ce := assertCode(t, c.run(true), errors.PULSE_MATERIALIZE_OUTPUT_ONLY)
			if ce.Details["slot"] != "output" {
				t.Errorf("details.slot = %v", ce.Details["slot"])
			}
			if c.locKey != "" && ce.Details[c.locKey] != c.locIndex {
				t.Errorf("details.%s = %v, want %d", c.locKey, ce.Details[c.locKey], c.locIndex)
			}
		})
	}
}

// TestMaterializeOutput_HiddenIsUnknownField: an instance whose feature
// profile omits capability:materialize_output refuses a set `output` as
// an unknown field on Materialize itself and on Process, and the slot's
// own refusal codes never surface.
func TestMaterializeOutput_HiddenIsUnknownField(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{FeatureProfile: &FeatureProfile{Features: []string{
		"capability:process", "capability:materialize", "AGG_COUNT", "FEAT_LOG",
	}}})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	req := &Request{Cohort: &types.Cohort{Filename: "src.pulse"},
		Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "spend"}},
		Output:   &types.MaterializeOutput{Exclude: []string{"tags"}}}
	_, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	ce := assertCode(t, err, errors.PULSE_REQUEST_UNKNOWN_FIELD)
	if keys, _ := ce.Details["unknown_keys"].([]string); len(keys) != 1 || keys[0] != "output" {
		t.Errorf("unknown_keys = %v", ce.Details["unknown_keys"])
	}
	assertAbsent(t, fsys, "out.pulse")
	_, err = p.Process(ctx, &Request{Cohort: req.Cohort, Output: req.Output,
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id"}}})
	assertCode(t, err, errors.PULSE_REQUEST_UNKNOWN_FIELD)
}
