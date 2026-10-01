package processing

import (
	stderrors "errors"
	"math"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E4-S2 runtime arm: a panel prefix slab that cannot partition the key
// set is refused.
//
// These drive the REAL fold path — processing.ApplyComposeOverlaysWithRequests,
// which is what service.(*Service).applyComposeOverlays calls — rather
// than unit-testing checkPanelSlabPartition. The predicate agreeing
// with itself proves nothing about whether the dispatcher consults it,
// or about where in the gate ladder it sits.

// panelPartitionRequest builds one authored Compose slot whose crosstab
// row axis is the given grouper tuple. Only the row axis matters to the
// gate; the rest is the minimum a Compose slot needs.
func panelPartitionRequest(label string, rowTypes ...types.GroupType) *types.Request {
	rows := make([]*types.Group, 0, len(rowTypes))
	for i, gt := range rowTypes {
		rows = append(rows, &types.Group{Type: gt, Field: []string{"region", "brands", "wave"}[i%3]})
	}
	return &types.Request{
		Label: label,
		Crosstab: &types.CrosstabSpec{
			Rows:    rows,
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "col"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// panelPartitionFold drives the dispatcher exactly the way the service
// layer does: one panel spec over a two-slot Compose, the reference
// labelled "baseline" and the target "t1".
func panelPartitionFold(t *testing.T, refRows, targetRows []types.GroupType, params map[string]any, exts *ExtensionRegistry) ([]types.OverlayLayer, error) {
	t.Helper()
	ref, target := panelTwoDimSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t1"}, nil)
	spec.Params = params
	requests := []*types.Request{
		panelPartitionRequest("baseline", refRows...),
		panelPartitionRequest("t1", targetRows...),
	}
	layers, _, err := ApplyComposeOverlaysWithRequests(
		[]types.ComposeOverlaySpec{spec},
		[]*types.Response{ref, target},
		[]string{"baseline", "t1"},
		requests, exts)
	return layers, err
}

// assertSlabRefusal unwraps the coded error and checks the code plus
// the Details a renderer needs to point at the offending slot.
func assertSlabRefusal(t *testing.T, err error, wantPanelIdx, wantSlotIdx, wantDim int) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	var ce *pulseerrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if ce.Code != pulseerrors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED {
		t.Fatalf("Code = %q, want %q", ce.Code, pulseerrors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)
	}
	for k, want := range map[string]any{
		"panel_index": wantPanelIdx,
		"slot_index":  wantSlotIdx,
		"dim_index":   wantDim,
		"axis":        "row",
	} {
		if ce.Details[k] != want {
			t.Errorf("Details[%q] = %v, want %v", k, ce.Details[k], want)
		}
	}
}

// The headline refusal: an explicit depth makes the leg a sum across
// rows, and a fan-out row grouper OUTSIDE the fixed prefix lands one
// record in two summed rows.
func TestApplyComposeOverlays_PanelWithinFanOutBeyondPrefixRefused(t *testing.T) {
	layers, err := panelPartitionFold(t,
		[]types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT},
		[]types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT},
		map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0},
		nil)
	assertSlabRefusal(t, err, 0, 0, 1)
	if layers != nil {
		t.Errorf("a refused spec still emitted %d layer(s)", len(layers))
	}
}

// The omitted-depth form reads the EXACT per-slot row margin and sums
// NOTHING, so a fan-out row axis is harmless there and must not be
// gated. The same axis that refuses above must pass here — the two
// arms differ only in whether n_within_depth is present.
//
// It also asserts the two arms are numerically DISTINCT on this
// fixture: if omitted depth and depth 0 produced the same p-value, a
// gate that fired on both would be invisible and this test would prove
// nothing.
func TestApplyComposeOverlays_PanelWithinOmittedDepthNotGated(t *testing.T) {
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	layers, err := panelPartitionFold(t, fanOut, fanOut,
		map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin}, nil)
	if err != nil {
		t.Fatalf("omitted depth was gated; it sums nothing: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
	exact := pairs(t, layers[0], 0, 0)[0]

	// Depth 0 on a CLEAN row axis — same fixture, same margins, so the
	// only difference is the slab sum.
	clean := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY}
	summedLayers, err := panelPartitionFold(t, clean, clean,
		map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0}, nil)
	if err != nil {
		t.Fatalf("clean axis at depth 0 was gated: %v", err)
	}
	summed := pairs(t, summedLayers[0], 0, 0)[0]
	if math.Abs(exact-summed) < 1e-12 {
		t.Fatalf("omitted depth and depth 0 produced the same p-value (%v);"+
			" the fixture cannot discriminate a gated arm from an ungated one", exact)
	}
}

// A fan-out grouper at depth d <= NWithinDepth sits INSIDE the fixed
// prefix: it multiplies slabs rather than cells, so each slab still
// partitions its own keys. This is the shape the within-prefix mode
// exists for and refusing it would refuse the filed request.
func TestApplyComposeOverlays_PanelWithinFanOutInsidePrefixAccepted(t *testing.T) {
	inside := []types.GroupType{types.GROUP_SET_PER_ELEMENT, types.GROUP_CATEGORY}
	layers, err := panelPartitionFold(t, inside, inside,
		map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0}, nil)
	if err != nil {
		t.Fatalf("a fan-out INSIDE the fixed prefix was refused: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
}

// Slot-by-slot, not reference-only: a clean reference and one dirty
// TARGET still refuses, and the diagnostic names the target by both
// panel index and authored slot index.
func TestApplyComposeOverlays_PanelWithinOffendingTargetSlotRefuses(t *testing.T) {
	layers, err := panelPartitionFold(t,
		[]types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY},
		[]types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT},
		map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0},
		nil)
	assertSlabRefusal(t, err, 1, 1, 1)
	if layers != nil {
		t.Errorf("a refused spec still emitted %d layer(s)", len(layers))
	}
}

