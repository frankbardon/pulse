package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The panel's within-prefix n leg (E4-S1).
//
// The panel pairs across SLOTS, which carry no dim tuple, so
// n_within_depth cannot index a pair axis the way the OVERLAY_PAIRWISE_*
// family's does. It indexes each slot's OWN row axis: omitted means the
// exact per-slot row margin, and `d` means the sum of that slot's row
// margins over rows agreeing on the first d+1 dim positions.
//
// Two properties carry the story. Omitted depth must reproduce the
// legacy number exactly — the pinned pre-params layer literal is the
// anchor — and an explicit depth of 0 must be a DIFFERENT number, or
// the *int representation buys nothing.

// withRowKeys replaces a fixture's row-key tuples. The margin at index
// i stays bound to the key now at index i, which is what lets the
// reverse-order fixture below separate a keyed slab sum from a
// positional one.
func withRowKeys(resp *types.Response, keys []types.AxisKey) *types.Response {
	resp.Crosstab.Matrix.RowKeys = keys
	return resp
}

// twoDimRowKeys is the 2-dim row axis the depth tests scope over:
// dim 0 has two distinct values, and "a" spans two rows while "b"
// spans one. The asymmetry matters — the singleton slab must agree
// with the unsummed margin, so a test can tell "summed correctly"
// from "summed everything".
var twoDimRowKeys = []types.AxisKey{{"a", "x"}, {"a", "y"}, {"b", "x"}}

// panelTwoDimSlots is the within-prefix fixture: a reference and one
// target, both on the 2-dim row axis. Every margin differs from every
// other, so no two of the three candidate denominators (exact margin,
// depth-0 slab, depth-1 slab) coincide by accident on row 0 — and the
// reference's "a" slab (160) differs from the target's (120), so a
// slab read against the WRONG slot is detectable too.
func panelTwoDimSlots() (*types.Response, *types.Response) {
	ref := withRowKeys(makeMatrixWithRowMargins(
		[3][3]float64{{50, 50, 50}, {25, 25, 25}, {40, 40, 40}},
		[3]float64{100, 60, 80},
	), twoDimRowKeys)
	target := withRowKeys(makeMatrixWithRowMargins(
		[3][3]float64{{60, 55, 45}, {20, 20, 20}, {30, 65, 50}},
		[3]float64{90, 30, 50},
	), twoDimRowKeys)
	return ref, target
}

// --- Omitted depth is the legacy number ------------------------------

// The byte-identity anchor, reached through the new mode. E3-S1's
// literal was captured from a git archive of the pre-params tree; if
// n_within at omitted depth reads anything other than the exact
// per-slot row margin, the whole layer marshals differently and this
// fails on the first differing digit.
//
// The baseline slots carry NO components at all, which doubles as the
// assertion that n_within reads the PAYLOAD margin: a components read
// would refuse these slots outright.
func TestApplyPropZPanel_NWithinOmittedDepthMatchesBaselineBytes(t *testing.T) {
	ref, targets := panelBaselineSlots()
	if ref.Components != nil {
		t.Fatal("fixture gained components; it must stay components-free to prove n_within is a payload read")
	}
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin}

	layer, warns, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("expected no warnings on the baseline fixture, got %v", warns)
	}
	got, err := json.Marshal(layer)
	if err != nil {
		t.Fatalf("marshal layer: %v", err)
	}
	if string(got) != panelBaselineLayerJSON {
		t.Fatalf("n_within at omitted depth is not the exact per-slot row margin:\n got %s\nwant %s",
			got, panelBaselineLayerJSON)
	}
}

