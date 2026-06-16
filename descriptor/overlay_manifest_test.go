package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestManifestOverlayCapability(t *testing.T) {
	m := BuildManifest()

	if got, want := len(m.Overlays), len(types.AllOverlayKinds()); got != want {
		t.Fatalf("Manifest.Overlays length = %d, want %d (parity with AllOverlayKinds)", got, want)
	}

	var entry *OverlayCapability
	for i := range m.Overlays {
		if m.Overlays[i].Kind == types.OverlayKindIndexVsMargin {
			entry = &m.Overlays[i]
			break
		}
	}
	if entry == nil {
		t.Fatalf("Manifest.Overlays missing OVERLAY_INDEX_VS_MARGIN entry")
	}

	// Shapes: [matrix]
	if got, want := entry.Shapes, []types.OverlayShape{types.OverlayShapeMatrix}; !equalShapes(got, want) {
		t.Errorf("Shapes = %v, want %v", got, want)
	}

	if got, want := entry.Scopes, []types.OverlayScope{types.OverlayScopeCell}; !equalScopes(got, want) {
		t.Errorf("Scopes = %v, want %v", got, want)
	}

	// RefKinds: ["Margin"]
	if got, want := entry.RefKinds, []string{"Margin"}; !equalStrings(got, want) {
		t.Errorf("RefKinds = %v, want %v", got, want)
	}

	// Buffered: true — the streamability table reports
	// OVERLAY_INDEX_VS_MARGIN as non-streamable, so Buffered must
	// invert to true.
	streamable, known := types.OverlayStreamable(types.OverlayKindIndexVsMargin)
	if !known {
		t.Fatalf("OverlayStreamable: OVERLAY_INDEX_VS_MARGIN must be a known kind")
	}
	if want := !streamable; entry.Buffered != want {
		t.Errorf("Buffered = %v, want %v (inverse of OverlayStreamable=%v)",
			entry.Buffered, want, streamable)
	}

	// Description: non-empty.
	if entry.Description == "" {
		t.Errorf("Description is empty; expected short human-readable summary")
	}
}

// TestManifestOverlayCapability_AlphabetizedByKind verifies the slice
// Manifest.Overlays carries is ordered by Kind. Deterministic ordering
// keeps the manifest golden stable as new kinds land.
func TestManifestOverlayCapability_AlphabetizedByKind(t *testing.T) {
	m := BuildManifest()
	for i := 1; i < len(m.Overlays); i++ {
		if string(m.Overlays[i-1].Kind) > string(m.Overlays[i].Kind) {
			t.Errorf("Manifest.Overlays not sorted: %q > %q at index %d",
				m.Overlays[i-1].Kind, m.Overlays[i].Kind, i)
		}
	}
}
