package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// U18 rank 5 (E3-S1): an overlay layer the `return` selection excludes
// is never folded — no handler runs (processing.WorkStats
// OverlayLayerRuns), no refusal or warning it would raise is raised —
// unless a resolved multiplicity family claims it, in which case it is
// computed so every kept p_adjusted (and its family size m) is the full
// run's. The skip happens inside the arm: dispatch (canStream's overlay
// coupling, crosstab fusion) reads the original request.

var excludeOverlays = &types.Return{Exclude: []string{"overlays"}}

func overlayRuns(t *testing.T, run func()) int64 {
	t.Helper()
	before := processing.WorkStats()
	run()
	return processing.WorkStats().Sub(before).OverlayLayerRuns
}

// TestReturnSkipsComputation_Overlays: the crosstab host (buffered and
// fused). Excluded: no layer folds and Overlays is nil, every other
// part byte-identical. A skipped component-reading layer stops vetoing
// components.crosstab, so `standard` + an excluded Fisher layer builds
// no component map while `standard` alone does.
func TestReturnSkipsComputation_Overlays(t *testing.T) {
	fused, buffered := overlayFoldService(t)
	for name, svc := range map[string]*Service{"fused": fused, "buffered": buffered} {
		t.Run(name, func(t *testing.T) {
			ok, why := processing.CanFuseCrosstab(overlayCrosstabRequest(false), multSchema(), svc.extensions)
			if name == "fused" && !ok {
				t.Fatalf("fusion gate declined (%s): the fused arm would silently run buffered", why)
			}
			var base, shaped *types.Response
			absent := overlayRuns(t, func() { base = mustProcess(t, svc, overlayCrosstabRequest(false)) })
			if absent != int64(len(base.Overlays)) || absent != 4 {
				t.Fatalf("return absent: %d layer folds, %d layers; want 4 — the fixture proves nothing", absent, len(base.Overlays))
			}
			req := overlayCrosstabRequest(false)
			req.Return = excludeOverlays
			if got := overlayRuns(t, func() { shaped = mustProcess(t, svc, req) }); got != 0 {
				t.Errorf("overlays excluded, yet %d layer(s) folded", got)
			}
			if shaped.Overlays != nil {
				t.Errorf("overlays excluded, yet present: %+v", shaped.Overlays)
			}
			b := *base
			b.Overlays = nil
			if !bytes.Equal(mustMarshal(t, &b), mustMarshal(t, shaped)) {
				t.Error("a kept part moved when the overlays were skipped")
			}

			// The skipped Fisher layer (it reads components.crosstab)
			// no longer vetoes the components skip.
			std := &types.Return{Preset: types.ReturnPresetStandard}
			before := processing.WorkStats()
			req = overlayCrosstabRequest(false)
			req.Return = std
			mustProcess(t, svc, req)
			if processing.WorkStats().Sub(before).CrosstabCellComponentMaps <= 0 {
				t.Fatal("standard with a kept Fisher layer built no component map: the veto control proves nothing")
			}
			before = processing.WorkStats()
			req = overlayCrosstabRequest(false)
			req.Return = &types.Return{Preset: types.ReturnPresetStandard, Exclude: []string{"overlays"}}
			mustProcess(t, svc, req)
			if d := processing.WorkStats().Sub(before); d.CrosstabCellComponentMaps != 0 || d.OverlayLayerRuns != 0 {
				t.Errorf("standard + overlays excluded: maps=%d folds=%d; want 0 — a skipped layer still vetoed", d.CrosstabCellComponentMaps, d.OverlayLayerRuns)
			}
		})
	}
}