// Omitted depth and depth 0 must be OBSERVABLY different numbers.
// This is the whole reason NWithinDepth is a *int: an int zero value
// could not carry "absent", and collapsing the two would hand a caller
// who asked for the first-dim slab the unsummed per-row margin — a
// smaller, entirely plausible number.
func TestApplyPropZPanel_NWithinOmittedDepthIsNotDepthZero(t *testing.T) {
	ref, target := panelTwoDimSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)

	// Row 0 is ("a","x"): margin 100 on the reference, 90 on the
	// target. Its depth-0 slab is {("a","x"), ("a","y")} — 160 on the
	// reference, 120 on the target. Every figure is distinct from
	// every other, so no pair of the candidate denominators can
	// coincide.
	wantExact, ok := twoProportionZ(50, 100, 60, 90)
	if !ok {
		t.Fatal("exact-margin expectation is degenerate; pick different fixture values")
	}
	wantSlab, ok := twoProportionZ(50, 160, 60, 120)
	if !ok {
		t.Fatal("slab expectation is degenerate; pick different fixture values")
	}
	if wantExact == wantSlab {
		t.Fatal("fixture cannot distinguish the exact margin from the depth-0 slab")
	}

	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin}
	omitted, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("omitted depth: %v", err)
	}
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0}
	zero, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("depth 0: %v", err)
	}

	gotOmitted := pairs(t, omitted, 0, 0)[0]
	gotZero := pairs(t, zero, 0, 0)[0]
	if math.IsNaN(gotOmitted) {
		t.Fatalf("omitted-depth p = NaN, want %v", wantExact)
	}
	if math.IsNaN(gotZero) {
		t.Fatalf("depth-0 p = NaN, want %v", wantSlab)
	}
	if math.Abs(gotOmitted-wantExact) > 1e-12 {
		t.Errorf("omitted-depth p = %v, want %v (the exact row margin, no summing)", gotOmitted, wantExact)
	}
	if math.Abs(gotZero-wantSlab) > 1e-12 {
		t.Errorf("depth-0 p = %v, want %v (margins summed over the dim-0 slab)", gotZero, wantSlab)
	}
	if gotOmitted == gotZero {
		t.Fatalf("omitted depth and depth 0 produced the same p (%v); the *int is collapsing to its zero value", gotZero)
	}
}

// --- The prefix slab -------------------------------------------------

// The slab sums only the rows that SHARE the prefix. A singleton slab
// must therefore equal the unsummed margin, and only the multi-row one
// must move — an implementation that summed the whole axis would pass
// the row-0 assertion above and fail here.
func TestApplyPropZPanel_NWithinDepthSumsOnlyThePrefixSlab(t *testing.T) {
	ref, target := panelTwoDimSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0}

	layer, warns, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}

	// Row 1 is ("a","y") — same dim-0 slab as row 0, so the same
	// denominators (160 / 120) against its own cell values.
	wantRow1, ok := twoProportionZ(25, 160, 20, 120)
	if !ok {
		t.Fatal("row 1 expectation is degenerate")
	}
	approxEqual(t, "row 1 depth-0 p", pairs(t, layer, 1, 0)[0], wantRow1, 1e-12)

	// Row 2 is ("b","x") — the ONLY row with dim 0 = "b", so its slab
	// is itself and the denominator must stay the unsummed margin.
	// Summing the whole axis would give 240 / 170 here.
	wantRow2, ok := twoProportionZ(40, 80, 30, 50)
	if !ok {
		t.Fatal("row 2 expectation is degenerate")
	}
	wholeAxis, _ := twoProportionZ(40, 240, 30, 170)
	if wantRow2 == wholeAxis {
		t.Fatal("fixture cannot distinguish a singleton slab from the whole axis")
	}
	got := pairs(t, layer, 2, 0)[0]
	if math.IsNaN(got) || math.Abs(got-wantRow2) > 1e-12 {
		t.Fatalf("row 2 depth-0 p = %v, want %v (singleton slab); the whole-axis misread would give %v",
			got, wantRow2, wholeAxis)
	}
}

