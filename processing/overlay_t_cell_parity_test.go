package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// E1-S12 byte-equal parity gate: TEST_WELCH ↔ OVERLAY_T_CELL +
// OVERLAY_T_VS_REF. The Welch p-value the overlay surface emits MUST
// be bit-for-bit identical to the p-value TEST_WELCH (alias of TEST_T)
// computes for the same (mean, variance, n) inputs.
//
// Both surfaces share the same plumbing by construction:
//
//   - TEST_WELCH (processing/test_t.go finalizeTwoSample) consumes per-
//     group welfordBucket state, computes `va/na + vb/nb` SE, the
//     Welch-Satterthwaite df recurrence, and finishes through
//     studentTTwoSidedP.
//   - AGG_WELFORD (processing/aggregator_welford.go) emits the SAME
//     welfordBucket state as a WelfordTriple {Mean, Variance, N}.
//   - applyTCell / applyTVsRef consume a triple-bearing cell pair and
//     route into welchTTest (processing/overlay_compose_handlers.go),
//     which performs the SAME `va/na + vb/nb` SE, the SAME df
//     recurrence, and finishes through studentTTwoSidedP.
//
// The parity test exploits this: it drives one welfordBucket per group
// over a synthesized 4-group × 50-row data set, snapshots the final
// triple, builds a triple-bearing crosstab cell pair from those exact
// triples, and asserts `math.Float64bits(p_overlay) == math.Float64bits
// (p_native)` for every (target, ref) tuple. Sourcing both surfaces
// from the same welfordBucket end-state guarantees bit-for-bit
// equivalence — any drift would imply welchTTest and tTestRow.
// finalizeTwoSample diverged.
//
// Coverage:
//
//   - TestOverlayTCell_ParityWithTestWelch (MATRIX-host): 10+ distinct
//     (mean, variance, n) input tuples driven through both surfaces.
//   - TestOverlayTVsRef_ParityWithTestWelch (SERIES-host): the same
//     parity check against the OVERLAY_T_VS_REF series handler.
//   - TestOverlayTCell_DegenerateNaNParity: N=1 + Variance=0 degenerate
//     arms produce matching NaN p-values across both surfaces (welch
//     gate triggers PULSE_TEST_VARIANCE_ZERO / PULSE_TEST_INSUFFICIENT_N
//     while overlay surfaces NaN + PULSE_OVERLAY_REF_ZERO; the test
//     pins NaN parity where the row test succeeds and the overlay
//     would also succeed, and pins both-side failure where both reject).
//
// Naming convention: "respondent cohort" / "consumer survey" terminology
// only — no customer brand names anywhere in this file.

// parityTuple is one row in the parity table. Each tuple drives two
// independent group streams (target + reference) through a welfordBucket
// pair so the resulting (mean, variance, n) triples can be fed into
// both surfaces side-by-side.
type parityTuple struct {
	name       string
	// targetStream / refStream are the raw scalar inputs (50 numeric
	// observations per group) the respondent cohort produced. We drive
	// the welfordBucket over the full stream so the test exercises the
	// real recurrence — no hand-computed (mean, variance, n) shortcuts.
	targetStream []float64
	refStream    []float64
}

// makeParityStream synthesizes a deterministic length-N float64 sequence
// whose welfordBucket-emitted (mean, variance) approximates the (mu, sd)
// pair. We use a centered arithmetic progression so the recurrence runs
// over a realistic input rather than identical values (which would
// make the variance trivially zero and hide drift).
func makeParityStream(mu, sd float64, n int) []float64 {
	out := make([]float64, n)
	if n <= 0 {
		return out
	}
	// Symmetric ±k·step grid centered on mu — produces a well-behaved
	// non-degenerate (mean, variance) tuple for the welfordBucket
	// recurrence to chew through.
	step := sd
	if n > 1 {
		step = sd * math.Sqrt(12.0/float64(n*n-1)) // uniform-distribution std rule
	}
	mid := float64(n-1) / 2.0
	for i := 0; i < n; i++ {
		out[i] = mu + (float64(i)-mid)*step
	}
	return out
}

