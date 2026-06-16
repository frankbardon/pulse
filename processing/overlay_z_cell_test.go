package processing

import (
	stderrors "errors"
	"math"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestApplyZCell_ScalarKnownAnswer pins a Z-cell p-value for the
// scalar+Params fallback path. Cell (0,0): target_mean=10, ref_mean=9,
// var=1, n=2 ⇒ se = sqrt(0.5+0.5) = 1, z = 1 ⇒ p ≈ 0.3173.
func TestApplyZCell_ScalarKnownAnswer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{9, 9, 9},
		{9, 9, 9},
		{9, 9, 9},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	spec := composeSpecMatrixRef(types.OverlayKindZCell, nil)
	layer, warnings, err := applyZCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	if layer.Kind != types.OverlayKindZCell {
		t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindZCell)
	}
	// se = sqrt(1/2 + 1/2) = 1; z = 1
	// p_value (two-sided) for z=1 is 2*(1 - Φ(1)) ≈ 0.31731
	got := cellAt(t, layer, 0, 0)
	approxEqual(t, "p-value", got, 0.31731, 1e-4)
}

// TestApplyZCell_TripleKnownAnswer pins a Z-cell p-value for the
// triple-bearing path. Both sides carry WelfordTriple cells:
//
//	target = {mean: 10, var: 4, n: 10}
//	ref    = {mean:  9, var: 4, n: 10}
//	se     = sqrt(4/10 + 4/10) = sqrt(0.8) ≈ 0.8944
//	z      = 1 / 0.8944 ≈ 1.118
//	p      = 2*(1 - Φ(1.118)) ≈ 0.2636
//
// Params defaults are intentionally left at their defaults (variance=1,
// n=2) — when a triple is present the handler MUST bypass the defaults
// so the test pins the triple-vs-default precedence.
func TestApplyZCell_TripleKnownAnswer(t *testing.T) {
	targetTriple := WelfordTriple{Mean: 10, Variance: 4, N: 10}
	refTriple := WelfordTriple{Mean: 9, Variance: 4, N: 10}
	target := makeMatrixFromTriples([3][3]WelfordTriple{
		{targetTriple, targetTriple, targetTriple},
		{targetTriple, targetTriple, targetTriple},
		{targetTriple, targetTriple, targetTriple},
	})
	ref := makeMatrixFromTriples([3][3]WelfordTriple{
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
	})
	spec := composeSpecMatrixRef(types.OverlayKindZCell, nil)
	layer, warnings, err := applyZCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	got := cellAt(t, layer, 0, 0)
	approxEqual(t, "p-value (triple)", got, 0.26355, 1e-4)
}

// TestApplyZCell_TripleBypassesParamsDefaults asserts the precedence
// rule: if a cell carries a WelfordTriple, the spec's Params defaults
// MUST NOT influence the cell's z-test. We pin Params variances /
// sample sizes that, if applied, would shift the p-value far from the
// triple-only answer; the assertion succeeds only when the handler
// reads (variance, n) off the triple and ignores the Params.
func TestApplyZCell_TripleBypassesParamsDefaults(t *testing.T) {
	targetTriple := WelfordTriple{Mean: 10, Variance: 4, N: 10}
	refTriple := WelfordTriple{Mean: 9, Variance: 4, N: 10}
	target := makeMatrixFromTriples([3][3]WelfordTriple{
		{targetTriple, targetTriple, targetTriple},
		{targetTriple, targetTriple, targetTriple},
		{targetTriple, targetTriple, targetTriple},
	})
	ref := makeMatrixFromTriples([3][3]WelfordTriple{
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
	})
	// Params override variance to 100 and sample size to 1000. If the
	// handler honoured them the SE would balloon and p would round
	// toward 1.0. The triple path should ignore them entirely.
	spec := composeSpecMatrixRef(types.OverlayKindZCell, map[string]any{
		"variance_target":    100.0,
		"variance_ref":       100.0,
		"sample_size_target": 1000.0,
		"sample_size_ref":    1000.0,
	})
	layer, _, err := applyZCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	got := cellAt(t, layer, 0, 0)
	// Same answer as TestApplyZCell_TripleKnownAnswer — triple wins.
	approxEqual(t, "p-value (triple bypasses Params)", got, 0.26355, 1e-4)
}

// TestApplyZCell_DegenerateInsufficientN exercises the n<2 rejection
// arm. The Params override drops sample size to 1; the handler should
// emit PULSE_OVERLAY_REF_ZERO and surface NaN on every cell.
func TestApplyZCell_DegenerateInsufficientN(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{9, 9, 9},
		{9, 9, 9},
		{9, 9, 9},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	spec := composeSpecMatrixRef(types.OverlayKindZCell, map[string]any{
		"sample_size_target": 1.0,
		"sample_size_ref":    1.0,
	})
	layer, warnings, err := applyZCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	if len(warnings) != 9 {
		t.Errorf("len(warnings) = %d, want 9", len(warnings))
	}
	for _, w := range warnings {
		if w.Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			t.Errorf("warning.Code = %q, want %q", w.Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
		}
	}
	got := cellAt(t, layer, 0, 0)
	if !math.IsNaN(got) {
		t.Errorf("cell(0,0) = %v, want NaN", got)
	}
}