// ONE offending slot refuses the WHOLE fold — nothing is emitted for
// the clean specs beside it. Dropping the offender instead would change
// M, and M sets the length and the pair ordering of every cell's
// flattened upper-triangular vector, so a caller would silently receive
// a shorter vector with no way to tell which slot left.
func TestApplyComposeOverlays_PanelWithinOffendingSlotSuppressesCleanSpecs(t *testing.T) {
	ref, target := panelTwoDimSlots()
	clean := composeSpecMultiTargetPropZPanel([]string{"t1"}, nil)
	clean.Name = "clean_panel"
	dirty := composeSpecMultiTargetPropZPanel([]string{"t1"}, nil)
	dirty.Name = "dirty_panel"
	dirty.Params = map[string]any{
		"n_source":       types.PanelNSourceRowMarginValueWithin,
		"n_within_depth": 0,
	}
	requests := []*types.Request{
		panelPartitionRequest("baseline", types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT),
		panelPartitionRequest("t1", types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT),
	}
	layers, warns, err := ApplyComposeOverlaysWithRequests(
		[]types.ComposeOverlaySpec{clean, dirty},
		[]*types.Response{ref, target},
		[]string{"baseline", "t1"},
		requests, nil)
	if err == nil {
		t.Fatal("expected the offending spec to refuse the whole fold")
	}
	if layers != nil || warns != nil {
		t.Errorf("partial emission: %d layer(s), %d warning(s)", len(layers), len(warns))
	}
}

// The cap keeps firing FIRST. A spec that is both over-cap and sitting
// on a fan-out axis must report the cap: it is the structural failure
// the caller has to fix before the params question is even meaningful,
// and both arms order it that way.
func TestApplyComposeOverlays_PanelWithinCapFiresBeforeSlabGate(t *testing.T) {
	ref, target := panelTwoDimSlots()
	// A cap of 0 means "unset", so the over-cap shape is two targets
	// against an explicit cap of 1.
	spec := composeSpecMultiTargetPropZPanel([]string{"t1", "t1"},
		&types.OverlayOptions{MaxPanelTargets: 1})
	spec.Params = map[string]any{
		"n_source":       types.PanelNSourceRowMarginValueWithin,
		"n_within_depth": 0,
	}
	requests := []*types.Request{
		panelPartitionRequest("baseline", types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT),
		panelPartitionRequest("t1", types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT),
	}
	_, _, err := ApplyComposeOverlaysWithRequests(
		[]types.ComposeOverlaySpec{spec},
		[]*types.Response{ref, target},
		[]string{"baseline", "t1"},
		requests, nil)
	var ce *pulseerrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("expected a CodedError, got %v", err)
	}
	if ce.Code != pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP {
		t.Fatalf("Code = %q, want the cap to fire first (%q)",
			ce.Code, pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
	}
}

// An embedder-registered fan-out grouper is gated exactly like
// GROUP_SET_PER_ELEMENT, through the live ExtensionRegistry. A name
// known to NEITHER the built-ins nor the registry passes — it cannot
// execute, so it can produce no wrong n.
func TestApplyComposeOverlays_PanelWithinExtensionGrouperGated(t *testing.T) {
	const custom types.GroupType = "GROUP_PANEL_FANOUT_X"
	axis := []types.GroupType{types.GROUP_CATEGORY, custom}
	params := map[string]any{
		"n_source":       types.PanelNSourceRowMarginValueWithin,
		"n_within_depth": 0,
	}

	if _, err := panelPartitionFold(t, axis, axis, params, nil); err != nil {
		t.Fatalf("an unregistered grouper was refused; it cannot execute: %v", err)
	}
	flat := &ExtensionRegistry{FansOut: map[types.GroupType]bool{custom: false}}
	if _, err := panelPartitionFold(t, axis, axis, params, flat); err != nil {
		t.Fatalf("a registration declaring FansOut=false was refused: %v", err)
	}
	fans := &ExtensionRegistry{FansOut: map[types.GroupType]bool{custom: true}}
	_, err := panelPartitionFold(t, axis, axis, params, fans)
	assertSlabRefusal(t, err, 0, 0, 1)
}

// The legacy ApplyComposeOverlays entry point carries no request list,
// so it cannot see the grouper types and does not gate. Documented,
// not accidental — the same bypass ApplyOverlaysWithExtensions has, and
// every request arriving through pulse.Compose goes via the
// requests-aware entry point instead.
func TestApplyComposeOverlays_LegacyEntryPointCannotGate(t *testing.T) {
	ref, target := panelTwoDimSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t1"}, nil)
	spec.Params = map[string]any{
		"n_source":       types.PanelNSourceRowMarginValueWithin,
		"n_within_depth": 0,
	}
	layers, _, err := ApplyComposeOverlays(
		[]types.ComposeOverlaySpec{spec},
		[]*types.Response{ref, target},
		[]string{"baseline", "t1"})
	if err != nil {
		t.Fatalf("the request-free entry point invented a refusal it cannot justify: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
}
