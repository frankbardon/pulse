package pulse_test

import (
	"context"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// The non-Request overlay hosts refuse a bad Ref / Scope identically in
// predict and at runtime (code, message and details), each through one
// shared rule (U30 E7-S4):
//
//   - FACET (FacetRequest.Overlays): overlayhost.FacetSpecRefusal — the
//     runtime used to check only Ref.Population.Cohort (in other words),
//     so a foreign Ref arm, an unsupported scope or a Level / Within ran;
//   - Compose (ComposedRequest.Overlays): overlayhost.ComposeSlotRefusals
//     — predict accepted a spec with no Targets the runtime refused, and
//     the two sides worded and detailed the label refusals differently;
//   - whole-chain (ChainRequest.Overlays): overlayhost.ResolveStageRef —
//     the runtime ran a StageRef carrying both Index and Name.
//
// Neither the Compose nor the whole-chain host judges a scope on either
// side, so they have no scope rows.

func TestFacetOverlay_SpecRefusalsMatchPredict(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	ctx := context.Background()
	kinds := []types.OverlayKind{types.OverlayKindChiSqVsPop, types.OverlayKindIndexVsPop, types.OverlayKindKSVsPop, types.OverlayKindZScoreVsPop}
	for _, k := range kinds {
		if !overlayhost.Accepts(overlayhost.Facet, k) {
			t.Fatalf("%s is not a FACET kind", k)
		}
	}
	host := func(kind types.OverlayKind, spec types.OverlaySpec) *types.FacetRequest {
		field := "x"
		if kind == types.OverlayKindChiSqVsPop {
			field = "g"
		}
		good := types.OverlaySpec{Name: "ok", Kind: kind, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: cohort}}}
		return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{field}, Overlays: []types.OverlaySpec{good, spec}}
	}
	for _, kind := range kinds {
		good := types.OverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: cohort}}}
		if env, err := p.PredictFacet(ctx, host(kind, good)); err != nil || len(env.Errors) != 0 {
			t.Fatalf("control %s predict %v %+v", kind, err, env)
		}
		if _, err := p.FacetSchema(ctx, host(kind, good)); err != nil {
			t.Fatalf("control %s: %v", kind, err)
		}
		mut := func(f func(*types.OverlaySpec)) types.OverlaySpec {
			s := good
			s.Ref.Population = &types.OverlayPopulationRef{Cohort: cohort}
			f(&s)
			return s
		}
		ref := errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
		for _, c := range []specRefusalCase{
			{"scope", mut(func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeCell }), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
			{"ref_margin", mut(func(s *types.OverlaySpec) { s.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow} }), ref},
			{"ref_matrix", mut(func(s *types.OverlaySpec) { s.Ref.Matrix = &types.OverlayMatrixRef{Name: "m"} }), ref},
			{"ref_missing", mut(func(s *types.OverlaySpec) { s.Ref = types.OverlayRef{} }), ref},
			{"cohort_empty", mut(func(s *types.OverlaySpec) { s.Ref.Population.Cohort = "" }), ref},
			{"level", mut(func(s *types.OverlaySpec) { s.Level = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
			{"within", mut(func(s *types.OverlaySpec) { s.Within = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		} {
			t.Run(string(kind)+"/"+c.name, func(t *testing.T) {
				env, err := p.PredictFacet(ctx, host(kind, c.spec))
				pe := firstPredictError(t, env, err)
				if pe == nil || pe.Details["index"] != 1 {
					t.Fatalf("predict %+v, want a refusal at index 1", pe)
				}
				_, err = p.FacetSchema(ctx, host(kind, c.spec))
				sameRefusal(t, "facet", pe, err, c.want)
			})
		}
	}
}

func TestComposeOverlay_SlotRefusalsMatchPredict(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	ctx := context.Background()
	var kinds []types.OverlayKind
	for _, k := range types.AllOverlayKinds() {
		if hs := overlayhost.Hosts(k); len(hs) > 0 && overlayhost.Accepts(overlayhost.Compose, k) {
			kinds = append(kinds, k)
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	if len(kinds) == 0 {
		t.Fatal("no Compose-host kind")
	}
	slot := func(label string) *types.Request {
		return &types.Request{Label: label, Cohort: &types.Cohort{Filename: cohort},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}
	}
	mk := func(spec types.ComposeOverlaySpec) *types.ComposedRequest {
		return &types.ComposedRequest{Requests: []*types.Request{slot("a"), slot("b")}, Overlays: []types.ComposeOverlaySpec{spec}}
	}
	for _, kind := range kinds {
		for _, c := range []struct {
			name      string
			reference string
			targets   []string
			want      errors.Code
		}{
			{"reference_empty", "", []string{"b"}, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN},
			{"reference_unknown", "zz", []string{"b"}, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN},
			{"targets_none", "a", nil, errors.PULSE_OVERLAY_TARGET_UNKNOWN},
			{"target_empty", "a", []string{""}, errors.PULSE_OVERLAY_TARGET_UNKNOWN},
			{"target_unknown", "a", []string{"b", "zz"}, errors.PULSE_OVERLAY_TARGET_UNKNOWN},
		} {
			t.Run(string(kind)+"/"+c.name, func(t *testing.T) {
				spec := types.ComposeOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: c.reference, Targets: c.targets}
				env, err := p.PredictCompose(ctx, mk(spec))
				pe := firstPredictError(t, env, err)
				if pe == nil || pe.Details["index"] != 0 {
					t.Fatalf("predict %+v, want a label refusal at index 0", pe)
				}
				_, err = p.Compose(ctx, mk(spec))
				sameRefusal(t, "compose", pe, err, c.want)
			})
		}
	}
}

func TestChainOverlay_StageRefRefusalsMatchPredict(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	ctx := context.Background()
	zero, one, nine := 0, 1, 9
	mk := func(spec *types.ChainOverlaySpec) *types.ChainRequest {
		return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: []*types.ChainStage{
			{Name: "s0", Request: &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "v"}}}},
			{Name: "s1", Request: &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "v"}}}},
		}, Overlays: []*types.ChainOverlaySpec{spec}}
	}
	for _, kind := range []types.OverlayKind{types.OverlayKindDeltaVsStage, types.OverlayKindIndexVsStage} {
		good := types.ChainOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Ref: types.StageRef{Index: &zero}, Target: types.StageRef{Index: &one}}
		g := good
		if env, err := p.PredictChain(ctx, mk(&g)); err != nil || len(env.Errors) != 0 {
			t.Fatalf("control %s predict %v %+v", kind, err, env)
		}
		g = good
		if _, err := p.ProcessChain(ctx, mk(&g)); err != nil {
			t.Fatalf("control %s: %v", kind, err)
		}
		refU, tgtU := errors.PULSE_OVERLAY_REFERENCE_UNKNOWN, errors.PULSE_OVERLAY_TARGET_UNKNOWN
		for _, c := range []struct {
			name string
			mut  func(*types.ChainOverlaySpec)
			want errors.Code
		}{
			{"ref_both", func(s *types.ChainOverlaySpec) { s.Ref = types.StageRef{Index: &zero, Name: "s0"} }, refU},
			{"ref_neither", func(s *types.ChainOverlaySpec) { s.Ref = types.StageRef{} }, refU},
			{"ref_index", func(s *types.ChainOverlaySpec) { s.Ref = types.StageRef{Index: &nine} }, refU},
			{"ref_name", func(s *types.ChainOverlaySpec) { s.Ref = types.StageRef{Name: "zz"} }, refU},
			{"target_both", func(s *types.ChainOverlaySpec) { s.Target = types.StageRef{Index: &one, Name: "s1"} }, tgtU},
			{"target_index", func(s *types.ChainOverlaySpec) { s.Target = types.StageRef{Index: &nine} }, tgtU},
			{"target_name", func(s *types.ChainOverlaySpec) { s.Target = types.StageRef{Name: "zz"} }, tgtU},
		} {
			t.Run(string(kind)+"/"+c.name, func(t *testing.T) {
				build := func() *types.ChainRequest {
					s := good
					c.mut(&s)
					return mk(&s)
				}
				pe := chainFirstError(t, p, build())
				if pe == nil || pe.Details["index"] != 0 {
					t.Fatalf("predict-chain %+v, want a stage-ref refusal at index 0", pe)
				}
				_, err := p.ProcessChain(ctx, build())
				sameRefusal(t, "process chain", pe, err, c.want)
			})
		}
	}
}

