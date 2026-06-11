package types_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestOverlaySpec_RoundTrip verifies the universal OverlaySpec shape
// (the acceptance-criterion example: an index-vs-margin cell-scoped
// spec referencing the row margin) survives JSON marshal/unmarshal
// structurally intact.
func TestOverlaySpec_RoundTrip(t *testing.T) {
	spec := types.OverlaySpec{
		Name:  "i_row",
		Kind:  types.OverlayKindIndexVsMargin,
		Scope: types.OverlayScopeCell,
		Ref: types.OverlayRef{
			Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal OverlaySpec: %v", err)
	}

	var got types.OverlaySpec
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal OverlaySpec: %v", err)
	}

	if !reflect.DeepEqual(spec, got) {
		t.Fatalf("OverlaySpec round-trip mismatch:\n want: %+v\n  got: %+v\n json: %s",
			spec, got, string(data))
	}

	// Spot-check the discriminated reference survived: only Margin must
	// be populated, every other family pointer must be nil.
	if got.Ref.Margin == nil || got.Ref.Margin.Axis != types.MarginAxisRow {
		t.Fatalf("Ref.Margin lost in round-trip: %+v", got.Ref)
	}
	if got.Ref.Sibling != nil || got.Ref.BaselineIndex != nil ||
		got.Ref.Population != nil || got.Ref.Stage != nil || got.Ref.Slot != nil {
		t.Fatalf("non-Margin Ref pointer unexpectedly populated: %+v", got.Ref)
	}
}

// TestOverlaySpec_OnRequest verifies the Request.Overlays slot
// marshals/unmarshals through the existing Request envelope without
// disturbing the other slots — additive contract.
func TestOverlaySpec_OnRequest(t *testing.T) {
	req := types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "id"},
		},
		Overlays: []types.OverlaySpec{
			{
				Name:  "i_row",
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal Request: %v", err)
	}

	var got types.Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal Request: %v", err)
	}

	if len(got.Overlays) != 1 {
		t.Fatalf("Overlays length = %d, want 1", len(got.Overlays))
	}
	if got.Overlays[0].Kind != types.OverlayKindIndexVsMargin {
		t.Fatalf("Overlays[0].Kind = %q, want %q",
			got.Overlays[0].Kind, types.OverlayKindIndexVsMargin)
	}
	if got.Overlays[0].Scope != types.OverlayScopeCell {
		t.Fatalf("Overlays[0].Scope = %q, want %q",
			got.Overlays[0].Scope, types.OverlayScopeCell)
	}
	if got.Overlays[0].Ref.Margin == nil ||
		got.Overlays[0].Ref.Margin.Axis != types.MarginAxisRow {
		t.Fatalf("Overlays[0].Ref.Margin lost: %+v", got.Overlays[0].Ref)
	}
	if got.Crosstab == nil {
		t.Fatalf("Crosstab slot lost during round-trip")
	}
}

// TestOverlaySpec_EmptyOmitted verifies an empty Overlays slot is
// omitted from the marshaled JSON entirely — additive byte-identity
// contract.
func TestOverlaySpec_EmptyOmitted(t *testing.T) {
	req := types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal Request: %v", err)
	}
	if got := string(data); contains(got, `"overlays"`) {
		t.Fatalf("empty Overlays must be omitted from JSON: %s", got)
	}
}

// TestOverlayLayer_RoundTrip verifies the response-side wrapper round-
// trips through JSON. Uses a scalar-shape payload because it exercises
// the OverlayPayload discriminated union most concisely.
func TestOverlayLayer_RoundTrip(t *testing.T) {
	value := 117.5
	layer := types.OverlayLayer{
		Name:  "i_row",
		Kind:  types.OverlayKindIndexVsMargin,
		Scope: types.OverlayScopeTotal,
		Ref: types.OverlayRef{
			Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand},
		},
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeScalar,
			Scalar: &value,
		},
	}

	data, err := json.Marshal(layer)
	if err != nil {
		t.Fatalf("marshal OverlayLayer: %v", err)
	}

	var got types.OverlayLayer
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal OverlayLayer: %v", err)
	}

	if !reflect.DeepEqual(layer, got) {
		t.Fatalf("OverlayLayer round-trip mismatch:\n want: %+v\n  got: %+v",
			layer, got)
	}
	if got.Payload.Shape != types.OverlayShapeScalar {
		t.Fatalf("Payload.Shape = %q, want %q",
			got.Payload.Shape, types.OverlayShapeScalar)
	}
	if got.Payload.Scalar == nil || *got.Payload.Scalar != value {
		t.Fatalf("Payload.Scalar lost: %+v", got.Payload.Scalar)
	}
}

// contains is a tiny grep helper used only by the omitempty assertion.
// Avoids pulling strings.Contains into the import block when only one
// site needs it.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