// bucketFromStream runs the welfordBucket over a value stream and
// returns the final state. Equivalent to driving TEST_T's UpdateRow
// over the same stream, then snapshotting the (n, mean, m2) triple.
func bucketFromStream(values []float64) welfordBucket {
	var b welfordBucket
	for _, v := range values {
		b.add(v)
	}
	return b
}

// tripleFromBucket folds a welfordBucket into the WelfordTriple shape
// AGG_WELFORD emits — identical conversion to aggregator_welford.go's
// Rich() method.
func tripleFromBucket(b welfordBucket) WelfordTriple {
	return WelfordTriple{
		Mean:     b.mean,
		Variance: b.sampleVariance(),
		N:        uint64(b.n),
	}
}

// nativeWelchP runs the TEST_WELCH row-test surface for one tuple and
// returns the p-value the row-test emits.
func nativeWelchP(t *testing.T, targetStream, refStream []float64) (float64, bool) {
	t.Helper()
	schema := twoSampleFixtureSchema()
	spec := &types.Test{
		Type:    types.TEST_WELCH,
		Field:   "revenue",
		SplitBy: "treatment",
		Alpha:   0.05,
	}
	rt, err := newTTestRow(spec, schema)
	if err != nil {
		t.Fatalf("newTTestRow: %v", err)
	}
	// Group key "a" sorts before "b" so finalizeTwoSample reads
	// (groupA = target_stream, groupB = ref_stream). The Welch t-stat
	// is mean(A) - mean(B) — symmetric in |t|, so two-sided p-value
	// does not depend on this ordering.
	for _, v := range targetStream {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "a")
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow target: %v", err)
		}
	}
	for _, v := range refStream {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "b")
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow ref: %v", err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		// Degenerate arm — TEST_T surfaced a coded error. The overlay
		// also produces NaN in this region, so the parity assertion
		// downstream treats both sides as "unavailable".
		return 0, false
	}
	return res.PValue, true
}

// overlayWelchPFromTriples drives the OVERLAY_T_CELL handler against
// a 1×1 triple-bearing crosstab and returns the per-cell p-value.
func overlayWelchPFromTriples(t *testing.T, target, ref WelfordTriple) (float64, bool) {
	t.Helper()
	targetResp := makeSingleCellTripleResponse(target)
	refResp := makeSingleCellTripleResponse(ref)
	spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
	layer, _, err := applyTCell(&spec, refResp, []*types.Response{targetResp}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	if layer.Payload.Matrix == nil {
		t.Fatal("applyTCell: nil matrix payload")
	}
	cell := layer.Payload.Matrix.Cells[0][0]
	if !cell.Present {
		return math.NaN(), false
	}
	v, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("applyTCell: cell value not float64: %T", cell.Value)
	}
	if math.IsNaN(v) {
		return math.NaN(), false
	}
	return v, true
}

// overlayWelchPFromTriplesSeries drives the OVERLAY_T_VS_REF series
// handler against a 1-entry triple-bearing series and returns the
// per-group p-value.
func overlayWelchPFromTriplesSeries(t *testing.T, target, ref WelfordTriple) (float64, bool) {
	t.Helper()
	targetResp := makeSeriesResponseTriples([]string{"cohort"}, []WelfordTriple{target})
	refResp := makeSeriesResponseTriples([]string{"cohort"}, []WelfordTriple{ref})
	spec := composeSpecSeriesRef(types.OverlayKindTVsRef, nil)
	layer, _, err := applyTVsRef(&spec, refResp, []*types.Response{targetResp}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) == 0 {
		return math.NaN(), false
	}
	stat := layer.Payload.Series.Entries[0].Summary.Statistic
	if stat == nil {
		return math.NaN(), false
	}
	if math.IsNaN(*stat) {
		return math.NaN(), false
	}
	return *stat, true
}

