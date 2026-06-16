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

// TestOverlayLayer_WarningsFreeByteIdentical locks the additive
// contract for the new OverlayLayer.Warnings slot (E1-S2): a layer
// authored without ever touching the field marshals byte-identically
// to one with Warnings explicitly set to nil AND to one with Warnings
// set to an empty (non-nil) slice. The `omitempty` tag must elide the
// key in all three cases — pre-E1-S2 callers stay wire-stable.
//
// Mirrors TestComposedResponse_OverlayFreeByteIdentical at
// types/types_test.go:1106 in style and intent.
func TestOverlayLayer_WarningsFreeByteIdentical(t *testing.T) {
	value := 1.0
	base := types.OverlayLayer{
		Name:  "i_row",
		Kind:  types.OverlayKindIndexVsMargin,
		Scope: types.OverlayScopeCell,
		Ref: types.OverlayRef{
			Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
		},
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeScalar,
			Scalar: &value,
		},
	}

	// (1) Implicit: field never touched on the struct literal.
	implicit, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal implicit OverlayLayer: %v", err)
	}
	if contains(string(implicit), `"warnings"`) {
		t.Fatalf("implicit OverlayLayer must omit the warnings key: %s", implicit)
	}

	// (2) Explicit nil: Warnings: nil produces the same bytes as the
	// implicit shape.
	explicitNil := base
	explicitNil.Warnings = nil
	gotNil, err := json.Marshal(explicitNil)
	if err != nil {
		t.Fatalf("marshal explicit-nil Warnings OverlayLayer: %v", err)
	}
	if string(gotNil) != string(implicit) {
		t.Fatalf("explicit-nil Warnings diverged from implicit:\n got: %s\nwant: %s",
			gotNil, implicit)
	}

	// (3) Empty non-nil slice: omitempty must still elide the key.
	emptySlice := base
	emptySlice.Warnings = []types.OverlayWarning{}
	gotEmpty, err := json.Marshal(emptySlice)
	if err != nil {
		t.Fatalf("marshal empty-slice Warnings OverlayLayer: %v", err)
	}
	if string(gotEmpty) != string(implicit) {
		t.Fatalf("empty-slice Warnings diverged from implicit:\n got: %s\nwant: %s",
			gotEmpty, implicit)
	}

	// (4) Populated slice: the warnings key MUST appear, carrying the
	// expected entries. Locks the positive path so a future tag
	// regression cannot silently drop populated warnings either.
	populated := base
	populated.Warnings = []types.OverlayWarning{
		{
			Code:    "PULSE_OVERLAY_REF_ZERO",
			Message: "reference cell is zero",
			Details: map[string]any{"row": 0.0, "col": 1.0},
		},
	}
	gotPopulated, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated Warnings OverlayLayer: %v", err)
	}
	if !contains(string(gotPopulated), `"Warnings"`) &&
		!contains(string(gotPopulated), `"warnings"`) {
		t.Fatalf("populated OverlayLayer must surface the warnings key: %s",
			gotPopulated)
	}
	// Round-trip the populated layer to confirm the entries survive.
	var back types.OverlayLayer
	if err := json.Unmarshal(gotPopulated, &back); err != nil {
		t.Fatalf("unmarshal populated OverlayLayer: %v", err)
	}
	if len(back.Warnings) != 1 {
		t.Fatalf("populated round-trip lost Warnings: len=%d, want 1",
			len(back.Warnings))
	}
	if back.Warnings[0].Code != "PULSE_OVERLAY_REF_ZERO" {
		t.Fatalf("Warnings[0].Code = %q, want PULSE_OVERLAY_REF_ZERO",
			back.Warnings[0].Code)
	}
	if back.Warnings[0].Message != "reference cell is zero" {
		t.Fatalf("Warnings[0].Message = %q, want %q",
			back.Warnings[0].Message, "reference cell is zero")
	}

	// (5) Unmarshal a JSON object WITHOUT the warnings key — the
	// resulting struct must have Warnings == nil (not an empty slice).
	// This is the contract that lets downstream code use `len(...) == 0`
	// and `Warnings == nil` interchangeably for pre-E1-S2 payloads.
	noKey := `{"name":"i_row","kind":"OVERLAY_INDEX_VS_MARGIN","scope":"cell",` +
		`"ref":{"margin":{"axis":"row"}},"payload":{"shape":"scalar","scalar":1}}`
	var decoded types.OverlayLayer
	if err := json.Unmarshal([]byte(noKey), &decoded); err != nil {
		t.Fatalf("unmarshal warnings-free OverlayLayer JSON: %v", err)
	}
	if decoded.Warnings != nil {
		t.Fatalf("Warnings must be nil after unmarshal of warnings-free JSON, "+
			"got %v (len=%d)", decoded.Warnings, len(decoded.Warnings))
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
