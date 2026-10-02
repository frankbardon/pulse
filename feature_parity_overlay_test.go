package pulse

// The regression and overlay rows of the hidden-name parity harness
// (feature_parity_harness_test.go). Regressions resolve through their
// own registry; overlay kinds through one handler table per host —
// crosstab (MATRIX), grouped Process (SERIES), Compose, ProcessChain
// and FacetSchema — so each host gets its own category, and the
// Compose and ProcessChain hosts, whose overlays ride the composed /
// chain request rather than a stage Request, get their own entry
// points.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// composeStubFallback is why a never-registered Compose overlay kind
// succeeds: the COMPOSE dispatcher answers a kind with no handler with
// an empty stub layer rather than an error.
const composeStubFallback = "the compose dispatcher answers a kind with no handler with an empty stub layer"

// parityCrosstabOverlay replaces r's grouped shape with a region ×
// region count crosstab carrying spec.
func parityCrosstabOverlay(r *types.Request, f parityFields, spec types.OverlaySpec) {
	r.Groups, r.Aggregations = nil, nil
	r.Crosstab = &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: f.num},
	}
	r.Overlays = append(r.Overlays, spec)
}

// composeOverlay adds one COMPOSE-host spec comparing slot "b" against
// slot "a".
func composeOverlay(scope types.OverlayScope) func(r *ComposedRequest, op string, f parityFields) {
	return func(r *ComposedRequest, op string, f parityFields) {
		r.Overlays = append(r.Overlays, types.ComposeOverlaySpec{
			Kind: types.OverlayKind(op), Scope: scope, Reference: "a", Targets: []string{"b"},
		})
	}
}

// regOverlayParityCategories are the regression category and one
// overlay category per host (and per kind-keyed gate a host runs
// before its table lookup).
var regOverlayParityCategories = []parityCategory{
	{
		// REG_OLS is streamable: on an ungrouped request it is the
		// streaming-vs-buffered route a hidden name must not take.
		name:       "regression",
		candidates: []string{"REG_OLS", "REG_GLM"},
		never:      "REG_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups = nil
			r.Regressions = append(r.Regressions, &types.RegressionSpec{
				Type: types.RegressionType(op), Name: "probe", Target: f.num, Predictors: []string{f.num},
			})
		},
	},
	{
		// MATRIX host: the Level/Within gate and the table lookup.
		name:       "overlay_crosstab",
		candidates: []string{"OVERLAY_SHARE_OF_COL", "OVERLAY_SHARE_OF_TOTAL"},
		never:      "OVERLAY_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			parityCrosstabOverlay(r, f, types.OverlaySpec{Name: "probe", Kind: types.OverlayKind(op), Scope: types.OverlayScopeCell})
		},
	},
	{
		// MATRIX host: OVERLAY_FORMULA is dispatched before the table.
		name:       "overlay_formula",
		candidates: []string{"OVERLAY_FORMULA"},
		never:      "OVERLAY_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			parityCrosstabOverlay(r, f, types.OverlaySpec{
				Name: "probe", Kind: types.OverlayKind(op), Scope: types.OverlayScopeCell,
				Params: json.RawMessage(`{"formula":"cell * 2"}`),
			})
		},
	},
	{
		// SERIES host. OVERLAY_INDEX_VS_PRIOR is streamable, so it is
		// the streaming-vs-buffered route a hidden kind must not take.
		name:       "overlay_series",
		candidates: []string{"OVERLAY_INDEX_VS_PRIOR", "OVERLAY_INDEX_VS_TOTAL"},
		never:      "OVERLAY_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Overlays = append(r.Overlays, types.OverlaySpec{Name: "probe", Kind: types.OverlayKind(op), Scope: types.OverlayScopeGroup})
		},
		neverOK: "the SERIES fold skips an ungrouped host without reading the kind",
	},
	{
		name:       "overlay_compose",
		candidates: []string{"OVERLAY_INDEX_VS_REF", "OVERLAY_DELTA_VS_REF"},
		never:      "OVERLAY_NEVER_REGISTERED",
		compose:    composeOverlay(types.OverlayScopeGroup),
		neverOK:    composeStubFallback,
	},
	{
		// A MATRIX-only Compose kind over SERIES slots: the shape gate
		// refuses the registered kind and passes an unknown one.
		name:       "overlay_compose_matrix",
		candidates: []string{"OVERLAY_RANK", "OVERLAY_T_CELL"},
		never:      "OVERLAY_NEVER_REGISTERED",
		compose:    composeOverlay(types.OverlayScopeCell),
		neverOK:    composeStubFallback,
	},
	{
		// The multi-layer Compose table.
		name:       "overlay_compose_panel",
		candidates: []string{"OVERLAY_PANEL_INDEX_VS_REF"},
		never:      "OVERLAY_NEVER_REGISTERED",
		compose:    composeOverlay(types.OverlayScopeGroup),
		neverOK:    composeStubFallback,
	},
	{
		name:       "overlay_chain",
		candidates: []string{"OVERLAY_INDEX_VS_STAGE", "OVERLAY_DELTA_VS_STAGE"},
		never:      "OVERLAY_NEVER_REGISTERED",
		chain: func(r *ChainRequest, op string, f parityFields) {
			zero := 0
			r.Overlays = append(r.Overlays, &types.ChainOverlaySpec{
				Name: "probe", Kind: types.OverlayKind(op), Scope: types.OverlayScopeGroup,
				Ref: types.StageRef{Index: &zero}, Target: types.StageRef{Index: &zero},
			})
		},
	},
	{
		name:       "overlay_facet",
		candidates: []string{"OVERLAY_INDEX_VS_POP", "OVERLAY_CHISQ_VS_POP"},
		never:      "OVERLAY_NEVER_REGISTERED",
		facet: func(r *types.FacetRequest, op string, f parityFields) {
			r.Overlays = append(r.Overlays, types.OverlaySpec{
				Name: "probe", Kind: types.OverlayKind(op), Scope: types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: parityCohort}},
				Params: json.RawMessage(`{"field":"` + f.cat + `"}`),
			})
			r.Fields = append(r.Fields, f.cat)
		},
	},
}

