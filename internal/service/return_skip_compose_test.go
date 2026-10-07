package service

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestReturnSkipsComputation_Compose extends TestReturnSkipsComputation
// to Compose (U18 E2-S4): each slot runs under its own compute plan,
// built where the whole ComposedRequest is in hand. A slot a Compose
// overlay names (reference or target) keeps every Components sub-part
// whatever its `return` excludes; an unnamed slot skips what its
// selection drops. Serial and parallel, buffered and fused crosstab
// arms. The service never shapes, so a slot's Components block is
// present iff it was computed.
func TestReturnSkipsComputation_Compose(t *testing.T) {
	standard := &types.Return{Preset: types.ReturnPresetStandard}
	compose := func(ret *types.Return, overlaid bool) *types.ComposedRequest {
		slot := func(label string) *types.Request {
			r := skipCrosstabRequest(false)
			r.Label = label
			r.Return = ret
			return r
		}
		c := &types.ComposedRequest{Requests: []*types.Request{slot("a"), slot("b"), slot("c")}}
		if overlaid {
			c.Overlays = []types.ComposeOverlaySpec{{
				Name: "idx", Kind: types.OverlayKindIndexVsRef, Scope: types.OverlayScopeCell,
				Reference: "a", Targets: []string{"b"},
			}}
		}
		return c
	}
	cases := []struct {
		name     string
		ret      *types.Return
		overlaid bool
		computed []bool // per slot: Components computed
	}{
		{name: "absent", computed: []bool{true, true, true}},
		{name: "absent_overlaid", overlaid: true, computed: []bool{true, true, true}},
		{name: "standard", ret: standard, computed: []bool{false, false, false}},
		{name: "standard_overlay_names_a_b", ret: standard, overlaid: true, computed: []bool{true, true, false}},
	}
	for _, arm := range crosstabSkipArms {
		if arm.join {
			continue
		}
		for _, parallel := range []bool{false, true} {
			t.Run(arm.name+map[bool]string{false: "/serial", true: "/parallel"}[parallel], func(t *testing.T) {
				svc := New(crosstabJoinFixture(t))
				svc.SetDisableCrosstabFusion(arm.disableFusion)
				assertCrosstabArm(t, svc, arm)
				var base *types.ComposedResponse // absent_overlaid
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						before := processing.WorkStats()
						var (
							out *types.ComposedResponse
							err error
						)
						if parallel {
							out, err = svc.ComposeParallel(context.Background(), compose(tc.ret, tc.overlaid), ComposeOptions{MaxWorkers: 3})
						} else {
							out, err = svc.Compose(context.Background(), compose(tc.ret, tc.overlaid))
						}
						if err != nil {
							t.Fatalf("compose: %v", err)
						}
						delta := processing.WorkStats().Sub(before)
						anyComputed := false
						for i, r := range out.Responses {
							got := r.Components != nil && r.Components.Crosstab != nil
							if got != tc.computed[i] {
								t.Errorf("slot %d: components computed=%v, want %v", i, got, tc.computed[i])
							}
							anyComputed = anyComputed || tc.computed[i]
						}
						if anyComputed && delta.CrosstabCellComponentMaps <= 0 {
							t.Errorf("a slot computes components, yet no cell map was built")
						}
						if !anyComputed && (delta.CrosstabCellComponentMaps != 0 || delta.AuxMarginAccumulators != 0) {
							t.Errorf("every slot skips components, yet maps=%d aux=%d were built",
								delta.CrosstabCellComponentMaps, delta.AuxMarginAccumulators)
						}
						if tc.name == "absent_overlaid" {
							if len(out.Overlays) == 0 {
								t.Fatal("no compose overlay layer: the fixture proves nothing")
							}
							base = out
							return
						}
						if !tc.overlaid || base == nil {
							return
						}
						// Kept numbers: a vetoed slot's components and the
						// overlay layer equal the unshaped run's.
						for i := range out.Responses {
							if tc.computed[i] && !reflect.DeepEqual(base.Responses[i].Components, out.Responses[i].Components) {
								t.Errorf("slot %d: vetoed components moved", i)
							}
						}
						if !jsonEqual(t, base.Overlays, out.Overlays) {
							t.Errorf("compose overlay layer moved under slot returns")
						}
					})
				}
			})
		}
	}
}

