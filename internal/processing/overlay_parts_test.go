package processing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// withTestOverlayHandler swaps the MATRIX handler of an existing kind
// for a test-only handler for the duration of the test. No kind is
// registered: the swap only lets the dispatcher see a multi-part
// layer before a real multi-output kind exists. Processing tests never
// run in parallel, so the swap is not observed elsewhere.
func withTestOverlayHandler(t *testing.T, kind types.OverlayKind, h overlayHandler) {
	t.Helper()
	prev, ok := overlayHandlers[kind]
	if !ok {
		t.Fatalf("%s has no MATRIX handler to swap", kind)
	}
	overlayHandlers[kind] = h
	t.Cleanup(func() { overlayHandlers[kind] = prev })
}

// TestApplyOverlays_PartsRefusal: the MATRIX dispatcher ships a flat
// parts map untouched and refuses a handler that nests parts with
// PROCESSING_INTERNAL naming the layer index and the part.
func TestApplyOverlays_PartsRefusal(t *testing.T) {
	// serial: swaps the package-level processing.overlayHandlers test hook

	scalar := func(v float64) *types.OverlayPayload {
		return &types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &v}
	}
	nested := scalar(2)
	nested.Parts = map[string]*types.OverlayPayload{"inner": scalar(3)}
	cases := []struct {
		name     string
		parts    map[string]*types.OverlayPayload
		wantPart string // "" = accepted
	}{
		{name: "flat parts ship", parts: map[string]*types.OverlayPayload{"expected": scalar(1), "p": scalar(0.5)}},
		{name: "nested part refused", parts: map[string]*types.OverlayPayload{"expected": scalar(1), "p": nested}, wantPart: "p"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withTestOverlayHandler(t, types.OverlayKindChiSqMatrix, func(spec *types.OverlaySpec, _ *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
				one := 1.0
				return types.OverlayLayer{Name: spec.Name, Kind: spec.Kind, Scope: spec.Scope, Payload: types.OverlayPayload{
					Shape: types.OverlayShapeScalar, Scalar: &one, Parts: c.parts,
				}}, nil, nil
			})
			specs := []types.OverlaySpec{
				{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
				{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
			}
			layers, _, err := ApplyOverlaysWithExtensions(specs, NewCrosstabHostView(chiSqMatrixPayloadKnown()), nil)
			if c.wantPart == "" {
				if err != nil {
					t.Fatalf("flat parts refused: %v", err)
				}
				if len(layers) != 2 || len(layers[1].Payload.Parts) != len(c.parts) {
					t.Fatalf("parts did not ship: %+v", layers)
				}
				return
			}
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != errors.PROCESSING_INTERNAL {
				t.Fatalf("err = %v, want PROCESSING_INTERNAL", err)
			}
			if ce.Details["part"] != c.wantPart || ce.Details["index"] != 0 {
				t.Errorf("details = %v, want part %q at index 0", ce.Details, c.wantPart)
			}
		})
	}
}

// TestApplyOverlays_ExistingKindsCarryNoParts: a real single-output
// kind's layer leaves Parts nil, so its wire bytes carry no `parts`
// key — the pre-Parts form.
func TestApplyOverlays_ExistingKindsCarryNoParts(t *testing.T) {
	t.Parallel()

	specs := []types.OverlaySpec{
		{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
		{Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow},
		{Kind: types.OverlayKindIndexVsMargin, Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}},
	}
	layers, _, err := ApplyOverlaysWithExtensions(specs, NewCrosstabHostView(chiSqMatrixPayloadKnown()), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range layers {
		if l.Payload.Parts != nil {
			t.Errorf("layer %d (%s) carries parts: %v", i, l.Kind, l.Payload.Parts)
		}
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"parts"`) {
			t.Errorf("layer %d (%s) wrote a parts key: %s", i, l.Kind, b)
		}
	}
}