// The DEEPEST valid depth fixes every dim, so each slab is a single
// row and the leg collapses back to the exact margin. It is the
// opposite bound from depth 0 and pins that prefix means d+1 positions
// rather than d.
func TestApplyPropZPanel_NWithinMaxDepthEqualsExactMargin(t *testing.T) {
	ref, target := panelTwoDimSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)

	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin}
	omitted, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("omitted depth: %v", err)
	}
	// Row depth is 2, so depth 1 (prefix 2) is the deepest accepted.
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 1}
	deepest, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("depth 1: %v", err)
	}
	for r := 0; r < 3; r++ {
		a := pairs(t, omitted, r, 0)[0]
		b := pairs(t, deepest, r, 0)[0]
		if math.IsNaN(a) || math.IsNaN(b) {
			t.Fatalf("row %d: NaN (omitted %v, deepest %v)", r, a, b)
		}
		if math.Abs(a-b) > 1e-12 {
			t.Errorf("row %d: deepest-depth p = %v, want the exact-margin p %v", r, b, a)
		}
	}
}

// Each slot's slab is assembled from ITS OWN margins. The fixture
// separates that: the reference's "a" slab is 160 and the target's is
// 120, so any read against the wrong slot lands on a visibly wrong
// denominator.
//
// The reverse-order arm is a REGRESSION guard, not a discriminator
// against today's code — enumeration and margin lookup are both keyed
// by the canonical key string, so a slot's row ORDER cannot reach the
// answer and no order-swap flip can make this fail. It is here to
// catch a future rewrite that indexes slabs positionally, which is
// exactly what E3-S3's components read had to be defended from.
func TestApplyPropZPanel_NWithinResolvesSlabsPerSlot(t *testing.T) {
	ref, ordered := panelTwoDimSlots()
	// Same key SET as the reference, reversed, with values and
	// margins carried along so the per-slot answer is unchanged by
	// the reordering.
	reversed := withRowKeys(makeMatrixWithRowMargins(
		[3][3]float64{{30, 65, 50}, {20, 20, 20}, {60, 55, 45}},
		[3]float64{50, 30, 90},
	), []types.AxisKey{{"b", "x"}, {"a", "y"}, {"a", "x"}})

	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0}

	// Reference row 0 is ("a","x"): ref slab 100 + 60 = 160, target
	// slab 90 + 30 = 120. Reading the REFERENCE's slab for the target
	// would give 160 — the misread a wrong slot index produces.
	want, ok := twoProportionZ(50, 160, 60, 120)
	if !ok {
		t.Fatal("expectation is degenerate")
	}
	wrongSlot, _ := twoProportionZ(50, 160, 60, 160)
	if want == wrongSlot {
		t.Fatal("fixture cannot distinguish the target's slab from the reference's")
	}

	for _, tc := range []struct {
		name   string
		target *types.Response
	}{
		{"axis-order target", ordered},
		{"reverse-order target", reversed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layer, warns, err := applyPropZPanel(&spec, ref, []*types.Response{tc.target}, 0, []int{1})
			if err != nil {
				t.Fatalf("applyPropZPanel: %v", err)
			}
			if len(warns) != 0 {
				t.Fatalf("expected no warnings, got %v", warns)
			}
			got := pairs(t, layer, 0, 0)[0]
			if math.IsNaN(got) || math.Abs(got-want) > 1e-12 {
				t.Fatalf("p = %v, want %v (target slab 120); reading the reference's slab would give %v",
					got, want, wrongSlot)
			}
		})
	}
}

// --- No cell-value fallback ------------------------------------------

