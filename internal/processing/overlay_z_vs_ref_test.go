package processing

import (
	"math"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func makeSeriesResponseTriples(keys []string, triples []WelfordTriple) *types.Response {
	if len(keys) != len(triples) {
		panic("makeSeriesResponseTriples: keys and triples must have the same length")
	}
	rows := make([]map[string]any, len(keys))
	for i, k := range keys {
		rows[i] = map[string]any{
			"region": k,
			"welch": map[string]any{
				"n":        int(triples[i].N),
				"n_null":   0,
				"mean":     triples[i].Mean,
				"variance": triples[i].Variance,
			},
		}
	}
	return &types.Response{Data: rows}
}

// TestApplyZVsRef_ScalarKnownAnswer pins a per-group z-test p-value
// for the scalar+Params fallback path. Both sides carry scalar means
// (10 vs 9); the Params defaults supply variance=1, n=2 ⇒
// se = sqrt(1/2 + 1/2) = 1, z = 1, p ≈ 0.3173.
func TestApplyZVsRef_ScalarKnownAnswer(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south", "east", "west"}, []float64{9, 9, 9, 9})
	target := makeSeriesResponse([]string{"north", "south", "east", "west"}, []float64{10, 10, 10, 10})
	spec := composeSpecSeriesRef(types.OverlayKindZVsRef, nil)
	layer, warnings, err := applyZVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	if layer.Kind != types.OverlayKindZVsRef {
		t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindZVsRef)
	}
	for i := 0; i < 4; i++ {
		approxEqual(t, "entry", entryStat(t, layer, i), 0.31731, 1e-4)
	}
}

// TestApplyZVsRef_TripleKnownAnswer pins a per-group z-test p-value
// for the triple-bearing path. Both sides carry WelfordTriple rows
// — Params defaults are ignored.
//
//	target = {mean: 10, var: 4, n: 10}
//	ref    = {mean:  9, var: 4, n: 10}
//	se     = sqrt(4/10 + 4/10) ≈ 0.8944
//	z      = 1 / 0.8944 ≈ 1.118
//	p      ≈ 0.2636
func TestApplyZVsRef_TripleKnownAnswer(t *testing.T) {
	targetTriples := []WelfordTriple{
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
	}
	refTriples := []WelfordTriple{
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
	}
	target := makeSeriesResponseTriples([]string{"north", "south", "east", "west"}, targetTriples)
	ref := makeSeriesResponseTriples([]string{"north", "south", "east", "west"}, refTriples)
	spec := composeSpecSeriesRef(types.OverlayKindZVsRef, nil)
	layer, warnings, err := applyZVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	for i := 0; i < 4; i++ {
		approxEqual(t, "entry (triple)", entryStat(t, layer, i), 0.26355, 1e-4)
	}
}

// TestApplyZVsRef_TripleBypassesParamsDefaults asserts the precedence
// rule: rows carrying a WelfordTriple ignore the Params defaults. We
// supply Params that, if honoured, would shift p toward 1.0; the
// triple path must produce the same answer as
// TestApplyZVsRef_TripleKnownAnswer.
func TestApplyZVsRef_TripleBypassesParamsDefaults(t *testing.T) {
	targetTriples := []WelfordTriple{
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
	}
	refTriples := []WelfordTriple{
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
	}
	target := makeSeriesResponseTriples([]string{"north", "south", "east", "west"}, targetTriples)
	ref := makeSeriesResponseTriples([]string{"north", "south", "east", "west"}, refTriples)
	spec := composeSpecSeriesRef(types.OverlayKindZVsRef, map[string]any{
		"variance_target":    100.0,
		"variance_ref":       100.0,
		"sample_size_target": 1000.0,
		"sample_size_ref":    1000.0,
	})
	layer, _, err := applyZVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZVsRef: %v", err)
	}
	for i := 0; i < 4; i++ {
		approxEqual(t, "entry (triple bypasses Params)", entryStat(t, layer, i), 0.26355, 1e-4)
	}
}

// TestApplyZVsRef_MissingRefGroup exercises the missing-ref-row arm.
// Target has 4 groups; reference has 3 — the 4th group emits
// PULSE_OVERLAY_REF_ZERO with ref_missing=true and the entry carries
// NaN.
func TestApplyZVsRef_MissingRefGroup(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south", "east"}, []float64{9, 9, 9})
	target := makeSeriesResponse([]string{"north", "south", "east", "west"}, []float64{10, 10, 10, 10})
	spec := composeSpecSeriesRef(types.OverlayKindZVsRef, nil)
	layer, warnings, err := applyZVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZVsRef: %v", err)
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
	// 4th entry — present in series, NaN statistic.
	if math.IsNaN(entryStat(t, layer, 0)) {
		t.Error("entry(0) is NaN, want real p-value")
	}
	got := entryStat(t, layer, 3)
	if !math.IsNaN(got) {
		t.Errorf("entry(3).Statistic = %v, want NaN (missing ref)", got)
	}
}

