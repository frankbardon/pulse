package processing

import (
	stderrors "errors"
	"math"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E7-S11 per-handler unit tests for OVERLAY_PROP_Z_PANEL — multi-ref
// COMPOSE-host pairwise two-proportion z-test panel.
//
// Test surface (per the story's acceptance criteria):
//
//   - TestApplyPropZPanel_PairwiseSig_3Targets — known pairwise p-values
//     from prepared cells (4-slot panel: reference + 3 targets).
//   - TestApplyPropZPanel_DiagonalIsOneMirroredLower — verifies the
//     pair-index ordering invariant (flattened upper-triangular row-
//     major, diagonal implicit, lower triangular implicit).
//   - TestApplyPropZPanel_OverCap_17Targets_Rejected — coded error path.
//   - TestApplyPropZPanel_DefaultMaxPanelTargets_16 — locks the default.
//   - TestApplyPropZPanel_CustomMaxPanelTargets — verifies per-spec
//     override surface.
//   - TestApplyPropZPanel_SingleTarget_DegenerateCase — only 1 target
//     means a 2-slot panel and one pair (still must work).

// composeSpecMultiTargetPropZPanel wires a multi-target
// ComposeOverlaySpec for the PROP_Z_PANEL tests. `targets` is the
// renderer-facing target slot labels; `opts` is the per-spec
// OverlayOptions slot (nil ⇒ default-16 cap, no DictPrefixFast).
func composeSpecMultiTargetPropZPanel(targets []string, opts *types.OverlayOptions) types.ComposeOverlaySpec {
	return types.ComposeOverlaySpec{
		Name:      "test_panel",
		Kind:      types.OverlayKindPropZPanel,
		Scope:     types.OverlayScopeCell,
		Reference: "baseline",
		Targets:   targets,
		Options:   opts,
	}
}

// pairs returns the flattened upper-triangular slice carried by cell
// (i, j) of a PROP_Z_PANEL layer. Helpful for tests that compare
// against pre-computed expectations.
func pairs(t *testing.T, layer types.OverlayLayer, i, j int) []float64 {
	t.Helper()
	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("layer.Payload.Shape = %q, want MATRIX", layer.Payload.Shape)
	}
	mx := layer.Payload.Matrix
	if mx == nil {
		t.Fatal("layer.Payload.Matrix nil")
	}
	if i >= len(mx.Cells) || j >= len(mx.Cells[i]) {
		t.Fatalf("cell (%d, %d) out of range", i, j)
	}
	cell := mx.Cells[i][j]
	if !cell.Present {
		t.Fatalf("cell (%d, %d) absent", i, j)
	}
	v, ok := cell.Value.([]float64)
	if !ok {
		t.Fatalf("cell (%d, %d) value not []float64: %T", i, j, cell.Value)
	}
	return v
}

