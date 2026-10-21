package descriptor

import (
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/types"
)

// TestProcessChainCapability_OverlaysCoversChainKinds enforces that the
// ProcessChainCapability.Overlays sub-block carries one entry per
// whole-chain overlay kind the catalog declares. A new whole-chain kind
// that lands in types/overlay.go without an entry here would silently
// drop from the chain capability surface even though it would still be
// dispatchable on ChainRequest.Overlays.
func TestProcessChainCapability_OverlaysCoversChainKinds(t *testing.T) {
	cap := processChainCapability()
	expected := []types.OverlayKind{
		types.OverlayKindDeltaVsStage,
		types.OverlayKindIndexVsStage,
	}
	if got, want := len(cap.Overlays), len(expected); got != want {
		t.Fatalf("ProcessChain.Overlays length = %d, want %d", got, want)
	}
	seen := make(map[types.OverlayKind]bool, len(cap.Overlays))
	for i, c := range cap.Overlays {
		if c.Kind == "" {
			t.Errorf("ProcessChain.Overlays[%d] has empty Kind", i)
		}
		if seen[c.Kind] {
			t.Errorf("ProcessChain.Overlays[%d] duplicate Kind %q", i, c.Kind)
		}
		seen[c.Kind] = true
	}
	for _, k := range expected {
		if !seen[k] {
			t.Errorf("ProcessChain.Overlays missing entry for kind %q", k)
		}
	}
}

// TestProcessChainCapability_OverlaysAlphabetizedByKind verifies the
// Overlays sub-block is ordered alphabetically by Kind. Deterministic
// ordering keeps the manifest golden stable.
func TestProcessChainCapability_OverlaysAlphabetizedByKind(t *testing.T) {
	cap := processChainCapability()
	for i := 1; i < len(cap.Overlays); i++ {
		if string(cap.Overlays[i-1].Kind) > string(cap.Overlays[i].Kind) {
			t.Errorf("ProcessChain.Overlays not sorted: %q > %q at index %d",
				cap.Overlays[i-1].Kind, cap.Overlays[i].Kind, i)
		}
	}
}

// TestProcessChainCapability_OverlaysBufferedMatchesStreamability
// verifies that every entry's Buffered flag is the inverse of
// types.OverlayStreamable for its kind. Whole-chain kinds always stay
// buffered (the post-stage-loop barrier runs after every stage
// finalises); this test guards against capability-surface drift if the
// streamability table flips.
func TestProcessChainCapability_OverlaysBufferedMatchesStreamability(t *testing.T) {
	cap := processChainCapability()
	for _, c := range cap.Overlays {
		streamable, known := types.OverlayStreamable(c.Kind)
		if !known {
			t.Errorf("ProcessChain.Overlays entry %q: OverlayStreamable reports unknown kind", c.Kind)
			continue
		}
		if want := !streamable; c.Buffered != want {
			t.Errorf("ProcessChain.Overlays entry %q: Buffered = %v, want %v (inverse of OverlayStreamable=%v)",
				c.Kind, c.Buffered, want, streamable)
		}
	}
}

// TestProcessChainCapability_OverlaysShape verifies each whole-chain
// overlay row declares the canonical chain-family contract: Shapes ⊇
// {MATRIX, SCALAR, SERIES} (whole-chain kinds inherit the target
// stage's host shape), Scopes == [total] (whole-result decoration), and
// RefKinds == [Stage] (the StageRef discriminated reference family).
func TestProcessChainCapability_OverlaysShape(t *testing.T) {
	cap := processChainCapability()

	wantShapes := []types.OverlayShape{
		types.OverlayShapeMatrix,
		types.OverlayShapeScalar,
		types.OverlayShapeSeries,
	}
	wantScopes := []types.OverlayScope{
		types.OverlayScopeTotal,
	}
	wantRefKinds := []string{"Stage"}

	for _, c := range cap.Overlays {
		if got := c.Shapes; !equalShapes(got, wantShapes) {
			t.Errorf("entry %q: Shapes = %v, want %v", c.Kind, got, wantShapes)
		}
		if got := c.Scopes; !equalScopes(got, wantScopes) {
			t.Errorf("entry %q: Scopes = %v, want %v", c.Kind, got, wantScopes)
		}
		if got := c.RefKinds; !equalStrings(got, wantRefKinds) {
			t.Errorf("entry %q: RefKinds = %v, want %v", c.Kind, got, wantRefKinds)
		}
		if c.Description == "" {
			t.Errorf("entry %q: Description is empty", c.Kind)
		}
	}
}

