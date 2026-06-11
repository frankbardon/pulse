package descriptor

import (
	"sort"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestOverlayCapabilities_CoversAllKinds enforces parity between
// types.AllOverlayKinds() and the per-kind catalog entries
// OverlayCapabilities() emits. A new overlay kind that lands in
// types/overlay.go without an entry here fails this gate — the
// manifest builder (E1-S10) iterates the capability slice and a
// missing entry would silently drop the kind.
func TestOverlayCapabilities_CoversAllKinds(t *testing.T) {
	caps := OverlayCapabilities()
	if got, want := len(caps), len(types.AllOverlayKinds()); got != want {
		t.Fatalf("OverlayCapabilities length = %d, want %d (parity with AllOverlayKinds)", got, want)
	}

	seen := make(map[types.OverlayKind]bool, len(caps))
	for i, c := range caps {
		if c.Kind == "" {
			t.Errorf("OverlayCapabilities[%d] has empty Kind", i)
		}
		if seen[c.Kind] {
			t.Errorf("OverlayCapabilities[%d] duplicate Kind %q", i, c.Kind)
		}
		seen[c.Kind] = true
	}
	for _, k := range types.AllOverlayKinds() {
		if !seen[k] {
			t.Errorf("OverlayCapabilities missing entry for kind %q", k)
		}
	}
}

// TestOverlayCapabilities_AlphabetizedByKind verifies the returned
// slice is ordered by Kind. Deterministic ordering keeps the
// downstream manifest golden stable.
func TestOverlayCapabilities_AlphabetizedByKind(t *testing.T) {
	caps := OverlayCapabilities()
	for i := 1; i < len(caps); i++ {
		if string(caps[i-1].Kind) > string(caps[i].Kind) {
			t.Errorf("OverlayCapabilities not sorted: %q > %q at index %d",
				caps[i-1].Kind, caps[i].Kind, i)
		}
	}
}

// TestOverlayCapabilities_IndexVsMargin verifies the E1 entry
// emits the contract spelled out in PRD §I-FR-I2 and the story
// acceptance criteria.
func TestOverlayCapabilities_IndexVsMargin(t *testing.T) {
	caps := OverlayCapabilities()

	var entry *OverlayCapability
	for i := range caps {
		if caps[i].Kind == types.OverlayKindIndexVsMargin {
			entry = &caps[i]
			break
		}
	}
	if entry == nil {
		t.Fatalf("OverlayCapabilities missing OVERLAY_INDEX_VS_MARGIN entry")
	}

	// Shapes: [matrix]
	if got, want := entry.Shapes, []types.OverlayShape{types.OverlayShapeMatrix}; !equalShapes(got, want) {
		t.Errorf("Shapes = %v, want %v", got, want)
	}

	// Scopes: [cell]
	if got, want := entry.Scopes, []types.OverlayScope{types.OverlayScopeCell}; !equalScopes(got, want) {
		t.Errorf("Scopes = %v, want %v", got, want)
	}

	// RefKinds: ["Margin"]
	if got, want := entry.RefKinds, []string{"Margin"}; !equalStrings(got, want) {
		t.Errorf("RefKinds = %v, want %v", got, want)
	}

	// Buffered: true (consults OverlayStreamable — inverts streamable)
	streamable, known := types.OverlayStreamable(types.OverlayKindIndexVsMargin)
	if !known {
		t.Fatalf("OverlayStreamable: OVERLAY_INDEX_VS_MARGIN must be a known kind")
	}
	if want := !streamable; entry.Buffered != want {
		t.Errorf("Buffered = %v, want %v (inverse of OverlayStreamable=%v)",
			entry.Buffered, want, streamable)
	}
	if !entry.Buffered {
		t.Errorf("Buffered = false, want true (E1 host crosstab is buffered)")
	}

	// Description: non-empty.
	if entry.Description == "" {
		t.Errorf("Description is empty; expected short human-readable summary")
	}
}

// TestOverlayCapabilities_BufferedMatchesStreamability verifies that
// every entry's Buffered flag is the inverse of OverlayStreamable for
// its kind. The streamability table in types/overlay_streamability.go
// is the single source of truth; this gate prevents the capability
// surface from drifting.
func TestOverlayCapabilities_BufferedMatchesStreamability(t *testing.T) {
	for _, c := range OverlayCapabilities() {
		streamable, known := types.OverlayStreamable(c.Kind)
		if !known {
			t.Errorf("OverlayCapabilities entry %q: OverlayStreamable reports unknown kind", c.Kind)
			continue
		}
		if want := !streamable; c.Buffered != want {
			t.Errorf("OverlayCapabilities entry %q: Buffered = %v, want %v (inverse of OverlayStreamable=%v)",
				c.Kind, c.Buffered, want, streamable)
		}
	}
}

// TestOverlayCapabilities_FieldsSorted verifies every entry's Shapes,
// Scopes, and RefKinds lists are alphabetically sorted. Required for
// deterministic golden output once the manifest hookup lands in
// E1-S10.
func TestOverlayCapabilities_FieldsSorted(t *testing.T) {
	for _, c := range OverlayCapabilities() {
		shapes := make([]string, len(c.Shapes))
		for i, s := range c.Shapes {
			shapes[i] = string(s)
		}
		if !sort.StringsAreSorted(shapes) {
			t.Errorf("entry %q: Shapes not sorted: %v", c.Kind, shapes)
		}

		scopes := make([]string, len(c.Scopes))
		for i, s := range c.Scopes {
			scopes[i] = string(s)
		}
		if !sort.StringsAreSorted(scopes) {
			t.Errorf("entry %q: Scopes not sorted: %v", c.Kind, scopes)
		}

		if !sort.StringsAreSorted(c.RefKinds) {
			t.Errorf("entry %q: RefKinds not sorted: %v", c.Kind, c.RefKinds)
		}

		// E2-S11: Fields slot lists Level / Within field-name strings
		// for the share / index / delta / zscore family; alphabetised
		// for golden stability mirroring the RefKinds / Shapes / Scopes
		// sort contract.
		if !sort.StringsAreSorted(c.Fields) {
			t.Errorf("entry %q: Fields not sorted: %v", c.Kind, c.Fields)
		}
	}
}

// equalShapes compares two OverlayShape slices for order-sensitive
// equality. Tiny helper local to the test file.
func equalShapes(a, b []types.OverlayShape) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalScopes compares two OverlayScope slices for order-sensitive
// equality.
func equalScopes(a, b []types.OverlayScope) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalStrings compares two string slices for order-sensitive
// equality.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