// TestApplyPropZPanel_PairwiseSig_3Targets pins the per-cell pairwise
// p-values for a 4-slot panel (reference + 3 targets) against hand-
// computed expected p-values. Each slot carries an identical 3×3
// matrix of cell values so the pairwise math is predictable across
// every (i, j) pair.
//
// Cell (0,0): reference = 50/100; targets = 50/100, 60/100, 40/100.
//
//	pair (ref, t0)  : 50/100 vs 50/100 ⇒ p = 1.0 (z = 0)
//	pair (ref, t1)  : 50/100 vs 60/100 ⇒ p ≈ 0.157
//	pair (ref, t2)  : 50/100 vs 40/100 ⇒ p ≈ 0.157
//	pair (t0, t1)   : 50/100 vs 60/100 ⇒ p ≈ 0.157
//	pair (t0, t2)   : 50/100 vs 40/100 ⇒ p ≈ 0.157
//	pair (t1, t2)   : 60/100 vs 40/100 ⇒ p ≈ 0.0046
//
// Pair index ordering (M = 4 slots; 6 pairs):
//
//	idx 0 = (0,1)  idx 1 = (0,2)  idx 2 = (0,3)
//	idx 3 = (1,2)  idx 4 = (1,3)
//	idx 5 = (2,3)
func TestApplyPropZPanel_PairwiseSig_3Targets(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	t0 := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	t1 := makeMatrixWithRowMargins(
		[3][3]float64{
			{60, 60, 60},
			{60, 60, 60},
			{60, 60, 60},
		},
		[3]float64{100, 100, 100},
	)
	t2 := makeMatrixWithRowMargins(
		[3][3]float64{
			{40, 40, 40},
			{40, 40, 40},
			{40, 40, 40},
		},
		[3]float64{100, 100, 100},
	)
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1", "t2"}, nil)
	layer, _, err := applyPropZPanel(&spec, ref, []*types.Response{t0, t1, t2}, 0, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	got := pairs(t, layer, 0, 0)
	if len(got) != 6 {
		t.Fatalf("len(pairs) = %d, want 6", len(got))
	}
	// Verify the explicit slot ordering: panel index 0 = ref, 1 = t0,
	// 2 = t1, 3 = t2. Pair (0,1) is ref vs t0 = 50/100 vs 50/100 ⇒ p =
	// 1.0; pair (2,3) is t1 vs t2 = 60/100 vs 40/100 ⇒ p ≈ 0.0046.
	approxEqual(t, "pair (0,1) ref-vs-t0", got[pairIndex(0, 1, 4)], 1.0, 1e-9)
	approxEqual(t, "pair (0,2) ref-vs-t1", got[pairIndex(0, 2, 4)], 0.1573, 0.005)
	approxEqual(t, "pair (0,3) ref-vs-t2", got[pairIndex(0, 3, 4)], 0.1573, 0.005)
	approxEqual(t, "pair (1,2) t0-vs-t1", got[pairIndex(1, 2, 4)], 0.1573, 0.005)
	approxEqual(t, "pair (1,3) t0-vs-t2", got[pairIndex(1, 3, 4)], 0.1573, 0.005)
	approxEqual(t, "pair (2,3) t1-vs-t2", got[pairIndex(2, 3, 4)], 0.0046, 0.001)

	// Layer kind echoes the spec.
	if layer.Kind != types.OverlayKindPropZPanel {
		t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindPropZPanel)
	}
	// Every cell should be present (all 9 cells); summary count
	// reflects that.
	if layer.Summary == nil || layer.Summary.Count == nil || *layer.Summary.Count != 9 {
		t.Errorf("layer.Summary.Count = %v, want 9", layer.Summary)
	}
}