// TestReturnSkipsOverlays_SeriesArmUnchanged: the series host. A
// buffered-only kind (OVERLAY_DELTA_VS_BASELINE) routes the grouped
// scan buffered (canStream); excluding it skips the fold but must not
// move the request onto the streaming arm. The work-counter profile of
// the run (fold counter aside) tells the arms apart — the control
// request with the overlay REMOVED streams and differs.
func TestReturnSkipsOverlays_SeriesArmUnchanged(t *testing.T) {
	_, svc := overlayFoldService(t)
	mk := func(ret *types.Return, overlays bool) *types.Request {
		r := &types.Request{
			Cohort:       &types.Cohort{Filename: "m.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
			Return:       ret,
		}
		if overlays {
			r.Overlays = []types.OverlaySpec{{
				Name: "d", Kind: types.OverlayKindDeltaVsBaseline, Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0}},
			}}
		}
		return r
	}
	if processing.CanStreamRequest(mk(nil, true), multSchema()) || !processing.CanStreamRequest(mk(nil, false), multSchema()) {
		t.Fatal("fixture: the overlay must force the buffered arm and its absence allow streaming")
	}
	profile := func(req *types.Request) (*types.Response, processing.WorkStatsSnapshot) {
		before := processing.WorkStats()
		resp := mustProcess(t, svc, req)
		d := processing.WorkStats().Sub(before)
		return resp, d
	}
	base, absent := profile(mk(nil, true))
	if absent.OverlayLayerRuns != 1 || len(base.Overlays) != 1 {
		t.Fatalf("return absent: %d folds, %d layers; want 1", absent.OverlayLayerRuns, len(base.Overlays))
	}
	shaped, excluded := profile(mk(excludeOverlays, true))
	_, streamed := profile(mk(excludeOverlays, false))
	if excluded.OverlayLayerRuns != 0 || shaped.Overlays != nil {
		t.Errorf("overlays excluded, yet %d fold(s), layers %+v", excluded.OverlayLayerRuns, shaped.Overlays)
	}
	absent.OverlayLayerRuns = 0
	if streamed == absent {
		t.Fatal("the streaming control's work profile equals the buffered run's: the arm check proves nothing")
	}
	if excluded != absent {
		t.Errorf("excluding the overlay moved the run's arm\n  excluded %+v\n  absent   %+v", excluded, absent)
	}
	b := *base
	b.Overlays = nil
	if !bytes.Equal(mustMarshal(t, &b), mustMarshal(t, shaped)) {
		t.Error("a kept part moved when the overlay was skipped")
	}
}

// TestReturnSkipsOverlays_MultiplicityVeto: a `request` family pools the
// tests with the inferential layers, so excluding the overlays still
// folds every MEMBER layer (the descriptive SHARE_OF_ROW, no member, is
// skipped) and every kept test's p_adjusted and m equal the full run's.
// Without the block the same selection folds nothing.
func TestReturnSkipsOverlays_MultiplicityVeto(t *testing.T) {
	fused, buffered := overlayFoldService(t)
	block := &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyRequest}
	for name, svc := range map[string]*Service{"fused": fused, "buffered": buffered} {
		t.Run(name, func(t *testing.T) {
			mk := func(ret *types.Return) *types.Request {
				r := overlayCrosstabRequest(true)
				r.Multiplicity = block
				r.Return = ret
				return r
			}
			full := mustProcess(t, svc, mk(nil))
			var shaped *types.Response
			if got := overlayRuns(t, func() { shaped = mustProcess(t, svc, mk(excludeOverlays)) }); got != 3 {
				t.Errorf("multiplicity veto: %d layer folds; want the 3 member layers", got)
			}
			if len(full.Tests) == 0 || full.Tests[0].PAdjusted == nil || full.Tests[0].Multiplicity == nil {
				t.Fatal("full run carries no corrected test: the fixture proves nothing")
			}
			if !bytes.Equal(mustMarshal(t, full.Tests), mustMarshal(t, shaped.Tests)) {
				t.Errorf("kept tests moved under the overlay skip\n got  %s\n want %s", mustMarshal(t, shaped.Tests), mustMarshal(t, full.Tests))
			}
			for i := 0; i < 3; i++ {
				if !bytes.Equal(mustMarshal(t, full.Overlays[i]), mustMarshal(t, shaped.Overlays[i])) {
					t.Errorf("member layer %d differs from the full run's", i)
				}
			}
			if !reflect.DeepEqual(shaped.Overlays[3], types.OverlayLayer{}) {
				t.Errorf("non-member layer 3 was folded: %+v", shaped.Overlays[3])
			}

			req := overlayCrosstabRequest(true)
			req.Return = excludeOverlays
			if got := overlayRuns(t, func() { mustProcess(t, svc, req) }); got != 0 {
				t.Errorf("no multiplicity block: %d layer folds; want 0", got)
			}
		})
	}
}