// n_within does NOT inherit the legacy <= 0 cell-value fallback. An
// unemitted margin is not a zero-sized one, and once a depth is set a
// single coordinate's cell value is not even the same dimension as a
// slab-wide sample size. The SAME fixture under row_margin_value still
// emits, which is what proves the fallback was dropped deliberately
// rather than never reached.
func TestApplyPropZPanel_NWithinDoesNotFallBackToCellValue(t *testing.T) {
	build := func() (*types.Response, *types.Response) {
		ref := makeMatrixWithRowMargins(
			[3][3]float64{{30, 50, 50}, {50, 50, 50}, {50, 50, 50}},
			[3]float64{0, 100, 100},
		)
		// Row 0 carries NO margin at all on the reference: the cell
		// is absent, so matrixRowMarginLookup has no entry for it.
		ref.Crosstab.Matrix.RowMargins[0] = types.MatrixCell{Present: false}
		target := makeMatrixWithRowMargins(
			[3][3]float64{{40, 55, 45}, {40, 40, 40}, {30, 65, 50}},
			[3]float64{100, 100, 100},
		)
		return ref, target
	}

	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin}
	ref, target := build()
	layer, warns, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 3, []int{7})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	mx := layer.Payload.Matrix
	if mx == nil {
		t.Fatal("layer matrix nil")
	}
	if mx.Cells[0][0].Present {
		t.Fatalf("coordinate (0,0) emitted %v; an unreadable row margin must skip the cell, not fall back to the cell value",
			mx.Cells[0][0].Value)
	}
	if !mx.Cells[1][0].Present {
		t.Fatal("the skip leaked beyond the unreadable row")
	}

	var found bool
	for _, w := range warns {
		if w.Details["n_missing"] != true {
			continue
		}
		found = true
		if w.Details["n_source"] != types.PanelNSourceRowMarginValueWithin {
			t.Errorf("Details[n_source] = %v, want %q", w.Details["n_source"], types.PanelNSourceRowMarginValueWithin)
		}
		if w.Details["panel_index"] != 0 {
			t.Errorf("Details[panel_index] = %v, want 0 (the reference)", w.Details["panel_index"])
		}
		if w.Details["slot_index"] != 3 {
			t.Errorf("Details[slot_index] = %v, want the authored Compose slot 3", w.Details["slot_index"])
		}
	}
	if !found {
		t.Fatalf("no n_missing warning; got %+v", warns)
	}

	// The legacy leg on the SAME fixture still emits at (0,0) by
	// substituting the cell value 30 for the missing margin.
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValue}
	ref, target = build()
	legacy, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 3, []int{7})
	if err != nil {
		t.Fatalf("legacy applyPropZPanel: %v", err)
	}
	if !legacy.Payload.Matrix.Cells[0][0].Present {
		t.Fatal("the legacy leg must still emit at (0,0); the no-fallback rule is mode-scoped")
	}
}

// A margin that IS emitted and happens to be 0 is a real 0, not an
// unreadable leg: n_within hands it to the kernel, which reports the
// degenerate pair as NaN. That is a different observable outcome from
// both the skip above and the legacy fallback, so all three states
// stay distinguishable.
func TestApplyPropZPanel_NWithinPresentZeroMarginIsNotASkip(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{{30, 50, 50}, {50, 50, 50}, {50, 50, 50}},
		[3]float64{0, 100, 100},
	)
	target := makeMatrixWithRowMargins(
		[3][3]float64{{40, 55, 45}, {40, 40, 40}, {30, 65, 50}},
		[3]float64{100, 100, 100},
	)
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin}

	layer, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	cell := layer.Payload.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatal("a present margin of 0 is a real sample size of 0, not an unreadable leg; the cell must still be emitted")
	}
	got, ok := cell.Value.([]float64)
	if !ok || len(got) != 1 {
		t.Fatalf("cell value = %#v, want a 1-pair slice", cell.Value)
	}
	if !math.IsNaN(got[0]) {
		t.Fatalf("p = %v, want NaN; n = 0 is degenerate and must be reported, not papered over", got[0])
	}
}

// --- The depth range guard -------------------------------------------

// A depth beyond the slot's row-axis dim count refuses with
// PULSE_OVERLAY_PARAM_MISSING, matching the crosstab family's
// buildPairwisePairs guard. It is a RUNTIME guard on both families —
// predict cannot see a materialised row axis.
func TestApplyPropZPanel_NWithinDepthOutOfRangeRefused(t *testing.T) {
	ref, target := panelTwoDimSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	// Row depth is 2, so depth 2 (prefix 3) is one past the end.
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 2}

	_, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err == nil {
		t.Fatal("applyPropZPanel accepted a depth past the row-axis dim count")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if coded.Code != pulseerrors.PULSE_OVERLAY_PARAM_MISSING {
		t.Fatalf("code = %q, want %q", coded.Code, pulseerrors.PULSE_OVERLAY_PARAM_MISSING)
	}
	if coded.Details["n_within_depth"] != 2 {
		t.Errorf("Details[n_within_depth] = %v, want 2", coded.Details["n_within_depth"])
	}
	if coded.Details["dim_count"] != 2 {
		t.Errorf("Details[dim_count] = %v, want 2", coded.Details["dim_count"])
	}
}

