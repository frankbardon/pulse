//go:build heavy

package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

func TestScalarOverlayHost_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	ctx := context.Background()

	// Control: the hosts run without overlays.
	for _, grouped := range []bool{false, true} {
		ctl := scalarHostRequest(f.cohort, types.OverlaySpec{}, grouped)
		ctl.Overlays = nil
		if _, err := f.p.Process(ctx, ctl); err != nil {
			t.Fatalf("control (grouped %v): %v", grouped, err)
		}
	}

	for _, grouped := range []bool{false, true} {
		host := "ungrouped/"
		if grouped {
			host = "groups_no_aggregations/"
		}
		for _, c := range scalarHostCases(t, grouped) {
			t.Run(host+c.name, func(t *testing.T) {
				mk := func() *types.Request { return scalarHostRequest(f.cohort, c.spec, grouped) }
				pe := firstPredictError(t, predictEnvelope(t, f.p, f.fs, f.cohort, mk()), nil)
				if pe == nil || pe.Details["index"] != 0 {
					t.Fatalf("predict %+v, want a refusal at index 0", pe)
				}
				want := overlayhost.ScalarSpecRefusal(&c.spec, c.spec.Kind, grouped, 0)
				if want == nil || pe.Message != want.Message {
					t.Fatalf("predict %q, want the shared rule's %v", pe.Message, want)
				}

				_, err := f.p.Process(ctx, mk())
				sameRefusal(t, "process", pe, err, c.want)

				sameRefusal(t, "process stream", pe, drainStream(ctx, f.p, mk()), c.want)

				shard := mk()
				shard.Cohort.Filename = f.archive
				_, err = f.shard.Process(ctx, shard)
				sameRefusal(t, "process shards", pe, err, c.want)

				// Parallel buffered decode (DecodeWorkers 3 over the
				// on-disk cohort, filler filtered out).
				dec, decCohort := f.decodeEngine(t)
				decReq := mk()
				decReq.Cohort.Filename = decCohort
				decReq.Filterers = []*types.Filterer{mgDecodeFilter()}
				_, err = dec.Process(ctx, decReq)
				sameRefusal(t, "process decode workers", pe, err, c.want)

				empty := mk()
				empty.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "id", Values: []string{"5000", "6000"}}}
				_, err = f.p.Process(ctx, empty)
				sameRefusal(t, "process empty result", pe, err, c.want)

				_, err = f.p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{mk()}})
				sameRefusal(t, "compose slot", pe, unlocated(t, err), c.want)

				if grouped {
					return
				}
				// ProcessChain stage 0 and stage 1: both sides refuse in
				// the chain pre-pass, located by details.stage.
				for stage := 0; stage < 2; stage++ {
					mkChain := func() *types.ChainRequest {
						h := mk()
						h.Cohort = nil
						base := &types.Request{Groups: []*types.Group{mgRegion()}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "score"}}}
						stages := []*types.ChainStage{{Name: "s0", Request: h}}
						if stage == 1 {
							stages = []*types.ChainStage{{Name: "s0", Request: base}, {Name: "s1", Request: h}}
						}
						return &types.ChainRequest{Cohort: &types.Cohort{Filename: f.cohort}, Stages: stages}
					}
					ce := chainFirstError(t, f.p, mkChain())
					if ce == nil || ce.Details["stage"] != stage || ce.Code != pe.Code || ce.Message != pe.Message {
						t.Fatalf("chain stage %d: predict-chain %+v, want predict's refusal located at the stage", stage, ce)
					}
					_, err = f.p.ProcessChain(ctx, mkChain())
					sameRefusal(t, "chain stage", ce, err, c.want)
				}
			})
		}
	}
}