// TestReturnSkipsOverlays_ErrorRule pins FR-25: an excluded layer is
// not computed, so the refusal it would raise is not raised (predict
// stays the validator); kept, the same request still refuses.
func TestReturnSkipsOverlays_ErrorRule(t *testing.T) {
	build := func(ret *types.Return) *types.Request {
		r := skipCrosstabRequest(false)
		r.Crosstab.Cell = &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"}
		r.Overlays = []types.OverlaySpec{{Name: "pz", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow}}
		r.Return = ret
		return r
	}
	for _, arm := range crosstabSkipArms {
		if arm.join {
			continue
		}
		t.Run(arm.name, func(t *testing.T) {
			svc := New(crosstabJoinFixture(t))
			svc.SetDisableCrosstabFusion(arm.disableFusion)
			svc.SetDisableComponents(true)
			_, err := svc.Process(context.Background(), build(nil))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_COMPONENTS_REQUIRED {
				t.Fatalf("kept pairwise overlay without components: err %v; want PULSE_OVERLAY_COMPONENTS_REQUIRED", err)
			}
			resp, err := svc.Process(context.Background(), build(excludeOverlays))
			if err != nil {
				t.Fatalf("excluded overlay still refused: %v", err)
			}
			if resp.Overlays != nil {
				t.Errorf("excluded overlay present: %+v", resp.Overlays)
			}
		})
	}
}

