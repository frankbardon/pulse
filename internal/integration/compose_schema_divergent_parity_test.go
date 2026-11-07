package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCompose_SchemaDivergentMatchesPredict: a built-in Compose overlay
// over two crosstab slots whose row axes produce the SAME keys from
// different grouper kinds (GROUP_CATEGORY on region vs labelled
// GROUP_NUMERIC_RANGES on qty) passes the runtime key-set gate and is
// PULSE_OVERLAY_SCHEMA_DIVERGENT at predict-compose and at runtime with
// one message and one details map (overlayhost.SchemaDivergentRefusal
// over each slot's authored axis schema) — the implicit Total column's
// kind included in both canonical strings.
func TestCompose_SchemaDivergentMatchesPredict(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, pulse.Extensions{})
	ctx := context.Background()
	slot := func(label string, row *types.Group) *types.Request {
		return &types.Request{Label: label, Cohort: cohort, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{row},
			Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "qty", Label: "n"},
		}}
	}
	ranges := json.RawMessage(`{"ranges":[{"label":"north","end":3},{"label":"south","start":3,"end":6},{"label":"east","start":6,"end":9},{"label":"west","start":9}]}`)
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			slot("a", &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}),
			slot("b", &types.Group{Type: types.GROUP_NUMERIC_RANGES, Field: "qty", Params: ranges}),
		},
		Overlays: []types.ComposeOverlaySpec{{Name: "ix", Kind: types.OverlayKindIndexVsRef, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}}},
	}
	env, err := p.PredictCompose(ctx, req)
	if err != nil {
		t.Fatalf("PredictCompose: %v", err)
	}
	if len(env.Errors) != 1 {
		t.Fatalf("predict-compose errors = %+v, want exactly one", env.Errors)
	}
	_, err = p.Compose(ctx, req)
	sameRefusal(t, "compose", env.Errors[0], err, perr.PULSE_OVERLAY_SCHEMA_DIVERGENT)
	want := map[string]any{
		"index": 0, "kind": string(types.OverlayKindIndexVsRef), "reference": "a", "target_label": "b",
		"reference_schema": "GROUP_CATEGORY/" + types.CrosstabTotalColumnType,
		"target_schema":    "GROUP_NUMERIC_RANGES/" + types.CrosstabTotalColumnType,
	}
	for k, v := range want {
		if got := env.Errors[0].Details[k]; got != v {
			t.Errorf("details[%s] = %v, want %v", k, got, v)
		}
	}
}
