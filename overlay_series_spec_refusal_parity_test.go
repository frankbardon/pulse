package pulse_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// Every SERIES-host overlay kind refuses a bad Ref and a bad Scope
// identically in predict and on every runtime arm that folds a
// Request.Overlays SERIES spec — code, message and details, index
// included: the grouped Process hook on the serial (buffered or
// streaming, by the overlays' streamability) and the shard-parallel
// tail, an empty result, a Compose slot, a ProcessChain stage 0 and a
// later stage (both judged by the chain pre-pass before any stage runs,
// located by details.stage), and Pulse.ApplySeriesOverlays. One shared
// rule (overlayhost.SeriesSpecRefusal) serves them all; before it the
// runtime re-checked neither, so a request predict refused ran silently
// (U30 E7-S4).

// seriesGoodSpec is a well-formed spec of kind on the mgFixture host.
func seriesGoodSpec(kind types.OverlayKind) types.OverlaySpec {
	s := types.OverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup}
	switch kind {
	case types.OverlayKindIndexVsRollingMean:
		s.Ref.RollingMean = &types.OverlayRollingMeanRef{}
		s.Params = json.RawMessage(`{"window":1}`)
	case types.OverlayKindZScoreVsRolling:
		// Window 1 is refused: a one-point sample SD (U35 #431).
		s.Ref.RollingMean = &types.OverlayRollingMeanRef{}
		s.Params = json.RawMessage(`{"window":2}`)
	case types.OverlayKindFormula:
		s.Params = json.RawMessage(`{"formula":"value / total"}`)
	case types.OverlayKindYoY:
		s.Ref.YoY = &types.OverlayYoYRef{}
	case types.OverlayKindIndexVsBaseline, types.OverlayKindDeltaVsBaseline:
		s.Ref.BaselineIndex = &types.OverlayBaselineIndexRef{}
	case types.OverlayKindIndexVsSibling, types.OverlayKindDeltaVsSibling:
		s.Ref.Sibling = &types.OverlaySiblingRef{Field: "region", Value: "north"}
	}
	return s
}

// seriesSpecCases derives the rows from the shared rule table: per kind
// an unsupported scope, a foreign Ref arm (Margin, and Matrix — an arm
// predict's old per-kind checks never looked at), the kind's own arm
// missing where it is required, a non-zero Ref.Prior.Lag and an empty
// Ref.Sibling.Field.
func seriesSpecCases(t *testing.T) []specRefusalCase {
	t.Helper()
	kinds := overlayhost.SeriesSpecKinds()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	if len(kinds) != 16 {
		t.Fatalf("SERIES rule table lists %d kinds, want the 16 SERIES-host kinds (OVERLAY_FORMULA included)", len(kinds))
	}
	ref := errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
	var out []specRefusalCase
	for _, kind := range kinds {
		good := seriesGoodSpec(kind)
		name := string(kind) + "/"
		scope := good
		scope.Scope = types.OverlayScopeCell
		margin := good
		margin.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
		matrix := good
		matrix.Ref.Matrix = &types.OverlayMatrixRef{Name: "m"}
		out = append(out,
			specRefusalCase{name + "scope", scope, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
			specRefusalCase{name + "ref_margin", margin, ref},
			specRefusalCase{name + "ref_matrix", matrix, ref})
		switch {
		case good.Ref != (types.OverlayRef{}):
			missing := good
			missing.Ref = types.OverlayRef{}
			out = append(out, specRefusalCase{name + "ref_missing", missing, ref})
		case kind == types.OverlayKindIndexVsPrior || kind == types.OverlayKindDeltaVsPrior || overlayhost.PriorTestKind(kind):
			lag := good
			lag.Ref.Prior = &types.OverlayPriorRef{Lag: 1}
			out = append(out, specRefusalCase{name + "ref_lag", lag, ref})
		}
		if good.Ref.Sibling != nil {
			field := good
			field.Ref.Sibling = &types.OverlaySiblingRef{Value: "north"}
			out = append(out, specRefusalCase{name + "sibling_field", field, ref})
		}
	}
	return out
}

// seriesSpecHost is a grouped request over cohort carrying spec as its
// second overlay (index 1), behind a well-formed INDEX_VS_TOTAL. An
// OVERLAY_YOY host groups by the date field.
func seriesSpecHost(cohort string, spec types.OverlaySpec) *types.Request {
	g := mgRegion()
	if spec.Kind == types.OverlayKindYoY {
		g = &types.Group{Type: types.GROUP_DATE, Field: "day", Params: json.RawMessage(`{"frequency":"monthly"}`)}
	}
	aggs := []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}}
	if overlayhost.PriorTestKind(spec.Kind) {
		// A prior-period test scores a measure of its own family (U47
		// #328): the Welford triple, or a proportion.
		measure := types.AGG_WELFORD
		if !overlayhost.PriorTestUsesWelford(spec.Kind) {
			measure = types.AGG_AVERAGE
		}
		aggs = append(aggs, &types.Aggregation{Type: measure, Field: "score", Label: "m"})
		spec.Params = json.RawMessage(`{"field":"m"}`)
	}
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Groups:       []*types.Group{g},
		Aggregations: aggs,
		Overlays:     []types.OverlaySpec{{Name: "t", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup}, spec},
	}
}