// TestReturnSkipsOverlays_Compose: the Compose-host overlays follow the
// Compose-level `return`; skipped, they fold nothing and veto no slot's
// Components. A `compose` multiplicity family keeps them (all of them —
// a Compose-host spec may emit several layers) and every slot member,
// and every kept p_adjusted equals the full run's. Serial and parallel.
func TestReturnSkipsOverlays_Compose(t *testing.T) {
	svc := composeMultService(t)
	compose := func(slotRet, composeRet *types.Return, mult *types.Multiplicity) *types.ComposedRequest {
		c := composeMultRequest()
		for _, r := range c.Requests {
			r.Return = slotRet
		}
		c.Return = composeRet
		c.Multiplicity = mult
		return c
	}
	run := func(parallel bool, c *types.ComposedRequest) (*types.ComposedResponse, processing.WorkStatsSnapshot) {
		before := processing.WorkStats()
		var (
			out *types.ComposedResponse
			err error
		)
		if parallel {
			out, err = svc.ComposeParallel(context.Background(), c, ComposeOptions{MaxWorkers: 3})
		} else {
			out, err = svc.Compose(context.Background(), c)
		}
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		return out, processing.WorkStats().Sub(before)
	}
	stdNoOverlays := &types.Return{Preset: types.ReturnPresetStandard, Exclude: []string{"overlays"}}
	block := &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyCompose}
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "serial", true: "parallel"}[parallel], func(t *testing.T) {
			full, d := run(parallel, compose(nil, nil, nil))
			if len(full.Overlays) == 0 || d.OverlayLayerRuns != 3*4+1 {
				t.Fatalf("return absent: %d folds, %d compose layers; want 13 folds", d.OverlayLayerRuns, len(full.Overlays))
			}
			// Control: a kept Compose overlay vetoes its slots' components.
			_, d = run(parallel, compose(stdNoOverlays, nil, nil))
			if d.CrosstabCellComponentMaps <= 0 || d.OverlayLayerRuns != 1 {
				t.Fatalf("kept compose overlay: maps=%d folds=%d; want maps>0, 1 fold", d.CrosstabCellComponentMaps, d.OverlayLayerRuns)
			}
			out, d := run(parallel, compose(stdNoOverlays, excludeOverlays, nil))
			if d.OverlayLayerRuns != 0 || d.CrosstabCellComponentMaps != 0 || out.Overlays != nil {
				t.Errorf("every overlay excluded: folds=%d maps=%d layers=%v; want 0 / 0 / nil",
					d.OverlayLayerRuns, d.CrosstabCellComponentMaps, out.Overlays)
			}
			for i := range full.Responses {
				if !bytes.Equal(mustMarshal(t, full.Responses[i].Crosstab), mustMarshal(t, out.Responses[i].Crosstab)) {
					t.Errorf("slot %d crosstab moved", i)
				}
			}

			// The compose family: members keep folding, kept numbers equal.
			fullM, _ := run(parallel, compose(nil, nil, block))
			outM, d := run(parallel, compose(excludeOverlays, excludeOverlays, block))
			if d.OverlayLayerRuns != 3*3+1 {
				t.Errorf("compose family veto: %d folds; want 10 (3 member layers per slot + the compose layer)", d.OverlayLayerRuns)
			}
			if !bytes.Equal(mustMarshal(t, fullM.Overlays), mustMarshal(t, outM.Overlays)) {
				t.Error("vetoed compose layer moved")
			}
			for i := range fullM.Responses {
				if fullM.Responses[i].Tests[0].PAdjusted == nil {
					t.Fatal("full run carries no corrected test: the fixture proves nothing")
				}
				if !bytes.Equal(mustMarshal(t, fullM.Responses[i].Tests), mustMarshal(t, outM.Responses[i].Tests)) {
					t.Errorf("slot %d: kept tests moved under the overlay skip", i)
				}
			}
		})
	}
}

// resolveComputePlan per layer: an excluded overlay slot computes
// exactly the layers a resolved multiplicity family claims — the
// request's own plan, or a Compose slot's share carried on ctx.
func TestResolveComputePlan_OverlayLayers(t *testing.T) {
	svc := &Service{}
	ctx := context.Background()
	req := &types.Request{Overlays: make([]types.OverlaySpec, 3)}
	ret := mustResolveReturn(t, &types.Request{Return: excludeOverlays})
	member := descx.ResolvedMultiplicity{Member: true}
	mult := &descx.MultiplicityPlan{Overlays: []descx.ResolvedMultiplicity{{}, member, {}}}
	layers := func(p processing.ComputePlan) []bool {
		return []bool{p.ComputesOverlay(0), p.ComputesOverlay(1), p.ComputesOverlay(2)}
	}
	if got := layers(svc.resolveComputePlan(ctx, req, nil, mult)); !reflect.DeepEqual(got, []bool{true, true, true}) {
		t.Errorf("no return: %v; want every layer", got)
	}
	if got := svc.resolveComputePlan(ctx, req, ret, nil); got.Overlays {
		t.Errorf("excluded, no family: %+v; want the slot off", got)
	}
	if got := layers(svc.resolveComputePlan(ctx, req, ret, mult)); !reflect.DeepEqual(got, []bool{false, true, false}) {
		t.Errorf("excluded, layer 1 a member: %v", got)
	}
	slotCtx := composeSlotContext(ctx, nil, &descx.ComposeMultiplicityPlan{Requests: []*descx.MultiplicityPlan{nil, mult}}, 1)
	if got := layers(svc.resolveComputePlan(slotCtx, req, ret, nil)); !reflect.DeepEqual(got, []bool{false, true, false}) {
		t.Errorf("Compose slot share: %v", got)
	}
}

func mustProcess(t *testing.T, svc *Service, req *types.Request) *types.Response {
	t.Helper()
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}