// Slots may declare DIFFERENT row-axis depths — the panel holds N + 1
// matrices where the MATRIX arm holds one, and this case has no
// precedent there. A depth valid for the reference and out of range
// for a target refuses the WHOLE spec, naming the offending slot.
//
// Refusing beats dropping the slot: M sets the length and the pair
// ordering of every cell's flattened upper-triangular vector, so a
// dropped slot would hand the caller a shorter vector with no way to
// tell which leg left. Pairing legs counted over different prefixes
// would be worse still.
func TestApplyPropZPanel_NWithinDepthRefusesOnTheShallowestSlot(t *testing.T) {
	ref, _ := panelTwoDimSlots()
	// The target declares a ONE-dim row axis. Depth 1 (prefix 2) is
	// valid for the reference and out of range here.
	shallow := withRowKeys(makeMatrixWithRowMargins(
		[3][3]float64{{60, 55, 45}, {20, 20, 20}, {30, 65, 50}},
		[3]float64{90, 70, 50},
	), []types.AxisKey{{"a"}, {"b"}, {"c"}})

	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 1}

	// Authored at Compose slots 3 (reference) and 7 (target), so a
	// diagnostic reporting the panel's internal ordering would send
	// the caller to a slot they never wrote.
	_, _, err := applyPropZPanel(&spec, ref, []*types.Response{shallow}, 3, []int{7})
	if err == nil {
		t.Fatal("applyPropZPanel accepted a depth out of range for one slot; the spec must be refused as a whole")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if coded.Code != pulseerrors.PULSE_OVERLAY_PARAM_MISSING {
		t.Fatalf("code = %q, want %q", coded.Code, pulseerrors.PULSE_OVERLAY_PARAM_MISSING)
	}
	if coded.Details["panel_index"] != 1 {
		t.Errorf("Details[panel_index] = %v, want 1 (the shallow target)", coded.Details["panel_index"])
	}
	if coded.Details["slot_index"] != 7 {
		t.Errorf("Details[slot_index] = %v, want the authored Compose slot 7", coded.Details["slot_index"])
	}
	if coded.Details["dim_count"] != 1 {
		t.Errorf("Details[dim_count] = %v, want the offending slot's depth 1", coded.Details["dim_count"])
	}

	// The guard is PER SLOT, not a uniformity check: the same pair of
	// slots at depth 0 (prefix 1) is in range for both and must not
	// be refused, even though their depths still differ.
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0}
	if _, _, err := applyPropZPanel(&spec, ref, []*types.Response{shallow}, 3, []int{7}); err != nil {
		t.Fatalf("depth 0 is in range for a 1-dim axis; differing slot depths alone must not refuse: %v", err)
	}
}

// --- n_within_depth without a mode that reads it ----------------------

