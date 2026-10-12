package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// An overlay kind authored on a slot that does not run it — a Compose
// kind such as OVERLAY_T_VS_REF on a grouped Process request, a Process
// kind on ComposedRequest.Overlays, a whole-chain or FACET kind anywhere
// else — once passed predict and then failed at runtime with
// PULSE_OVERLAY_KIND_UNKNOWN, or (on a Compose host) silently answered
// an empty stub layer (E4-S12). Every slot now refuses it with the one
// shared refusal, PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, Details
// {index, kind, host, hosts}, identical on predict and runtime.
//
// Every case runs PREDICT FIRST on a fresh request, and puts a valid
// spec at index 0 where the slot offers one, so the refused index is 1.

// whRefused returns the expected refusal Details and message for kind
// on host at index.
func whRefused(host overlayhost.Host, kind types.OverlayKind, index int) (map[string]any, string) {
	ce := overlayhost.Refusal(host, kind, kind, index)
	if ce == nil {
		return nil, ""
	}
	return ce.Details, ce.Message
}

// whCheck compares predict's first error and the runtime error against
// the expected refusal: code, Details (through JSON, so a []string and a
// decoded []any compare alike) and message.
func whCheck(t *testing.T, host overlayhost.Host, kind types.OverlayKind, index int, pe *descriptor.EnvelopeEntry, runErr error) {
	t.Helper()
	wantDetails, wantMsg := whRefused(host, kind, index)
	want := nsJSON(t, wantDetails)
	if pe == nil || pe.Code != string(perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) ||
		nsJSON(t, pe.Details) != want || pe.Message != wantMsg {
		t.Fatalf("predict = %+v, want PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE %s %q", pe, want, wantMsg)
	}
	if runErr == nil {
		t.Fatal("runtime accepted the spec predict refuses")
	}
	ce := codeOf(t, runErr)
	if ce.Code != perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || nsJSON(t, ce.Details) != want || ce.Message != wantMsg {
		t.Fatalf("runtime = %s %s %q, want predict's %s %q", ce.Code, nsJSON(t, ce.Details), ce.Message, want, wantMsg)
	}
}

func whFirstError(t *testing.T, env *descriptor.Envelope, err error) *descriptor.EnvelopeEntry {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 {
		return nil
	}
	return env.Errors[0]
}

// whWrongKinds returns every catalog kind host does not run.
func whWrongKinds(t *testing.T, host overlayhost.Host) []types.OverlayKind {
	t.Helper()
	var out []types.OverlayKind
	for _, k := range types.AllOverlayKinds() {
		if !overlayhost.Accepts(host, k) {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		t.Fatalf("vacuous: %s runs every kind", host)
	}
	return out
}

// TestOverlayWrongHost_ProcessParity: every non-Process kind on
// Request.Overlays, over a grouped SERIES host, a crosstab MATRIX host
// and an ungrouped one.
func TestOverlayWrongHost_ProcessParity(t *testing.T) {
	ctx := context.Background()
	p, data := nsFixture(t, pulse.Options{})
	shapes := []struct {
		name  string
		build func(kind types.OverlayKind) *pulse.Request
		index int
	}{
		{"series", func(kind types.OverlayKind) *pulse.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "ns.pulse"}, Groups: nsGroups(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Overlays: []types.OverlaySpec{
					{Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup},
					{Kind: kind, Scope: types.OverlayScopeGroup},
				}}
		}, 1},
		{"matrix", func(kind types.OverlayKind) *pulse.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "ns.pulse"},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}},
					Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "score"},
					Shape:   types.CrosstabShapeMatrix,
				},
				Overlays: []types.OverlaySpec{
					{Kind: types.OverlayKindShareOfCol, Scope: types.OverlayScopeCell, Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}}},
					{Kind: kind, Scope: types.OverlayScopeCell},
				}}
		}, 1},
		// A long-shape crosstab has no MATRIX payload for the hook to
		// fold: the refusal is a property of the request, raised anyway.
		{"long", func(kind types.OverlayKind) *pulse.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "ns.pulse"},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}},
					Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "score"},
					Shape:   types.CrosstabShapeLong,
				},
				Overlays: []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeCell}}}
		}, 0},
		{"ungrouped", func(kind types.OverlayKind) *pulse.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "ns.pulse"},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Overlays:     []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeTotal}}}
		}, 0},
	}
	for _, sh := range shapes {
		// Control: the leading valid spec alone runs clean on both sides.
		if sh.index == 1 {
			ctl := sh.build("")
			ctl.Overlays = ctl.Overlays[:1]
			if pe := predictCodedError(t, p, data, ctl); pe != nil {
				t.Fatalf("%s control: predict = %+v", sh.name, pe)
			}
			if _, err := p.Process(ctx, ctl); err != nil {
				t.Fatalf("%s control: Process: %v", sh.name, err)
			}
		}
		for _, kind := range whWrongKinds(t, overlayhost.Request) {
			t.Run(fmt.Sprintf("%s/%s", sh.name, kind), func(t *testing.T) {
				pe := predictCodedError(t, p, data, sh.build(kind))
				_, err := p.Process(ctx, sh.build(kind))
				whCheck(t, overlayhost.Request, kind, sh.index, pe, err)
			})
		}
	}
}