// TestChainStage_CrosstabRefusedBothSides: a ProcessChain stage carrying
// Request.Crosstab is refused PULSE_CHAIN_NOT_MERGEABLE (reason
// mergegate.CrosstabStageReason, details {stage_index, stage_name}) by
// predict-chain and the runtime alike, whatever else the stage carries.
// Before U30 E7-S4 a later stage beside aggregations dropped the
// crosstab and its overlays silently, a stage 0 beside aggregations hit
// Process's PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS predict-chain never
// raised, and both sides accepted the former.
func TestChainStage_CrosstabRefusedBothSides(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	ctx := context.Background()
	xtab := func() *types.CrosstabSpec {
		return &types.CrosstabSpec{Rows: []*types.Group{mgRegion()}, Columns: []*types.Group{mgSex()},
			Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "score", Label: "n"}}
	}
	chi := []types.OverlaySpec{{Name: "chi", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix}}
	for _, c := range []struct {
		name  string
		stage int
		build func() *types.ChainRequest
	}{
		{"stage 0 crosstab only", 0, func() *types.ChainRequest {
			return &types.ChainRequest{Cohort: &types.Cohort{Filename: f.cohort}, Stages: []*types.ChainStage{
				{Name: "x", Request: &types.Request{Crosstab: xtab(), Overlays: chi}}}}
		}},
		{"stage 0 crosstab beside aggregations", 0, func() *types.ChainRequest {
			return &types.ChainRequest{Cohort: &types.Cohort{Filename: f.cohort}, Stages: []*types.ChainStage{
				{Name: "x", Request: &types.Request{Crosstab: xtab(), Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}}}}}
		}},
		{"stage 1 crosstab beside aggregations", 1, func() *types.ChainRequest {
			req := chChain(f, types.OverlayKindIndexVsStage, "", "zz_n")
			chCrosstabStage1(req)
			req.Stages[1].Request.Overlays = chi
			return req
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			pe := chainFirstError(t, f.p, c.build())
			if pe == nil || pe.Code != string(errors.PULSE_CHAIN_NOT_MERGEABLE) || pe.Details["stage_index"] != c.stage {
				t.Fatalf("predict-chain %+v, want PULSE_CHAIN_NOT_MERGEABLE at stage %d", pe, c.stage)
			}
			if pe.Message != "chain stage is not mergeable: a crosstab stage is excluded (a crosstab answers a matrix, not the rows the next stage reads)" {
				t.Fatalf("predict-chain message %q", pe.Message)
			}
			_, err := f.p.ProcessChain(ctx, c.build())
			sameRefusal(t, "process chain", pe, err, errors.PULSE_CHAIN_NOT_MERGEABLE)
		})
	}
}