// TestApplyPropZPanel_DiagonalIsOneMirroredLower verifies the
// pair-index ordering invariant. The flattened slice carries the
// upper triangular (i < j) in row-major order; the diagonal is
// implicit (every diagonal entry is 1.0 — self-vs-self) and the
// lower triangular is implicit (p[j, i] == p[i, j] — symmetric
// matrix). The test reconstructs the dense M×M matrix from a known
// flattened slice and asserts symmetry + diagonal.
func TestApplyPropZPanel_DiagonalIsOneMirroredLower(t *testing.T) {
	// 3-slot panel (reference + 2 targets) gives a 3-pair flattened
	// slice: (0,1), (0,2), (1,2).
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{30, 30, 30},
			{30, 30, 30},
			{30, 30, 30},
		},
		[3]float64{100, 100, 100},
	)
	t0 := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	t1 := makeMatrixWithRowMargins(
		[3][3]float64{
			{70, 70, 70},
			{70, 70, 70},
			{70, 70, 70},
		},
		[3]float64{100, 100, 100},
	)
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
	layer, _, err := applyPropZPanel(&spec, ref, []*types.Response{t0, t1}, 0, []int{1, 2})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	got := pairs(t, layer, 0, 0)
	const m = 3
	if len(got) != pairCount(m) {
		t.Fatalf("len(pairs) = %d, want %d for M=%d", len(got), pairCount(m), m)
	}
	// Pair indices: (0,1) = 0, (0,2) = 1, (1,2) = 2.
	if pairIndex(0, 1, m) != 0 {
		t.Errorf("pairIndex(0,1,3) = %d, want 0", pairIndex(0, 1, m))
	}
	if pairIndex(0, 2, m) != 1 {
		t.Errorf("pairIndex(0,2,3) = %d, want 1", pairIndex(0, 2, m))
	}
	if pairIndex(1, 2, m) != 2 {
		t.Errorf("pairIndex(1,2,3) = %d, want 2", pairIndex(1, 2, m))
	}
	// Reconstruct the dense M×M matrix from the flattened slice via
	// the pair-index helper. Diagonal is implicit 1.0; lower
	// triangular mirrors upper.
	dense := make([][]float64, m)
	for i := range dense {
		dense[i] = make([]float64, m)
	}
	for i := 0; i < m; i++ {
		dense[i][i] = 1.0 // diagonal — implicit
	}
	for i := 0; i < m; i++ {
		for j := i + 1; j < m; j++ {
			p := got[pairIndex(i, j, m)]
			dense[i][j] = p
			dense[j][i] = p // symmetric mirror
		}
	}
	// Sanity check: every diagonal entry is exactly 1.0.
	for i := 0; i < m; i++ {
		if dense[i][i] != 1.0 {
			t.Errorf("dense[%d][%d] = %v, want 1.0 (diagonal)", i, i, dense[i][i])
		}
	}
	// Sanity check: dense[0][1] == dense[1][0] (symmetric matrix).
	if dense[0][1] != dense[1][0] {
		t.Errorf("dense[0][1] = %v, dense[1][0] = %v — must be symmetric", dense[0][1], dense[1][0])
	}
	if dense[0][2] != dense[2][0] {
		t.Errorf("dense[0][2] = %v, dense[2][0] = %v — must be symmetric", dense[0][2], dense[2][0])
	}
	if dense[1][2] != dense[2][1] {
		t.Errorf("dense[1][2] = %v, dense[2][1] = %v — must be symmetric", dense[1][2], dense[2][1])
	}
}