// composeOverlayRequest is a two-slot ComposedRequest over the
// instance's base request, with category c's spec added.
func composeOverlayRequest(h *parityHost, c parityCategory, op string) (*ComposedRequest, bool) {
	if c.compose == nil {
		return nil, false
	}
	slot := func(label string) *Request {
		req := h.base()
		req.Cohort = &types.Cohort{Filename: h.cohort}
		req.Label = label
		return req
	}
	composed := &ComposedRequest{Requests: []*Request{slot("a"), slot("b")}}
	c.compose(composed, op, h.fields)
	return composed, true
}

// overlayParityEntryPoints drive the entry points whose own top-level
// slot (not a stage Request) carries the overlay.
var overlayParityEntryPoints = []parityEntryPoint{
	{name: "Compose/overlays", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		req, ok := composeOverlayRequest(h, c, op)
		if !ok {
			return nil, false
		}
		resp, err := h.p.Compose(context.Background(), req)
		return parityOutcome(resp, err), true
	}},
	{name: "ComposeParallel/overlays", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		req, ok := composeOverlayRequest(h, c, op)
		if !ok {
			return nil, false
		}
		resp, err := h.p.ComposeParallel(context.Background(), req, ComposeOptions{MaxWorkers: 2})
		return parityOutcome(resp, err), true
	}},
	{name: "ProcessChain/overlays", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		if c.chain == nil || h.chainBase == nil {
			return nil, false
		}
		req := &ChainRequest{
			Cohort: &types.Cohort{Filename: h.cohort},
			Stages: []*types.ChainStage{{Name: "base", Request: h.chainBase()}},
		}
		c.chain(req, op, h.fields)
		resp, err := h.p.ProcessChain(context.Background(), req)
		return parityOutcome(resp, err), true
	}},
}
