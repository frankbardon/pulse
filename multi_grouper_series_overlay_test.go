package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// mgSeriesOverlaySpecs is every SERIES overlay that reads the host as
// ONE ordered series: the lag-1 prior, the rolling window, the
// positional baseline and the first-match sibling.
func mgSeriesOverlaySpecs() []types.OverlaySpec {
	window := json.RawMessage(`{"window": 2}`)
	sib := &types.OverlaySiblingRef{Field: "region", Value: "north"}
	return []types.OverlaySpec{
		{Name: "dp", Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup},
		{Name: "ip", Kind: types.OverlayKindIndexVsPrior, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Prior: &types.OverlayPriorRef{}}},
		{Name: "irm", Kind: types.OverlayKindIndexVsRollingMean, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "zr", Kind: types.OverlayKindZScoreVsRolling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "ib", Kind: types.OverlayKindIndexVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "db", Kind: types.OverlayKindDeltaVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "is", Kind: types.OverlayKindIndexVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: sib}},
		{Name: "ds", Kind: types.OverlayKindDeltaVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: sib}},
	}
}

// TestMultiGrouper_SeriesOverlaysRefused: over (region, sex) the host
// is the six composite rows north|F 7, north|M 5, south|F 4, south|M 6,
// west|F 3, west|M 5. Before the refusal OVERLAY_DELTA_VS_PRIOR gave
// south|F a delta of -1 (4 minus north|M's 5) where its own bucket has
// no prior; rolling windows straddled buckets the same way, the
// positional baseline indexed every region to north|F, and a sibling
// named by one key column resolved to the first matching row for every
// row. Each such kind is now refused
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (details.groupers) by
// predict and by every runtime mode, as OVERLAY_YOY is; over the
// region grouper alone each still runs, and DELTA_VS_PRIOR's figures
// are the plain lag-1 deltas.
func TestMultiGrouper_SeriesOverlaysRefused(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	for _, spec := range mgSeriesOverlaySpecs() {
		t.Run(string(spec.Kind), func(t *testing.T) {
			build := func(groups ...*types.Group) *types.Request {
				req := mgRequest(f, groups)
				req.Overlays = []types.OverlaySpec{spec}
				return req
			}
			// One grouper: accepted and folded. Region counts 12, 10, 8.
			if env := predictEnvelope(t, f.p, f.fs, f.cohort, build(mgRegion())); len(env.Errors) > 0 {
				t.Fatalf("one grouper: predict %+v", env.Errors)
			}
			resp, err := f.p.Process(context.Background(), build(mgRegion()))
			if err != nil {
				t.Fatalf("one grouper: %v", err)
			}
			if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Series == nil || len(resp.Overlays[0].Payload.Series.Entries) != 3 {
				t.Fatalf("one grouper: overlays %+v, want one 3-entry series layer", resp.Overlays)
			}
			if spec.Kind == types.OverlayKindDeltaVsPrior {
				entries := resp.Overlays[0].Payload.Series.Entries
				if st := entries[0].Summary.Statistic; st == nil || !math.IsNaN(*st) {
					t.Errorf("first delta = %v, want NaN (no prior)", st)
				}
				for i, want := range []float64{-2, -2} {
					if st := entries[i+1].Summary.Statistic; st == nil || *st != want {
						t.Errorf("delta[%d] = %v, want %v", i+1, st, want)
					}
				}
			}

			for name, groups := range map[string]func() []*types.Group{
				"region outer": func() []*types.Group { return []*types.Group{mgRegion(), mgSex()} },
				"region inner": func() []*types.Group { return []*types.Group{mgSex(), mgRegion()} },
			} {
				env := predictEnvelope(t, f.p, f.fs, f.cohort, build(groups()...))
				if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) || env.Errors[0].Details["groupers"] != 2 {
					t.Errorf("%s: predict errors %+v, want PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE groupers 2", name, env.Errors)
				}
				for _, mode := range mgModes {
					t.Run(mode.name+"/"+name, func(t *testing.T) {
						if !mode.admits(groups()) {
							t.Skip("mode admits mergeable groupers only")
						}
						switch mode.name {
						case "Streaming":
							// mgRequireStreamable predicts first, and predict
							// refuses; run the engine directly, and a
							// streamable kind must still take the streaming arm.
							resp, err := f.p.Process(context.Background(), build(groups()...))
							if resp != nil && len(resp.Overlays) > 0 {
								t.Fatalf("a layer over composite rows: %+v", resp.Overlays)
							}
							requireCodedError(t, err, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
							// An error exit records no arm. Routing reads only
							// the kinds' streamability flags, so a twin carrying
							// the streamable, non-refused INDEX_VS_TOTAL proves
							// a streamable refused kind rode the streaming arm.
							if types.OverlayStreamability[spec.Kind] {
								twin := mgRequest(f, groups())
								twin.Overlays = []types.OverlaySpec{{Name: "it", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup}}
								if _, err := f.p.Process(context.Background(), twin); err != nil {
									t.Fatalf("streamable twin: %v", err)
								}
								if f.lastArm() != observe.ArmStreaming {
									t.Errorf("streamable twin ran on arm %q, want streaming", f.lastArm())
								}
							}
							return
						case "ProcessStream":
							iter, err := f.p.ProcessStream(context.Background(), build(groups()...))
							if err == nil {
								defer func() { _ = iter.Close() }()
								for {
									var ok bool
									if _, ok, err = iter.Next(context.Background()); err != nil || !ok {
										break
									}
								}
							}
							requireCodedError(t, err, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
							return
						}
						resp, err := mode.run(t, f, build(groups()...))
						if resp != nil && len(resp.Overlays) > 0 {
							t.Fatalf("a layer over composite rows: %+v", resp.Overlays)
						}
						ce := requireCodedError(t, err, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
						if ce.Details["groupers"] != 2 || ce.Details["kind"] != string(spec.Kind) {
							t.Errorf("details = %v, want kind %s groupers 2", ce.Details, spec.Kind)
						}
					})
				}
			}
		})
	}
}