// TestApplyPropZPanel_OverCap_17Targets_Rejected pins the
// over-cap-rejection path: 17 targets against the default 16-target
// cap fires PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP at handler entry
// without producing any layer.
func TestApplyPropZPanel_OverCap_17Targets_Rejected(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	// 17 identical target slots — over the default 16-target cap.
	targets := make([]*types.Response, 17)
	targetIdxs := make([]int, 17)
	targetLabels := make([]string, 17)
	for i := range targets {
		targets[i] = makeMatrixWithRowMargins(
			[3][3]float64{
				{50, 50, 50},
				{50, 50, 50},
				{50, 50, 50},
			},
			[3]float64{100, 100, 100},
		)
		targetIdxs[i] = i + 1
		targetLabels[i] = "t" + string(rune('a'+i))
	}
	spec := composeSpecMultiTargetPropZPanel(targetLabels, nil)
	_, _, err := applyPropZPanel(&spec, ref, targets, 0, targetIdxs)
	if err == nil {
		t.Fatal("applyPropZPanel returned nil error against 17 targets vs default cap 16, want PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	gotCode, _ := coded.Details["code"].(string)
	if gotCode != string(pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP) {
		t.Errorf("error code = %q, want %q", gotCode, pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
	}
	observed, _ := coded.Details["observed"].(int)
	if observed != 17 {
		t.Errorf("observed = %v, want 17", observed)
	}
	capDetail, _ := coded.Details["cap"].(int)
	if capDetail != 16 {
		t.Errorf("cap = %v, want 16", capDetail)
	}
}

// TestApplyPropZPanel_DefaultMaxPanelTargets_16 locks the default cap
// value of 16 — the maximum allowed target count under default
// OverlayOptions. 16 targets must NOT trip the cap.
func TestApplyPropZPanel_DefaultMaxPanelTargets_16(t *testing.T) {
	if defaultMaxPanelTargets != 16 {
		t.Fatalf("defaultMaxPanelTargets = %d, want 16 (per interview risk paragraph)", defaultMaxPanelTargets)
	}
	if got := resolveMaxPanelTargets(nil); got != 16 {
		t.Errorf("resolveMaxPanelTargets(nil) = %d, want 16", got)
	}
	if got := resolveMaxPanelTargets(&types.OverlayOptions{}); got != 16 {
		t.Errorf("resolveMaxPanelTargets({}) = %d, want 16", got)
	}
	// Exactly 16 targets succeeds.
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	targets := make([]*types.Response, 16)
	targetIdxs := make([]int, 16)
	targetLabels := make([]string, 16)
	for i := range targets {
		targets[i] = makeMatrixWithRowMargins(
			[3][3]float64{
				{50, 50, 50},
				{50, 50, 50},
				{50, 50, 50},
			},
			[3]float64{100, 100, 100},
		)
		targetIdxs[i] = i + 1
		targetLabels[i] = "t" + string(rune('a'+i))
	}
	spec := composeSpecMultiTargetPropZPanel(targetLabels, nil)
	layer, _, err := applyPropZPanel(&spec, ref, targets, 0, targetIdxs)
	if err != nil {
		t.Fatalf("applyPropZPanel against 16 targets (at cap): %v", err)
	}
	if layer.Kind != types.OverlayKindPropZPanel {
		t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindPropZPanel)
	}
}

// TestApplyPropZPanel_CustomMaxPanelTargets pins the per-spec override
// surface. A custom cap of 3 must reject the 4-target panel and accept
// the 3-target panel.
func TestApplyPropZPanel_CustomMaxPanelTargets(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	makeTargets := func(n int) ([]*types.Response, []int, []string) {
		ts := make([]*types.Response, n)
		idxs := make([]int, n)
		labels := make([]string, n)
		for i := range ts {
			ts[i] = makeMatrixWithRowMargins(
				[3][3]float64{
					{50, 50, 50},
					{50, 50, 50},
					{50, 50, 50},
				},
				[3]float64{100, 100, 100},
			)
			idxs[i] = i + 1
			labels[i] = "t" + string(rune('a'+i))
		}
		return ts, idxs, labels
	}

	// 3 targets, cap=3 ⇒ accept.
	t3, idxs3, labels3 := makeTargets(3)
	spec := composeSpecMultiTargetPropZPanel(labels3, &types.OverlayOptions{MaxPanelTargets: 3})
	if _, _, err := applyPropZPanel(&spec, ref, t3, 0, idxs3); err != nil {
		t.Errorf("3 targets vs cap=3: %v, want nil", err)
	}

	// 4 targets, cap=3 ⇒ reject.
	t4, idxs4, labels4 := makeTargets(4)
	spec = composeSpecMultiTargetPropZPanel(labels4, &types.OverlayOptions{MaxPanelTargets: 3})
	_, _, err := applyPropZPanel(&spec, ref, t4, 0, idxs4)
	if err == nil {
		t.Fatal("4 targets vs cap=3: nil error, want PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if code, _ := coded.Details["code"].(string); code != string(pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP) {
		t.Errorf("error code = %q, want %q", code, pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
	}
	if capDetail, _ := coded.Details["cap"].(int); capDetail != 3 {
		t.Errorf("cap = %v, want 3", capDetail)
	}
}

// TestApplyPropZPanel_SingleTarget_DegenerateCase covers the
// degenerate single-target panel (2 slots: reference + 1 target). One
// pair is computed; the per-cell []float64 has length 1.
func TestApplyPropZPanel_SingleTarget_DegenerateCase(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	target := makeMatrixWithRowMargins(
		[3][3]float64{
			{60, 60, 60},
			{60, 60, 60},
			{60, 60, 60},
		},
		[3]float64{100, 100, 100},
	)
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	layer, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	got := pairs(t, layer, 0, 0)
	if len(got) != 1 {
		t.Fatalf("len(pairs) = %d, want 1 (2-slot panel)", len(got))
	}
	// pair (0, 1) = reference vs target = 50/100 vs 60/100 ⇒ p ≈
	// 0.157 (same value the OVERLAY_PROP_Z_CELL known-answer test
	// pins for the same (success, n) pair, modulo the broader
	// tolerance).
	approxEqual(t, "single pair p-value", got[0], 0.1573, 0.005)
}

// TestApplyPropZPanel_PairIndexInverse pins the pair-index helper
// against its mathematical inverse — every (i, j) pair with i < j in
// an M-slot panel must land at a unique index in [0, M*(M-1)/2).
func TestApplyPropZPanel_PairIndexInverse(t *testing.T) {
	for m := 2; m <= 8; m++ {
		seen := map[int]struct{}{}
		total := pairCount(m)
		for i := 0; i < m; i++ {
			for j := i + 1; j < m; j++ {
				idx := pairIndex(i, j, m)
				if idx < 0 || idx >= total {
					t.Errorf("pairIndex(%d,%d,%d) = %d, want in [0,%d)", i, j, m, idx, total)
					continue
				}
				if _, ok := seen[idx]; ok {
					t.Errorf("pairIndex(%d,%d,%d) = %d already seen — collision", i, j, m, idx)
				}
				seen[idx] = struct{}{}
			}
		}
		if len(seen) != total {
			t.Errorf("M=%d: covered %d / %d indices", m, len(seen), total)
		}
	}
}

// TestApplyPropZPanel_NonMatrixSlotErrors pins the defense-in-depth
// guard: a non-MATRIX target slot fires PULSE_OVERLAY_SLOT_NOT_CROSSTAB
// at handler entry (the schema-match gate normally catches this
// upstream, but the runtime arm stays observable).
func TestApplyPropZPanel_NonMatrixSlotErrors(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	// A response with no Crosstab — readMatrix returns nil.
	bogus := &types.Response{Data: []map[string]any{{"x": 1.0}}}
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	_, _, err := applyPropZPanel(&spec, ref, []*types.Response{bogus}, 0, []int{1})
	if err == nil {
		t.Fatal("applyPropZPanel against non-MATRIX target: nil error")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if code, _ := coded.Details["code"].(string); code != string(pulseerrors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB) {
		t.Errorf("error code = %q, want %q", code, pulseerrors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB)
	}
}

// TestApplyPropZPanel_AbsentSlotValueEmitsWarning pins the absent-slot
// handling: when one of the slots is missing a value at coordinate
// (i, j), the cell stays absent on the overlay and a single
// PULSE_OVERLAY_REF_ZERO warning carrying ref_missing: true surfaces.
func TestApplyPropZPanel_AbsentSlotValueEmitsWarning(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	// Target with cell (0, 0) absent (NaN value).
	target := makeMatrixWithRowMargins(
		[3][3]float64{
			{math.NaN(), 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	layer, warnings, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	// Cell (0, 0) is absent in target ⇒ overlay cell (0, 0) absent
	// + one PULSE_OVERLAY_REF_ZERO warning with ref_missing: true.
	if layer.Payload.Matrix.Cells[0][0].Present {
		t.Errorf("cell (0,0) present; want absent (target slot absent)")
	}
	// Other cells (0, 1) etc. populated normally.
	if !layer.Payload.Matrix.Cells[0][1].Present {
		t.Errorf("cell (0,1) absent; want present")
	}
	foundMissing := false
	for _, w := range warnings {
		if w.Code == string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			if missing, _ := w.Details["ref_missing"].(bool); missing {
				foundMissing = true
				break
			}
		}
	}
	if !foundMissing {
		t.Errorf("expected PULSE_OVERLAY_REF_ZERO with ref_missing: true; got %d warnings: %+v", len(warnings), warnings)
	}
}