// makeSingleCellTripleResponse builds a 1×1 MATRIX-shape Response whose
// single cell carries the supplied WelfordTriple. Used by the
// OVERLAY_T_CELL parity probe so the per-cell handler sees an
// unambiguous (target_triple, ref_triple) pair on (r0, c0).
func makeSingleCellTripleResponse(triple WelfordTriple) *types.Response {
	cells := [][]types.MatrixCell{{{Value: triple, Present: true}}}
	return &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      []types.AxisKey{{"r0"}},
				ColumnKeys:   []types.AxisKey{{"c0"}},
				Cells:        cells,
			},
		},
	}
}

// parityTuples returns the ≥10 (target, ref) input pairs the parity
// test drives through both surfaces. Each entry is a deterministic
// length-50 numeric stream so the welfordBucket recurrence produces
// reproducible (mean, variance) triples.
func parityTuples() []parityTuple {
	return []parityTuple{
		{
			name:         "equal_means_unit_var",
			targetStream: makeParityStream(10.0, 1.0, 50),
			refStream:    makeParityStream(10.0, 1.0, 50),
		},
		{
			name:         "small_shift_equal_var",
			targetStream: makeParityStream(10.5, 1.0, 50),
			refStream:    makeParityStream(10.0, 1.0, 50),
		},
		{
			name:         "large_shift_equal_var",
			targetStream: makeParityStream(15.0, 2.0, 50),
			refStream:    makeParityStream(10.0, 2.0, 50),
		},
		{
			name:         "unequal_variances",
			targetStream: makeParityStream(10.0, 3.0, 50),
			refStream:    makeParityStream(10.0, 1.0, 50),
		},
		{
			name:         "unequal_sample_sizes",
			targetStream: makeParityStream(10.0, 1.5, 30),
			refStream:    makeParityStream(11.0, 1.5, 70),
		},
		{
			name:         "small_n_both",
			targetStream: makeParityStream(5.0, 1.0, 10),
			refStream:    makeParityStream(6.0, 1.0, 10),
		},
		{
			name:         "negative_means",
			targetStream: makeParityStream(-2.0, 1.5, 40),
			refStream:    makeParityStream(-3.0, 1.5, 40),
		},
		{
			name:         "wide_variance_gap",
			targetStream: makeParityStream(50.0, 0.1, 50),
			refStream:    makeParityStream(50.0, 5.0, 50),
		},
		{
			name:         "high_magnitude_means",
			targetStream: makeParityStream(1000.0, 50.0, 50),
			refStream:    makeParityStream(995.0, 50.0, 50),
		},
		{
			name:         "asymmetric_n_with_drift",
			targetStream: makeParityStream(7.5, 2.0, 25),
			refStream:    makeParityStream(7.0, 2.0, 75),
		},
		{
			name:         "fractional_means",
			targetStream: makeParityStream(0.123, 0.05, 50),
			refStream:    makeParityStream(0.118, 0.05, 50),
		},
		{
			name:         "treatment_vs_control_pairwise_survey",
			targetStream: makeParityStream(3.85, 1.2, 50),
			refStream:    makeParityStream(3.60, 1.1, 50),
		},
	}
}

