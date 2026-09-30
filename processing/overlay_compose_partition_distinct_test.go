package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// E4-S3 runtime arm: the E4-S2 slab gate covers the DISTINCT-KEY
// within-prefix mode too.
//
// E4-S2 built that gate off types.PanelNSourceUsesWithinDepth rather
// than a narrower "sums distinct cells" predicate, so joining the
// predicate is supposed to be the whole wiring. These drive the REAL
// fold — ApplyComposeOverlaysWithRequests, what service.applyComposeOverlays
// calls — because the predicate agreeing with itself proves nothing
// about whether the dispatcher consults it for this mode.

// panelDistinctPartitionFold is panelPartitionFold with
// components-bearing slots, so a spec the gate WAVES PAST reaches the
// handler and produces a layer instead of dying on the components
// requirement. The two-dim row axis is the same one the value leg's
// partition tests use.
func panelDistinctPartitionFold(t *testing.T, refRows, targetRows []types.GroupType, params map[string]any) ([]types.OverlayLayer, error) {
	t.Helper()
	ref := withPanelMarginComponents(
		withRowKeys(makeMatrixWithRowMargins(
			[3][3]float64{{50, 50, 50}, {25, 25, 25}, {40, 40, 40}},
			[3]float64{999, 999, 999},
		), twoDimRowKeys), types.AGG_DISTINCT_SUM, [3]float64{100, 60, 80})
	target := withPanelMarginComponents(
		withRowKeys(makeMatrixWithRowMargins(
			[3][3]float64{{60, 55, 45}, {20, 20, 20}, {30, 65, 50}},
			[3]float64{999, 999, 999},
		), twoDimRowKeys), types.AGG_DISTINCT_SUM, [3]float64{90, 30, 50})

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
		requests, nil)
	return layers, err
}

// The inherited refusal. An explicit depth makes the distinct leg a
// sum across rows, and a fan-out row grouper OUTSIDE the fixed prefix
// lands one respondent in two summed rows: the denominator comes out
// too large and every p-value too small, silently.
func TestApplyComposeOverlays_PanelDistinctWithinFanOutBeyondPrefixRefused(t *testing.T) {
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	layers, err := panelDistinctPartitionFold(t, fanOut, fanOut, map[string]any{
		"n_source":       types.PanelNSourceRowMarginDistinctWithin,
		"n_within_depth": 0,
	})
	assertSlabRefusal(t, err, 0, 0, 1)
	if layers != nil {
		t.Errorf("a refused spec still emitted %d layer(s)", len(layers))
	}
}

// The fan-out on the TARGET only. The gate walks every slot in panel
// order, so an axis that is clean on the reference and fans out on a
// target must still refuse — and must name the target.
func TestApplyComposeOverlays_PanelDistinctWithinFanOutOnTargetRefused(t *testing.T) {
	clean := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY}
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	_, err := panelDistinctPartitionFold(t, clean, fanOut, map[string]any{
		"n_source":       types.PanelNSourceRowMarginDistinctWithin,
		"n_within_depth": 0,
	})
	assertSlabRefusal(t, err, 1, 1, 1)
}

// Omitted depth sums NOTHING, so the same fan-out axis is harmless and
// must not be gated. The two arms differ only in whether
// n_within_depth is present — and they must produce DIFFERENT numbers
// on a clean axis, or a gate that fired on both would be invisible.
func TestApplyComposeOverlays_PanelDistinctWithinOmittedDepthNotGated(t *testing.T) {
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	layers, err := panelDistinctPartitionFold(t, fanOut, fanOut, map[string]any{
		"n_source": types.PanelNSourceRowMarginDistinctWithin,
	})
	if err != nil {
		t.Fatalf("omitted depth was gated; it sums nothing: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
	exact := pairs(t, layers[0], 0, 0)[0]

	// The same fixture on a CLEAN axis at depth 0 must be a different
	// number. Without this the refusal above could be firing on a
	// fixture whose two arms coincide, and the gate assertion would
	// prove nothing.
	clean := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY}
	summedLayers, err := panelDistinctPartitionFold(t, clean, clean, map[string]any{
		"n_source":       types.PanelNSourceRowMarginDistinctWithin,
		"n_within_depth": 0,
	})
	if err != nil {
		t.Fatalf("clean axis at depth 0 was gated: %v", err)
	}
	summed := pairs(t, summedLayers[0], 0, 0)[0]
	if math.IsNaN(exact) || math.IsNaN(summed) {
		t.Fatalf("NaN p-value (exact %v, summed %v)", exact, summed)
	}
	if exact == summed {
		t.Fatal("the gated and ungated arms produce the same number; this fixture cannot demonstrate the gate")
	}
}

// A fan-out INSIDE the fixed prefix multiplies SLABS, not cells, so
// each slab still partitions its own keys. That is the shape the
// within-prefix mode exists to serve and it must run.
func TestApplyComposeOverlays_PanelDistinctWithinFanOutInsidePrefixAccepted(t *testing.T) {
	inside := []types.GroupType{types.GROUP_SET_PER_ELEMENT, types.GROUP_CATEGORY}
	layers, err := panelDistinctPartitionFold(t, inside, inside, map[string]any{
		"n_source":       types.PanelNSourceRowMarginDistinctWithin,
		"n_within_depth": 0,
	})
	if err != nil {
		t.Fatalf("a fan-out inside the fixed prefix was refused: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
}