// TestApplyZCell_DegenerateZeroVariance exercises the
// both-variances-zero rejection arm. The triple path is the only way
// to drive a degenerate variance through the handler — the Params
// `varianceFromParams` reader clamps non-positive values back to its
// dflt (1.0), so this test uses WelfordTriple cells with Variance=0
// on both sides to collapse the SE.
func TestApplyZCell_DegenerateZeroVariance(t *testing.T) {
	targetTriple := WelfordTriple{Mean: 10, Variance: 0, N: 10}
	refTriple := WelfordTriple{Mean: 9, Variance: 0, N: 10}
	tripleTarget := makeMatrixFromTriples([3][3]WelfordTriple{
		{targetTriple, targetTriple, targetTriple},
		{targetTriple, targetTriple, targetTriple},
		{targetTriple, targetTriple, targetTriple},
	})
	tripleRef := makeMatrixFromTriples([3][3]WelfordTriple{
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
	})
	spec := composeSpecMatrixRef(types.OverlayKindZCell, nil)
	layer, warnings, err := applyZCell(&spec, tripleRef, []*types.Response{tripleTarget}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	if len(warnings) != 9 {
		t.Errorf("len(warnings) = %d, want 9", len(warnings))
	}
	for _, w := range warnings {
		if w.Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			t.Errorf("warning.Code = %q, want %q", w.Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
		}
	}
	got := cellAt(t, layer, 0, 0)
	if !math.IsNaN(got) {
		t.Errorf("cell(0,0) = %v, want NaN", got)
	}
}

// TestApplyZCell_MissingRefCell exercises the missing-ref-cell arm.
// Target carries a non-NaN cell where the reference cell is absent —
// the handler emits PULSE_OVERLAY_REF_ZERO with ref_missing=true and
// leaves the target cell absent in the overlay layer.
func TestApplyZCell_MissingRefCell(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{math.NaN(), 9, 9},
		{9, 9, 9},
		{9, 9, 9},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	spec := composeSpecMatrixRef(types.OverlayKindZCell, nil)
	layer, warnings, err := applyZCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("len(warnings) = %d, want 1", len(warnings))
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning.Code = %q, want %q", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	if warnings[0].Details["ref_missing"] != true {
		t.Errorf("warning.Details[ref_missing] = %v, want true", warnings[0].Details["ref_missing"])
	}
	if layer.Payload.Matrix.Cells[0][0].Present {
		t.Errorf("layer.Cells[0][0].Present = true, want absent")
	}
}

// TestApplyZCell_NonMatrixSlotRejected asserts the structural guard:
// either slot lacks a matrix payload ⇒ coded error (defense in depth;
// the chassis schema-match gate normally catches this earlier).
func TestApplyZCell_NonMatrixSlotRejected(t *testing.T) {
	target := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	// Reference has no Crosstab — defense-in-depth path.
	ref := &types.Response{}
	spec := composeSpecMatrixRef(types.OverlayKindZCell, nil)
	_, _, err := applyZCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err == nil {
		t.Fatal("applyZCell: expected error for non-matrix reference, got nil")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not coded: %T (%v)", err, err)
	}
	if got := coded.Details["code"]; got != string(pulseerrors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB) {
		t.Errorf("error code = %v, want %v", got, pulseerrors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB)
	}
}

// TestWelchZTest_MatchesTestZTwoSample asserts the parity contract:
// for the same (mean, variance, n) triple, the overlay's welchZTest
// produces a p-value byte-equal to TEST_Z_TWO_SAMPLE's helper math
// in processing/test_z.go.
func TestWelchZTest_MatchesTestZTwoSample(t *testing.T) {
	// Mirror the test_z.go finalize line:
	//   p = 2 * (1 - standardNormalCDF(|z|))
	// with se = sqrt(va/na + vb/nb).
	va, na := 4.0, 10.0
	vb, nb := 4.0, 10.0
	se := math.Sqrt(va/na + vb/nb)
	z := (10.0 - 9.0) / se
	wantP := 2 * (1 - standardNormalCDF(math.Abs(z)))

	gotP, ok := welchZTest(10.0, va, na, 9.0, vb, nb)
	if !ok {
		t.Fatal("welchZTest reported degenerate where TEST_Z parity expects a real p-value")
	}
	if gotP != wantP {
		t.Errorf("welchZTest p = %v, want %v (byte-equal parity with TEST_Z helper math)", gotP, wantP)
	}
}