// TestOverlayTCell_ParityWithTestWelch is the headline E1-S12 byte-
// equal parity gate. For each of 10+ (mean, variance, n) tuples the
// row-test surface (TEST_WELCH via newTTestRow) and the overlay
// surface (applyTCell via WelfordTriple cells) must produce the same
// float64 p-value at math.Float64bits granularity.
//
// Both surfaces source their (mean, variance, n) from the SAME
// welfordBucket instance per group so any divergence at the bit level
// would imply welchTTest and tTestRow.finalizeTwoSample drifted.
func TestOverlayTCell_ParityWithTestWelch(t *testing.T) {
	tuples := parityTuples()
	if len(tuples) < 10 {
		t.Fatalf("parityTuples returned %d, want >= 10", len(tuples))
	}
	for _, tc := range tuples {
		t.Run(tc.name, func(t *testing.T) {
			// Drive both groups through the SAME welfordBucket
			// recurrence the row test uses internally. The resulting
			// (mean, variance, n) triple is what both surfaces ingest.
			targetBucket := bucketFromStream(tc.targetStream)
			refBucket := bucketFromStream(tc.refStream)
			targetTriple := tripleFromBucket(targetBucket)
			refTriple := tripleFromBucket(refBucket)

			pNative, nativeOK := nativeWelchP(t, tc.targetStream, tc.refStream)
			pOverlay, overlayOK := overlayWelchPFromTriples(t, targetTriple, refTriple)

			if nativeOK != overlayOK {
				t.Fatalf("availability divergence: TEST_WELCH ok=%v overlay ok=%v",
					nativeOK, overlayOK)
			}
			if !nativeOK {
				// Both surfaces declined — degenerate region, no
				// bit-comparison possible.
				return
			}
			bitsNative := math.Float64bits(pNative)
			bitsOverlay := math.Float64bits(pOverlay)
			if bitsNative != bitsOverlay {
				t.Errorf("byte-equal parity violated: TEST_WELCH p=%v (bits=0x%x), overlay p=%v (bits=0x%x)",
					pNative, bitsNative, pOverlay, bitsOverlay)
			}
		})
	}
}

// TestOverlayTVsRef_ParityWithTestWelch is the SERIES-host sub-test
// (E1-S12 acceptance criterion: "OVERLAY_T_VS_REF parity sub-test
// included"). Same tuples, same welfordBucket-sourced triples, same
// byte-equal assertion — but routes through the series handler.
func TestOverlayTVsRef_ParityWithTestWelch(t *testing.T) {
	tuples := parityTuples()
	for _, tc := range tuples {
		t.Run(tc.name, func(t *testing.T) {
			targetBucket := bucketFromStream(tc.targetStream)
			refBucket := bucketFromStream(tc.refStream)
			targetTriple := tripleFromBucket(targetBucket)
			refTriple := tripleFromBucket(refBucket)

			pNative, nativeOK := nativeWelchP(t, tc.targetStream, tc.refStream)
			pOverlay, overlayOK := overlayWelchPFromTriplesSeries(t, targetTriple, refTriple)

			if nativeOK != overlayOK {
				t.Fatalf("availability divergence: TEST_WELCH ok=%v overlay ok=%v",
					nativeOK, overlayOK)
			}
			if !nativeOK {
				return
			}
			bitsNative := math.Float64bits(pNative)
			bitsOverlay := math.Float64bits(pOverlay)
			if bitsNative != bitsOverlay {
				t.Errorf("series byte-equal parity violated: TEST_WELCH p=%v (bits=0x%x), overlay p=%v (bits=0x%x)",
					pNative, bitsNative, pOverlay, bitsOverlay)
			}
		})
	}
}

