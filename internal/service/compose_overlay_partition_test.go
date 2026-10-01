package service

import (
	"context"
	stderrors "errors"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E4-S2 wiring: the panel's within-prefix slab gate must be reachable
// from the SERVICE fold, not just from the processing entry point that
// carries it. `service.(*Service).applyComposeOverlays` is the only
// caller the Compose orchestrators use, and it is the only place the
// authored per-slot requests and the extension registry are both in
// hand — a regression that re-pointed it at the request-free
// processing.ApplyComposeOverlays would disarm the gate on every real
// Compose while leaving every processing-package test green.

// panelSlabMatrixResponse builds a minimal 1x1 MATRIX response with a
// row margin, enough for the panel handler to fold if the gate lets it
// through.
func panelSlabMatrixResponse(cell, margin float64) *types.Response {
	return &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY", "GROUP_SET_PER_ELEMENT"}},
				ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      []types.AxisKey{{"a", "x"}},
				ColumnKeys:   []types.AxisKey{{"c0"}},
				Cells:        [][]types.MatrixCell{{{Value: cell, Present: true}}},
				RowMargins:   []types.MatrixCell{{Value: margin, Present: true}},
			},
		},
	}
}

// panelSlabRequest is one authored Compose slot with a fan-out row
// grouper at dim 1.
func panelSlabRequest(label string, inner types.GroupType) *types.Request {
	return &types.Request{
		Label:  label,
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: inner, Field: "brands"},
			},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "col"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

func panelSlabComposed(inner types.GroupType, params map[string]any) (*types.ComposedRequest, []*types.Request, []*types.Response) {
	requests := []*types.Request{
		panelSlabRequest("baseline", inner),
		panelSlabRequest("t1", inner),
	}
	composed := &types.ComposedRequest{
		Requests: requests,
		Overlays: []types.ComposeOverlaySpec{{
			Name:      "panel",
			Kind:      types.OverlayKindPropZPanel,
			Scope:     types.OverlayScopeCell,
			Reference: "baseline",
			Targets:   []string{"t1"},
			Params:    params,
		}},
	}
	responses := []*types.Response{
		panelSlabMatrixResponse(40, 100),
		panelSlabMatrixResponse(30, 100),
	}
	return composed, requests, responses
}

func TestApplyComposeOverlays_PanelSlabGateIsWiredIntoTheServiceFold(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed, requests, responses := panelSlabComposed(types.GROUP_SET_PER_ELEMENT,
		map[string]any{
			"n_source":       types.PanelNSourceRowMarginValueWithin,
			"n_within_depth": 0,
		})

	layers, _, err := svc.applyComposeOverlays(context.Background(), composed, requests, responses)
	if err == nil {
		t.Fatal("the service fold did not gate a fan-out prefix slab; it must be calling" +
			" the request-free processing entry point")
	}
	var ce *pulseerrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if ce.Code != pulseerrors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED {
		t.Fatalf("Code = %q, want %q", ce.Code, pulseerrors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)
	}
	if layers != nil {
		t.Errorf("a refused fold still emitted %d layer(s)", len(layers))
	}
}

// The control: the identical fold over a NON-fan-out inner grouper
// runs. Without this the test above would pass just as happily against
// a gate that refused every panel spec.
func TestApplyComposeOverlays_PanelSlabGateLetsAPartitioningAxisThrough(t *testing.T) {
	cfg := setupTestFS(t, "test.pulse", testSchema(), testRecords())
	svc := New(cfg)

	composed, requests, responses := panelSlabComposed(types.GROUP_CATEGORY,
		map[string]any{
			"n_source":       types.PanelNSourceRowMarginValueWithin,
			"n_within_depth": 0,
		})

	layers, _, err := svc.applyComposeOverlays(context.Background(), composed, requests, responses)
	if err != nil {
		t.Fatalf("a partitioning row axis was refused: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
}