// TestOverlayWrongHost_ComposeParity: every kind Compose does not run on
// ComposedRequest.Overlays (it once answered an empty stub layer).
func TestOverlayWrongHost_ComposeParity(t *testing.T) {
	ctx := context.Background()
	p, _ := nsFixture(t, pulse.Options{})
	build := func(kind types.OverlayKind) *pulse.ComposedRequest {
		slot := func(label string) *types.Request {
			return &types.Request{Label: label, Cohort: &types.Cohort{Filename: "ns.pulse"}, Groups: nsGroups(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}}
		}
		return &pulse.ComposedRequest{Requests: []*types.Request{slot("a"), slot("b")},
			Overlays: []types.ComposeOverlaySpec{
				{Name: "ok", Kind: types.OverlayKindIndexVsRef, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"}},
				{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"}},
			}}
	}
	ctl := build("")
	ctl.Overlays = ctl.Overlays[:1]
	if env, err := p.PredictCompose(ctx, ctl); whFirstError(t, env, err) != nil {
		t.Fatalf("control: predict = %+v", env.Errors)
	}
	if _, err := p.Compose(ctx, ctl); err != nil {
		t.Fatalf("control: Compose: %v", err)
	}
	for _, kind := range whWrongKinds(t, overlayhost.Compose) {
		t.Run(string(kind), func(t *testing.T) {
			env, err := p.PredictCompose(ctx, build(kind))
			pe := whFirstError(t, env, err)
			_, runErr := p.Compose(ctx, build(kind))
			whCheck(t, overlayhost.Compose, kind, 1, pe, runErr)
		})
	}
}

// TestOverlayWrongHost_ChainParity: every non-chain catalog kind on
// ChainRequest.Overlays (once PULSE_OVERLAY_KIND_UNKNOWN on both sides,
// with Details that differed by a runtime-only `host`).
func TestOverlayWrongHost_ChainParity(t *testing.T) {
	ctx := context.Background()
	p, _ := nsFixture(t, pulse.Options{})
	zero := 0
	build := func(kind types.OverlayKind) *pulse.ChainRequest {
		return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "ns.pulse"}, Stages: []*types.ChainStage{
			{Name: "profile", Request: &types.Request{Groups: nsGroups(), Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}}},
		}, Overlays: []*types.ChainOverlaySpec{
			{Name: "ok", Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeGroup, Ref: types.StageRef{Index: &zero}},
			{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Ref: types.StageRef{Index: &zero}},
		}}
	}
	ctl := build("")
	ctl.Overlays = ctl.Overlays[:1]
	if env, err := p.PredictChain(ctx, ctl); whFirstError(t, env, err) != nil {
		t.Fatalf("control: predict = %+v", env.Errors)
	}
	if _, err := p.ProcessChain(ctx, ctl); err != nil {
		t.Fatalf("control: ProcessChain: %v", err)
	}
	for _, kind := range whWrongKinds(t, overlayhost.Chain) {
		t.Run(string(kind), func(t *testing.T) {
			env, err := p.PredictChain(ctx, build(kind))
			pe := whFirstError(t, env, err)
			_, runErr := p.ProcessChain(ctx, build(kind))
			whCheck(t, overlayhost.Chain, kind, 1, pe, runErr)
		})
	}
}

// TestOverlayWrongHost_FacetParity: every non-FACET kind on
// FacetRequest.Overlays (predict refused it under this code already;
// the runtime answered a population or PULSE_OVERLAY_KIND_UNKNOWN
// error).
func TestOverlayWrongHost_FacetParity(t *testing.T) {
	ctx := context.Background()
	p, _ := nsFixture(t, pulse.Options{})
	build := func(kind types.OverlayKind) *pulse.FacetRequest {
		params, _ := json.Marshal(map[string]any{"field": "region"})
		return &pulse.FacetRequest{Cohort: &types.Cohort{Filename: "ns.pulse"}, Fields: []string{"region"},
			Overlays: []types.OverlaySpec{
				{Kind: types.OverlayKindIndexVsPop, Scope: types.OverlayScopeGroup, Params: params,
					Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "ns.pulse"}}},
				{Kind: kind, Scope: types.OverlayScopeGroup, Params: params},
			}}
	}
	ctl := build("")
	ctl.Overlays = ctl.Overlays[:1]
	if env, err := p.PredictFacet(ctx, ctl); whFirstError(t, env, err) != nil {
		t.Fatalf("control: predict = %+v", env.Errors)
	}
	if _, err := p.FacetSchema(ctx, ctl); err != nil {
		t.Fatalf("control: FacetSchema: %v", err)
	}
	for _, kind := range whWrongKinds(t, overlayhost.Facet) {
		t.Run(string(kind), func(t *testing.T) {
			env, err := p.PredictFacet(ctx, build(kind))
			pe := whFirstError(t, env, err)
			_, runErr := p.FacetSchema(ctx, build(kind))
			whCheck(t, overlayhost.Facet, kind, 1, pe, runErr)
		})
	}
}