// TestOverlayTCell_DegenerateNaNParity exercises the N=1 and
// Variance=0 degenerate arms. Both surfaces refuse the computation
// in this region; the row test surfaces a coded error
// (PULSE_TEST_INSUFFICIENT_N / PULSE_TEST_VARIANCE_ZERO) while the
// overlay surfaces NaN + PULSE_OVERLAY_REF_ZERO. The parity claim is
// behavioural rather than bit-equal: where the row test fails, the
// overlay also fails (no spurious finite p-value emitted).
func TestOverlayTCell_DegenerateNaNParity(t *testing.T) {
	cases := []struct {
		name         string
		targetStream []float64
		refStream    []float64
	}{
		{
			name:         "n_eq_1_target",
			targetStream: []float64{10.0},
			refStream:    makeParityStream(9.0, 1.0, 10),
		},
		{
			name:         "n_eq_1_ref",
			targetStream: makeParityStream(10.0, 1.0, 10),
			refStream:    []float64{9.0},
		},
		{
			name:         "zero_variance_both",
			targetStream: []float64{10.0, 10.0, 10.0, 10.0, 10.0},
			refStream:    []float64{9.0, 9.0, 9.0, 9.0, 9.0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targetBucket := bucketFromStream(tc.targetStream)
			refBucket := bucketFromStream(tc.refStream)
			targetTriple := tripleFromBucket(targetBucket)
			refTriple := tripleFromBucket(refBucket)

			_, nativeOK := nativeWelchP(t, tc.targetStream, tc.refStream)
			_, overlayOK := overlayWelchPFromTriples(t, targetTriple, refTriple)

			if nativeOK {
				t.Errorf("row test unexpectedly succeeded in degenerate arm")
			}
			if overlayOK {
				t.Errorf("overlay unexpectedly succeeded in degenerate arm")
			}
		})
	}
}

// TestOverlayTCell_HelpersReused is the "no duplication" gate from the
// E1-S12 acceptance criteria. The overlay handler MUST consume
// welchTTest (the canonical Welch implementation in
// processing/overlay_compose_handlers.go) and the welchTTest helper
// MUST consume studentTTwoSidedP (the canonical t-CDF helper in
// processing/welford.go). We verify behavioural equivalence at
// boundary inputs: welchTTest's output for a fixed (mean, var, n)
// pair MUST equal what tTestRow.finalizeTwoSample emits for the same
// inputs driven through the same welfordBucket. The bit-equal
// assertion in TestOverlayTCell_ParityWithTestWelch implicitly
// enforces this; this test stresses the entry-point reuse with a
// targeted single-tuple probe so a refactor that duplicates the
// recurrence (instead of reusing the canonical helper) breaks here
// first.
func TestOverlayTCell_HelpersReused(t *testing.T) {
	stream := makeParityStream(10.0, 2.0, 50)
	refStream := makeParityStream(9.5, 2.0, 50)
	targetBucket := bucketFromStream(stream)
	refBucket := bucketFromStream(refStream)
	targetTriple := tripleFromBucket(targetBucket)
	refTriple := tripleFromBucket(refBucket)

	// Direct invocation of welchTTest (the helper applyTCell consumes).
	pDirect, ok := welchTTest(
		targetTriple.Mean, targetTriple.Variance, float64(targetTriple.N),
		refTriple.Mean, refTriple.Variance, float64(refTriple.N))
	if !ok {
		t.Fatal("welchTTest direct invocation failed for non-degenerate inputs")
	}
	// Row-test invocation (the production TEST_WELCH plumbing).
	pNative, nativeOK := nativeWelchP(t, stream, refStream)
	if !nativeOK {
		t.Fatal("TEST_WELCH row test failed for non-degenerate inputs")
	}
	// Overlay invocation (the production applyTCell plumbing).
	pOverlay, overlayOK := overlayWelchPFromTriples(t, targetTriple, refTriple)
	if !overlayOK {
		t.Fatal("applyTCell failed for non-degenerate inputs")
	}

	bitsDirect := math.Float64bits(pDirect)
	bitsNative := math.Float64bits(pNative)
	bitsOverlay := math.Float64bits(pOverlay)
	if bitsDirect != bitsNative {
		t.Errorf("welchTTest direct vs TEST_WELCH row test bit drift: direct=%v (bits=0x%x), native=%v (bits=0x%x)",
			pDirect, bitsDirect, pNative, bitsNative)
	}
	if bitsDirect != bitsOverlay {
		t.Errorf("welchTTest direct vs applyTCell bit drift: direct=%v (bits=0x%x), overlay=%v (bits=0x%x)",
			pDirect, bitsDirect, pOverlay, bitsOverlay)
	}
}

// Silence unused-import warning when the schema package isn't reached
// by every test in early failure paths.
var _ = encoding.FieldTypeF64