// A depth set alongside a mode that does not consume it is INERT, and
// an inert param the caller believes is applied is the silent no-op
// this family refuses. The panel can make this refusal only because
// NWithinDepth is a *int: "written" is distinguishable from "zero", so
// the check cannot misfire on a caller who never named the key.
func TestApplyPropZPanel_NWithinDepthRefusedWithoutAWithinMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		nSource any
		depth   any
	}{
		{"legacy default, depth 0", "", 0},
		{"legacy default, depth 1", "", 1},
		{"row_margin_value", types.PanelNSourceRowMarginValue, 0},
		{"cell_n_unweighted", types.PanelNSourceCellNUnweighted, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, targets := panelCountedSlots()
			spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
			spec.Params = map[string]any{"n_source": tc.nSource, "n_within_depth": tc.depth}

			_, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
			if err == nil {
				t.Fatal("applyPropZPanel accepted an n_within_depth no mode reads")
			}
			var coded *pulseerrors.CodedError
			if !stderrors.As(err, &coded) {
				t.Fatalf("error is not a CodedError: %v", err)
			}
			if coded.Code != pulseerrors.PULSE_OVERLAY_PARAM_MISSING {
				t.Fatalf("code = %q, want %q", coded.Code, pulseerrors.PULSE_OVERLAY_PARAM_MISSING)
			}
			if coded.Details["n_within_depth"] != tc.depth {
				t.Errorf("Details[n_within_depth] = %v, want %v", coded.Details["n_within_depth"], tc.depth)
			}
		})
	}

	// And the same modes WITHOUT the key still run. The refusal is
	// keyed on the pointer being set, never on the mode alone.
	for _, mode := range []any{"", types.PanelNSourceRowMarginValue, types.PanelNSourceCellNUnweighted} {
		ref, targets := panelCountedSlots()
		spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
		spec.Params = map[string]any{"n_source": mode}
		if _, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2}); err != nil {
			t.Fatalf("mode %v without n_within_depth must still run: %v", mode, err)
		}
	}
}

// A negative depth refuses under the same code, before any host is
// looked at.
func TestApplyPropZPanel_NegativeNWithinDepthRefused(t *testing.T) {
	ref, target := panelTwoDimSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": -1}

	_, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) || coded.Code != pulseerrors.PULSE_OVERLAY_PARAM_MISSING {
		t.Fatalf("expected PULSE_OVERLAY_PARAM_MISSING for a negative depth, got %v", err)
	}
	if coded.Details["n_within_depth"] != -1 {
		t.Errorf("Details[n_within_depth] = %v, want -1", coded.Details["n_within_depth"])
	}
}

// The cap keeps firing FIRST, now against a depth fault too. Both arms
// must agree on which failure a doubly-wrong spec reports.
func TestApplyPropZPanel_CapFiresBeforeNWithinDepthFault(t *testing.T) {
	ref, targets := panelBaselineSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, &types.OverlayOptions{MaxPanelTargets: 1})
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": -1}

	_, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if coded.Code != pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP {
		t.Fatalf("code = %q, want the cap to short-circuit the depth gate (%q)",
			coded.Code, pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
	}
}

// --- The slab helper itself -------------------------------------------

// panelRowMarginSlabLookup omits a slab whose rows are not ALL
// readable. A partial sum would understate the denominator silently
// and be indistinguishable from a genuinely smaller subgroup.
func TestPanelRowMarginSlabLookup_PartialSlabIsUnreadable(t *testing.T) {
	resp := withRowKeys(makeMatrixWithRowMargins(
		[3][3]float64{{1, 1, 1}, {1, 1, 1}, {1, 1, 1}},
		[3]float64{100, 60, 80},
	), twoDimRowKeys)
	// Drop the margin on ("a","y") — one of the two rows in the "a"
	// slab, and NOT the anchor row the lookup is keyed from.
	resp.Crosstab.Matrix.RowMargins[1] = types.MatrixCell{Present: false}

	slot := NewComposeHostView([]*types.Response{resp}).Slot(0)
	got := panelRowMarginSlabLookup(slot, matrixRowMarginLookup(resp.Crosstab.Matrix), 1)

	if v, ok := got["a|x"]; ok {
		t.Errorf("slab a = %v, want it absent: one of its two rows carries no margin", v)
	}
	if v, ok := got["a|y"]; ok {
		t.Errorf("slab a (anchored at a|y) = %v, want it absent", v)
	}
	// The intact singleton slab is unaffected — the omission is per
	// slab, not per matrix.
	if v, ok := got["b|x"]; !ok || v != 80 {
		t.Errorf("slab b = (%v, %v), want (80, true)", v, ok)
	}
}
