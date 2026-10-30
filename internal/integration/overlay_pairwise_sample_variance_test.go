package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// TestPairwiseMoment_PopulationVarianceCellsMatchWelford (E4-S3):
// OVERLAY_PAIRWISE_WELCH_T / _TWO_MEANS_Z read {mean, variance, n} off
// each cell. AGG_VARIANCE / AGG_STDDEV cells carry the POPULATION
// variance m2 / n, AGG_WELFORD the sample variance m2 / (n - 1) the
// tests are defined on; the leg reader now converts the population
// cells, so the overlay over either equals the overlay over AGG_WELFORD
// cells — the same p-values, bit for bit (both read m2 / (n - 1)) — on
// the fused and buffered arms and in a Compose slot. It used to test on
// the population variance (too small by (n - 1) / n).
func TestPairwiseMoment_PopulationVarianceCellsMatchWelford(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	buffered, err := pulse.New(pulse.Options{FS: f.fs, DisableCrosstabFusion: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	host := func(kind types.OverlayKind, scope types.OverlayScope, cell types.AggregationType, cohort string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}},
				Cell:    &types.Aggregation{Type: cell, Field: "score", Label: "v"},
			},
			Overlays: []types.OverlaySpec{{Name: "pw", Kind: kind, Scope: scope}},
		}
	}
	payload := func(t *testing.T, resp *types.Response) string {
		t.Helper()
		if resp == nil || len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil {
			t.Fatalf("want one matrix layer, got %+v", resp)
		}
		present := 0
		for _, row := range resp.Overlays[0].Payload.Matrix.Cells {
			for _, c := range row {
				if c.Present {
					present++
				}
			}
		}
		if present == 0 {
			t.Fatal("no pair scored: the fixture must carry n >= 2 cells")
		}
		b, err := json.Marshal(resp.Overlays[0].Payload)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	arms := []struct {
		name   string
		inst   *pulse.Pulse
		cohort string
	}{
		{"fused", f.p, f.cohort},
		{"buffered", buffered, f.cohort},
		{"shards", f.shard, f.archive},
	}
	for _, kind := range []types.OverlayKind{types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ} {
		for _, scope := range []types.OverlayScope{types.OverlayScopeColumn, types.OverlayScopeRow} {
			for _, arm := range arms {
				ref, err := arm.inst.Process(ctx, host(kind, scope, types.AGG_WELFORD, arm.cohort))
				if err != nil {
					t.Fatal(err)
				}
				want := payload(t, ref)
				for _, cell := range []types.AggregationType{types.AGG_VARIANCE, types.AGG_STDDEV} {
					req := host(kind, scope, cell, arm.cohort)
					if env := predictEnvelope(t, arm.inst, f.fs, arm.cohort, req); len(env.Errors) != 0 {
						t.Fatalf("%s %s: predict %v", kind, cell, envCodes(env))
					}
					resp, err := arm.inst.Process(ctx, req)
					if err != nil {
						t.Fatalf("%s %s %s: %v", arm.name, kind, cell, err)
					}
					if got := payload(t, resp); got != want {
						t.Errorf("%s %s %s over %s:\n got %s\nwant %s (AGG_WELFORD)", arm.name, kind, scope, cell, got, want)
					}
					out, err := arm.inst.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{host(kind, scope, cell, arm.cohort)}})
					if err != nil {
						t.Fatalf("compose: %v", err)
					}
					if got := payload(t, out.Responses[0]); got != want {
						t.Errorf("%s compose slot %s %s over %s differs from AGG_WELFORD", arm.name, kind, scope, cell)
					}
				}
			}
		}
	}
}