func chainFirstError(t *testing.T, p *pulse.Pulse, req *types.ChainRequest) *descriptor.EnvelopeEntry {
	t.Helper()
	env, err := p.PredictChain(context.Background(), req)
	return firstPredictError(t, env, err)
}

func TestSeriesOverlay_SpecRefusalsMatchPredict(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	ctx := context.Background()

	// Control: every well-formed spec runs on every arm.
	for _, kind := range overlayhost.SeriesSpecKinds() {
		ctl := seriesSpecHost(f.cohort, seriesGoodSpec(kind))
		if env := predictEnvelope(t, f.p, f.fs, f.cohort, ctl); len(env.Errors) != 0 {
			t.Fatalf("control %s predict %+v", kind, env.Errors[0])
		}
		if _, err := f.p.Process(ctx, seriesSpecHost(f.cohort, seriesGoodSpec(kind))); err != nil {
			t.Fatalf("control %s: %v", kind, err)
		}
	}

	streamed, buffered := 0, 0
	for _, c := range seriesSpecCases(t) {
		t.Run(c.name, func(t *testing.T) {
			pe := firstPredictError(t, predictEnvelope(t, f.p, f.fs, f.cohort, seriesSpecHost(f.cohort, c.spec)), nil)
			if pe == nil || pe.Details["index"] != 1 {
				t.Fatalf("predict %+v, want a refusal at index 1", pe)
			}
			// The serial Process hook: streamed when both overlays are
			// streamable kinds, else buffered.
			if s, _ := types.OverlayStreamable(c.spec.Kind); s {
				streamed++
			} else {
				buffered++
			}
			_, err := f.p.Process(ctx, seriesSpecHost(f.cohort, c.spec))
			sameRefusal(t, "process", pe, err, c.want)

			// The shard-parallel tail (ShardWorkers 3 over the archive).
			_, err = f.shard.Process(ctx, seriesSpecHost(f.archive, c.spec))
			sameRefusal(t, "process shards", pe, err, c.want)

			// An empty result: the refusal is a property of the request,
			// raised before the hook's payload guards.
			empty := seriesSpecHost(f.cohort, c.spec)
			empty.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "id", Values: []string{"5000", "6000"}}}
			pEmpty := firstPredictError(t, predictEnvelope(t, f.p, f.fs, f.cohort, empty), nil)
			empty = seriesSpecHost(f.cohort, c.spec)
			empty.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "id", Values: []string{"5000", "6000"}}}
			_, err = f.p.Process(ctx, empty)
			sameRefusal(t, "process empty result", pEmpty, err, c.want)

			// A Compose slot's own Request.Overlays (PredictCompose defers
			// slot overlays to Predict on the slot; located by
			// details.request at runtime).
			_, err = f.p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{seriesSpecHost(f.cohort, c.spec)}})
			sameRefusal(t, "compose slot", pe, unlocated(t, err), c.want)

			// ProcessChain stage 0 and stage 1: predict-chain and the
			// runtime both refuse in the chain pre-pass, located by
			// details.stage.
			for stage := 0; stage < 2; stage++ {
				mk := func() *types.ChainRequest {
					host := seriesSpecHost(f.cohort, c.spec)
					host.Cohort = nil
					base := &types.Request{Groups: []*types.Group{mgRegion()}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "score"}}}
					stages := []*types.ChainStage{{Name: "s0", Request: host}}
					if stage == 1 {
						stages = []*types.ChainStage{{Name: "s0", Request: base}, {Name: "s1", Request: host}}
					}
					return &types.ChainRequest{Cohort: &types.Cohort{Filename: f.cohort}, Stages: stages}
				}
				ce := chainFirstError(t, f.p, mk())
				if ce == nil || ce.Details["stage"] != stage || ce.Code != pe.Code || ce.Message != pe.Message {
					t.Fatalf("chain stage %d: predict-chain %+v, want predict's refusal located at the stage", stage, ce)
				}
				_, err = f.p.ProcessChain(ctx, mk())
				sameRefusal(t, "chain stage", ce, err, c.want)
			}

			// Pulse.ApplySeriesOverlays over a caller-held series.
			_, err = f.p.ApplySeriesOverlays(ctx, &pulse.SeriesOverlayRequest{
				Points:   []pulse.SeriesPoint{{Key: types.AxisKey{"north"}, Value: 1, Present: true}},
				Overlays: seriesSpecHost(f.cohort, c.spec).Overlays,
			})
			sameRefusal(t, "apply series overlays", pe, err, c.want)
		})
	}
	if streamed == 0 || buffered == 0 {
		t.Fatalf("streamed %d / buffered %d rows: both serial arms must be exercised", streamed, buffered)
	}
}
