package pulse_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// Every Process overlay kind on a SCALAR Request.Overlays host — an
// ungrouped request, or one with groups but no aggregations — is refused
// identically in predict and on every runtime arm that can carry it:
// code, message and details. Nothing runs on that host; predict's
// per-kind validators refused it ("requires a MATRIX host", "requires a
// SERIES host") while the runtime ran no overlay and raised nothing
// (U46 E4-S15 "not judged", superseded in U30 E7-S5). One shared rule
// (overlayhost.ScalarSpecRefusal: the kind's Ref / Scope table, then
// the host it needs) serves predict, the Process hooks and the chain
// pre-pass.

// scalarGoodSpec is a spec of kind well-formed for the host it needs
// (OVERLAY_SHARE_OF_TOTAL: its MATRIX dispatch unless series).
func scalarGoodSpec(kind types.OverlayKind, series bool) types.OverlaySpec {
	for _, k := range overlayhost.SeriesSpecKinds() {
		if k == kind && (series || kind != types.OverlayKindShareOfTotal) {
			return seriesGoodSpec(kind)
		}
	}
	if kind == types.OverlayKindFormula {
		return types.OverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeCell, Params: json.RawMessage(`{"formula":"cell"}`)}
	}
	s := types.OverlaySpec{Name: "o", Kind: kind, Scope: overlayhost.CrosstabSpecScopes(kind)[0]}
	if isMarginKind(kind) {
		s.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
	}
	return s
}

// scalarHostCases: per kind, the well-formed spec (refused for want of
// its host), an unsupported scope and a foreign Ref arm (each refused by
// the kind's own table first, where it has one).
func scalarHostCases(t *testing.T, grouped bool) []specRefusalCase {
	t.Helper()
	kinds := overlayhost.ScalarSpecKinds()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	if len(kinds) != 36 {
		t.Fatalf("SCALAR rule table lists %d kinds, want the 36 MATRIX / SERIES Process kinds", len(kinds))
	}
	ref := errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
	var out []specRefusalCase
	for _, kind := range kinds {
		good := scalarGoodSpec(kind, grouped)
		name := string(kind) + "/"
		out = append(out, specRefusalCase{name + "host", good, ref})
		if kind == types.OverlayKindFormula {
			continue
		}
		scope := good
		scope.Scope = "bogus"
		matrix := good
		matrix.Ref.Matrix = &types.OverlayMatrixRef{Name: "m"}
		out = append(out,
			specRefusalCase{name + "scope", scope, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
			specRefusalCase{name + "ref_matrix", matrix, ref})
	}
	return out
}

// scalarHostRequest is an ungrouped aggregation request over cohort
// carrying spec; grouped swaps the aggregation for a grouper (groups, no
// aggregations — still SCALAR).
func scalarHostRequest(cohort string, spec types.OverlaySpec, grouped bool) *types.Request {
	req := &types.Request{
		Cohort:   &types.Cohort{Filename: cohort},
		Overlays: []types.OverlaySpec{spec},
	}
	if grouped {
		req.Groups = []*types.Group{mgRegion()}
	} else {
		req.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}}
	}
	return req
}

func drainStream(ctx context.Context, p *pulse.Pulse, req *types.Request) error {
	it, err := p.ProcessStream(ctx, req)
	if err != nil {
		return err
	}
	defer func() { _ = it.Close() }()
	for {
		_, ok, err := it.Next(ctx)
		if err != nil || !ok {
			return err
		}
	}
}

func TestScalarOverlayHost_RefusalsMatchPredict(t *testing.T) {
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