// TestProcessChainCapability_OverlaysFieldsSorted verifies each row's
// inner slices are alphabetically sorted. Required for deterministic
// golden output.
func TestProcessChainCapability_OverlaysFieldsSorted(t *testing.T) {
	cap := processChainCapability()
	for _, c := range cap.Overlays {
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
		if !sort.StringsAreSorted(c.Fields) {
			t.Errorf("entry %q: Fields not sorted: %v", c.Kind, c.Fields)
		}
	}
}

// TestProcessChainCapability_OverlaysMirrorsOverlayCatalog verifies the
// chain capability rows are byte-equal to the corresponding entries on
// the top-level Manifest.Overlays surface. Single source of truth for
// kind metadata lives in descriptor.overlayCapabilityFor() — this gate
// catches accidental divergence between the chain sub-block and the
// global overlay catalog (the chain rows MUST be the same rows that
// land on Manifest.Overlays).
func TestProcessChainCapability_OverlaysMirrorsOverlayCatalog(t *testing.T) {
	chainCap := processChainCapability()
	all := OverlayCapabilities()

	byKind := make(map[types.OverlayKind]descriptor.OverlayCapability, len(all))
	for _, c := range all {
		byKind[c.Kind] = c
	}

	for _, chain := range chainCap.Overlays {
		want, ok := byKind[chain.Kind]
		if !ok {
			t.Errorf("ProcessChain.Overlays entry %q has no matching row in OverlayCapabilities()", chain.Kind)
			continue
		}
		if !equalShapes(chain.Shapes, want.Shapes) {
			t.Errorf("entry %q: Shapes diverged: chain=%v, catalog=%v", chain.Kind, chain.Shapes, want.Shapes)
		}
		if !equalScopes(chain.Scopes, want.Scopes) {
			t.Errorf("entry %q: Scopes diverged: chain=%v, catalog=%v", chain.Kind, chain.Scopes, want.Scopes)
		}
		if !equalStrings(chain.RefKinds, want.RefKinds) {
			t.Errorf("entry %q: RefKinds diverged: chain=%v, catalog=%v", chain.Kind, chain.RefKinds, want.RefKinds)
		}
		if chain.Buffered != want.Buffered {
			t.Errorf("entry %q: Buffered diverged: chain=%v, catalog=%v", chain.Kind, chain.Buffered, want.Buffered)
		}
		if chain.Description != want.Description {
			t.Errorf("entry %q: Description diverged", chain.Kind)
		}
	}
}

// TestProcessChainCapability_RowLocalAttributesMatchGate: the manifest's
// process_chain.row_local_attributes is exactly the built-in attributes
// the shared chain gate (mergegate.ChainRefusal) admits on a bare spec
// — so the list moves with the gate (U51 E3-S2 admitted
// ATTR_SCALE_SCORE and the reference-form ATTR_PROFILE_SIMILARITY; the
// #391 row-local reads were missing) — and is sorted. ATTR_VEC_DISTANCE is judged over a row-local metric.
func TestProcessChainCapability_RowLocalAttributesMatchGate(t *testing.T) {
	var want []string
	for _, at := range types.AllAttributeTypes() {
		req := &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}},
			Attributes:   []*types.Attribute{{Type: at, Field: "x"}},
		}
		if at == types.ATTR_VEC_DISTANCE {
			// Admitted over a row-local metric (its rejection rule
			// covers the fitted metrics and a centroid).
			req.Attributes[0].Params = []byte(`{"metric":"euclidean","reference":[1]}`)
		}
		if mergegate.ChainRefusal(req, nil, nil, 0, "s0") == nil {
			want = append(want, string(at))
		}
	}
	sort.Strings(want)
	got := processChainCapability().RowLocalAttributes
	if !reflect.DeepEqual(got, want) {
		t.Errorf("process_chain.row_local_attributes = %v, want the gate's %v", got, want)
	}
}

// TestProcessChainCapability_MergeableGroupersMatchGate: the manifest's
// process_chain.mergeable_groupers is exactly the built-in groupers the
// shared chain gate (mergegate.ChainRefusal) admits — so the list moves
// with types.GroupType.Mergeable (U51 E3-S3 admitted GROUP_ROUNDED; the
// list had stopped at CATEGORY / RANGE) — and is sorted.
func TestProcessChainCapability_MergeableGroupersMatchGate(t *testing.T) {
	var want []string
	for _, gt := range types.AllGroupTypes() {
		req := &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}},
			Groups:       []*types.Group{{Type: gt, Field: "g"}},
		}
		if mergegate.ChainRefusal(req, nil, nil, 0, "s0") == nil {
			want = append(want, string(gt))
		}
	}
	sort.Strings(want)
	got := processChainCapability().MergeableGroupers
	if !reflect.DeepEqual(got, want) {
		t.Errorf("process_chain.mergeable_groupers = %v, want the gate's %v", got, want)
	}
}