// TestApplyZVsRef_DegenerateInsufficientN exercises the n<2 arm via
// Params override. Every group should emit PULSE_OVERLAY_REF_ZERO
// and surface a NaN statistic.
func TestApplyZVsRef_DegenerateInsufficientN(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south"}, []float64{9, 9})
	target := makeSeriesResponse([]string{"north", "south"}, []float64{10, 10})
	spec := composeSpecSeriesRef(types.OverlayKindZVsRef, map[string]any{
		"sample_size_target": 1.0,
		"sample_size_ref":    1.0,
	})
	layer, warnings, err := applyZVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZVsRef: %v", err)
	}
	if len(warnings) != 2 {
		t.Errorf("len(warnings) = %d, want 2", len(warnings))
	}
	for _, w := range warnings {
		if w.Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			t.Errorf("warning.Code = %q, want %q", w.Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
		}
	}
	for i := 0; i < 2; i++ {
		if !math.IsNaN(entryStat(t, layer, i)) {
			t.Errorf("entry(%d).Statistic should be NaN", i)
		}
	}
}

// TestApplyZVsRef_DegenerateZeroVariance exercises the
// both-variances-zero arm via the triple path (the Params variance
// reader clamps non-positive defaults to 1.0, so the triple path is
// the only way to drive degenerate variance through the handler).
func TestApplyZVsRef_DegenerateZeroVariance(t *testing.T) {
	targetTriples := []WelfordTriple{
		{Mean: 10, Variance: 0, N: 10},
		{Mean: 10, Variance: 0, N: 10},
	}
	refTriples := []WelfordTriple{
		{Mean: 9, Variance: 0, N: 10},
		{Mean: 9, Variance: 0, N: 10},
	}
	target := makeSeriesResponseTriples([]string{"north", "south"}, targetTriples)
	ref := makeSeriesResponseTriples([]string{"north", "south"}, refTriples)
	spec := composeSpecSeriesRef(types.OverlayKindZVsRef, nil)
	layer, warnings, err := applyZVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZVsRef: %v", err)
	}
	if len(warnings) != 2 {
		t.Errorf("len(warnings) = %d, want 2", len(warnings))
	}
	for i := 0; i < 2; i++ {
		if !math.IsNaN(entryStat(t, layer, i)) {
			t.Errorf("entry(%d).Statistic should be NaN", i)
		}
	}
}

// TestApplyZVsRef_NilReferenceRejected asserts the structural guard:
// nil reference slot ⇒ coded error.
func TestApplyZVsRef_NilReferenceRejected(t *testing.T) {
	target := makeSeriesResponse([]string{"north"}, []float64{10})
	spec := composeSpecSeriesRef(types.OverlayKindZVsRef, nil)
	_, _, err := applyZVsRef(&spec, nil, []*types.Response{target}, 0, []int{1})
	if err == nil {
		t.Fatal("applyZVsRef: expected error for nil reference, got nil")
	}
}

// TestApplyZVsRef_MatchesMatrixForSameInputs asserts the parity
// contract: a SERIES-host group with (target_mean, ref_mean, var, n)
// produces the same p-value as a MATRIX-host cell with the same
// inputs. Cross-shape consistency keeps callers honest when they
// switch between Compose authoring surfaces.
func TestApplyZVsRef_MatchesMatrixForSameInputs(t *testing.T) {
	// SERIES: target=10, ref=9, var defaults, n defaults
	refSeries := makeSeriesResponse([]string{"only"}, []float64{9})
	targetSeries := makeSeriesResponse([]string{"only"}, []float64{10})
	specSeries := composeSpecSeriesRef(types.OverlayKindZVsRef, nil)
	layerSeries, _, err := applyZVsRef(&specSeries, refSeries, []*types.Response{targetSeries}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZVsRef: %v", err)
	}
	seriesP := entryStat(t, layerSeries, 0)

	// MATRIX: every cell (target_mean=10, ref_mean=9)
	refMatrix := makeMatrixFromValues([3][3]float64{
		{9, 9, 9},
		{9, 9, 9},
		{9, 9, 9},
	})
	targetMatrix := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	specMatrix := composeSpecMatrixRef(types.OverlayKindZCell, nil)
	layerMatrix, _, err := applyZCell(&specMatrix, refMatrix, []*types.Response{targetMatrix}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	matrixP := cellAt(t, layerMatrix, 0, 0)

	if seriesP != matrixP {
		t.Errorf("SERIES p = %v, MATRIX p = %v — parity broken", seriesP, matrixP)
	}
}