// TestFacetOverlay_WeightRefusedLikePredict: facets are never weighted,
// so a facet overlay's own `weight` used to be ignored silently. A set
// weight (a field name or a {field, kind} object) is now
// PULSE_WEIGHT_UNSUPPORTED at predict and runtime alike — one error,
// same message and details; absent and `null` (the explicit "run
// unweighted" opt-out, which is what a facet does) still run (#259).
func TestFacetOverlay_WeightRefusedLikePredict(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for _, kind := range []types.OverlayKind{types.OverlayKindChiSqVsPop, types.OverlayKindIndexVsPop, types.OverlayKindKSVsPop, types.OverlayKindZScoreVsPop} {
		field := "x"
		if kind == types.OverlayKindChiSqVsPop {
			field = "g"
		}
		mk := func(w types.SlotWeight) *types.FacetRequest {
			spec := types.OverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: cohort}}, Weight: w}
			good := types.OverlaySpec{Name: "ok", Kind: kind, Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: cohort}}}
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{field}, Overlays: []types.OverlaySpec{good, spec}}
		}
		for name, w := range map[string]types.SlotWeight{"absent": {}, "null": types.NullSlotWeight()} {
			if env, err := p.PredictFacet(ctx, mk(w)); err != nil || len(env.Errors) != 0 {
				t.Fatalf("%s %s predict: %v %+v", kind, name, err, env)
			}
			if _, err := p.FacetSchema(ctx, mk(w)); err != nil {
				t.Fatalf("%s %s runtime: %v", kind, name, err)
			}
		}
		for name, w := range map[string]types.SlotWeight{
			"field":     types.SlotWeightField("x"),
			"frequency": types.SlotWeightOf(types.WeightSpec{Field: "x", Kind: types.WeightKindFrequency}),
		} {
			t.Run(string(kind)+"/"+name, func(t *testing.T) {
				env, err := p.PredictFacet(ctx, mk(w))
				pe := firstPredictError(t, env, err)
				if len(env.Errors) != 1 || pe.Details["slot"] != "overlays[1]" || pe.Details["operator"] != string(kind) || pe.Details["field"] != "x" {
					t.Fatalf("predict errors %+v, want one PULSE_WEIGHT_UNSUPPORTED on overlays[1]", env.Errors)
				}
				_, err = p.FacetSchema(ctx, mk(w))
				sameRefusal(t, "facet", pe, err, errors.PULSE_WEIGHT_UNSUPPORTED)
			})
		}
	}
}