// TestReturnSkipsComputation_Chain: every stage runs under its own
// compute plan — a later stage (runChainStage bypasses Process) skips
// what its `return` excludes — and a chain overlay, which reads its
// stages' data only, vetoes no skip: its layer is unchanged.
func TestReturnSkipsComputation_Chain(t *testing.T) {
	svc, _ := skipArms()[0].build(t)
	zero, one := 0, 1
	chain := func(ret0, ret1 *types.Return, overlaid bool) *types.ChainRequest {
		stage0 := skipRequest("", true, false)
		stage0.Cohort = nil
		stage0.Return = ret0
		stage1 := &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "sum", Label: "total"}},
			Return:       ret1,
		}
		req := &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "skip.pulse"},
			Stages: []*types.ChainStage{{Name: "s0", Request: stage0}, {Name: "s1", Request: stage1}},
		}
		if overlaid {
			req.Overlays = []*types.ChainOverlaySpec{{
				Name: "idx", Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeTotal,
				Ref: types.StageRef{Index: &zero}, Target: types.StageRef{Index: &one},
			}}
		}
		return req
	}
	standard := &types.Return{Preset: types.ReturnPresetStandard}
	cases := []struct {
		name       string
		ret0, ret1 *types.Return
		overlaid   bool
		computed   [2]bool
	}{
		{name: "absent", computed: [2]bool{true, true}},
		{name: "absent_overlaid", overlaid: true, computed: [2]bool{true, true}},
		{name: "stage1_standard", ret1: standard, computed: [2]bool{true, false}},
		{name: "stage0_standard", ret0: standard, computed: [2]bool{false, true}},
		{name: "both_standard", ret0: standard, ret1: standard},
		{name: "both_standard_overlaid", ret0: standard, ret1: standard, overlaid: true},
	}
	var base *types.ChainResponse
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := processing.WorkStats()
			out, err := svc.ProcessChain(context.Background(), chain(tc.ret0, tc.ret1, tc.overlaid))
			if err != nil {
				t.Fatalf("ProcessChain: %v", err)
			}
			delta := processing.WorkStats().Sub(before)
			for i, r := range out.Stages {
				if got := r.Components != nil; got != tc.computed[i] {
					t.Errorf("stage %d: components computed=%v, want %v", i, got, tc.computed[i])
				}
				if len(r.Data) == 0 {
					t.Fatalf("stage %d: no rows — the fixture proves nothing", i)
				}
			}
			builds := delta.AggComponentBuilds
			if (tc.computed[0] || tc.computed[1]) && builds <= 0 {
				t.Errorf("a stage computes components, yet AggComponentBuilds delta %d", builds)
			}
			if !tc.computed[0] && !tc.computed[1] && builds != 0 {
				t.Errorf("every stage skips components, yet built %d", builds)
			}
			if tc.name == "absent_overlaid" {
				if len(out.Overlays) == 0 {
					t.Fatal("no chain overlay layer: the fixture proves nothing")
				}
				base = out
				return
			}
			if base == nil {
				return
			}
			for i := range out.Stages {
				if !jsonEqual(t, base.Stages[i].Data, out.Stages[i].Data) {
					t.Errorf("stage %d: data moved under a stage return", i)
				}
				if tc.computed[i] && !reflect.DeepEqual(base.Stages[i].Components, out.Stages[i].Components) {
					t.Errorf("stage %d: kept components moved", i)
				}
			}
			if tc.overlaid && !jsonEqual(t, base.Overlays, out.Overlays) {
				t.Errorf("chain overlay layer moved under stage returns")
			}
		})
	}
}

// TestReturnSkipsComputation_CrosstabOverlayVeto: a request overlay
// that reads its host's components (descx.OverlayReadsHostComponents)
// keeps components.crosstab under `standard` — computed, every kept
// figure equal to the return-absent run's — while the auxiliary margin
// figures, which no overlay reads, still skip (fused: setCompute is the
// one guard). A payload-only overlay vetoes nothing.
func TestReturnSkipsComputation_CrosstabOverlayVeto(t *testing.T) {
	build := func(kind types.OverlayKind, scope types.OverlayScope, ret *types.Return) *types.Request {
		r := skipCrosstabRequest(false)
		r.Crosstab.Cell = &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"}
		r.Overlays = []types.OverlaySpec{{Name: "o", Kind: kind, Scope: scope}}
		if kind == types.OverlayKindShareOfRow {
			r.Overlays[0].Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
		}
		r.Return = ret
		return r
	}
	standard := &types.Return{Preset: types.ReturnPresetStandard}
	for _, arm := range crosstabSkipArms {
		if arm.join {
			continue
		}
		t.Run(arm.name, func(t *testing.T) {
			svc := New(crosstabJoinFixture(t))
			svc.SetDisableCrosstabFusion(arm.disableFusion)
			for _, tc := range []struct {
				kind  types.OverlayKind
				scope types.OverlayScope
				zero  map[string]bool
			}{
				{types.OverlayKindPairwisePropZ, types.OverlayScopeRow, map[string]bool{"aux": true}},
				{types.OverlayKindShareOfRow, types.OverlayScopeRow, map[string]bool{"maps": true, "aux": true}},
			} {
				t.Run(string(tc.kind), func(t *testing.T) {
					cohort, err := svc.Open(context.Background(), "left.pulse")
					if err != nil {
						t.Fatal(err)
					}
					if ok, why := processing.CanFuseCrosstab(build(tc.kind, tc.scope, nil), cohort.Schema(), svc.extensions); arm.wantFused && !ok {
						t.Fatalf("fusion gate declined (%s): the arm would silently run buffered", why)
					}
					baseResp, _ := runCrosstabSkip(t, svc, build(tc.kind, tc.scope, nil))
					if len(baseResp.Overlays) != 1 {
						t.Fatalf("no overlay layer: the fixture proves nothing: %+v", baseResp.Overlays)
					}
					resp, delta := runCrosstabSkip(t, svc, build(tc.kind, tc.scope, standard))
					assertCrosstabCounters(t, delta, tc.zero)
					assertKeptCrosstabEqual(t, baseResp.Components.Crosstab, resp, tc.zero)
					if !jsonEqual(t, baseResp.Overlays, resp.Overlays) {
						t.Errorf("overlay layer moved under standard")
					}
					if resp.Components != nil && (resp.Components.Aggregations != nil || resp.Components.Run != nil) {
						t.Errorf("veto kept more than components.crosstab: %+v", resp.Components)
					}
				})
			}
		})
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ab, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(ab) == string(bb)
}
