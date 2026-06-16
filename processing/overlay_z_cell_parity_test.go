package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// nativeZTwoSampleP runs the TEST_Z_TWO_SAMPLE row-test surface for
// one tuple and returns the p-value the row-test emits.
func nativeZTwoSampleP(t *testing.T, targetStream, refStream []float64) (float64, bool) {
	t.Helper()
	schema := twoSampleFixtureSchema()
	spec := &types.Test{
		Type:    types.TEST_Z_TWO_SAMPLE,
		Field:   "revenue",
		SplitBy: "treatment",
		Alpha:   0.05,
	}
	rt, err := newZTestRow(spec, schema)
	if err != nil {
		t.Fatalf("newZTestRow: %v", err)
	}
	// Group key "a" sorts before "b" so the Finalize sees
	// (groupA = target_stream, groupB = ref_stream). The z statistic is
	// (mean(A) - mean(B)) / se — symmetric in |z|, so the two-sided
	// p-value does not depend on this ordering.
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
		// Degenerate arm — TEST_Z_TWO_SAMPLE surfaced a coded error.
		// The overlay also produces NaN in this region, so the parity
		// assertion downstream treats both sides as "unavailable".
		return 0, false
	}
	return res.PValue, true
}

// overlayZFromTriples drives the OVERLAY_Z_CELL handler against a 1×1
// triple-bearing crosstab and returns the per-cell p-value. Reuses the
// same makeSingleCellTripleResponse fixture the T parity test exercises
// — both overlay families consume the identical WelfordTriple cell
// shape AGG_WELFORD emits.
func overlayZFromTriples(t *testing.T, target, ref WelfordTriple) (float64, bool) {
	t.Helper()
	targetResp := makeSingleCellTripleResponse(target)
	refResp := makeSingleCellTripleResponse(ref)
	spec := composeSpecMatrixRef(types.OverlayKindZCell, nil)
	layer, _, err := applyZCell(&spec, refResp, []*types.Response{targetResp}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	if layer.Payload.Matrix == nil {
		t.Fatal("applyZCell: nil matrix payload")
	}
	cell := layer.Payload.Matrix.Cells[0][0]
	if !cell.Present {
		return math.NaN(), false
	}
	v, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("applyZCell: cell value not float64: %T", cell.Value)
	}
	if math.IsNaN(v) {
		return math.NaN(), false
	}
	return v, true
}

// overlayZFromTriplesSeries drives the OVERLAY_Z_VS_REF series handler
// against a 1-entry triple-bearing series and returns the per-group
// p-value. Reuses makeSeriesResponseTriples (the SERIES-host triple
// fixture the OVERLAY_Z_VS_REF unit tests already exercise).
func overlayZFromTriplesSeries(t *testing.T, target, ref WelfordTriple) (float64, bool) {
	t.Helper()
	targetResp := makeSeriesResponseTriples([]string{"cohort"}, []WelfordTriple{target})
	refResp := makeSeriesResponseTriples([]string{"cohort"}, []WelfordTriple{ref})
	spec := composeSpecSeriesRef(types.OverlayKindZVsRef, nil)
	layer, _, err := applyZVsRef(&spec, refResp, []*types.Response{targetResp}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZVsRef: %v", err)
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

func TestOverlayZCell_ParityWithTestZTwoSample(t *testing.T) {
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

			pNative, nativeOK := nativeZTwoSampleP(t, tc.targetStream, tc.refStream)
			pOverlay, overlayOK := overlayZFromTriples(t, targetTriple, refTriple)

			if nativeOK != overlayOK {
				t.Fatalf("availability divergence: TEST_Z_TWO_SAMPLE ok=%v overlay ok=%v",
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
				t.Errorf("byte-equal parity violated: TEST_Z_TWO_SAMPLE p=%v (bits=0x%x), overlay p=%v (bits=0x%x)",
					pNative, bitsNative, pOverlay, bitsOverlay)
			}
		})
	}
}

func TestOverlayZVsRef_ParityWithTestZTwoSample(t *testing.T) {
	tuples := parityTuples()
	for _, tc := range tuples {
		t.Run(tc.name, func(t *testing.T) {
			targetBucket := bucketFromStream(tc.targetStream)
			refBucket := bucketFromStream(tc.refStream)
			targetTriple := tripleFromBucket(targetBucket)
			refTriple := tripleFromBucket(refBucket)

			pNative, nativeOK := nativeZTwoSampleP(t, tc.targetStream, tc.refStream)
			pOverlay, overlayOK := overlayZFromTriplesSeries(t, targetTriple, refTriple)

			if nativeOK != overlayOK {
				t.Fatalf("availability divergence: TEST_Z_TWO_SAMPLE ok=%v overlay ok=%v",
					nativeOK, overlayOK)
			}
			if !nativeOK {
				return
			}
			bitsNative := math.Float64bits(pNative)
			bitsOverlay := math.Float64bits(pOverlay)
			if bitsNative != bitsOverlay {
				t.Errorf("series byte-equal parity violated: TEST_Z_TWO_SAMPLE p=%v (bits=0x%x), overlay p=%v (bits=0x%x)",
					pNative, bitsNative, pOverlay, bitsOverlay)
			}
		})
	}
}

// TestOverlayZCell_DegenerateNaNParity exercises the N=1 and
// Variance=0 degenerate arms. Both surfaces refuse the computation in
// this region; the row test surfaces a coded error
// (PULSE_TEST_INSUFFICIENT_N / PULSE_TEST_VARIANCE_ZERO) while the
// overlay surfaces NaN. The parity claim is behavioural rather than
// bit-equal: where the row test fails, the overlay also fails (no
// spurious finite p-value emitted).
func TestOverlayZCell_DegenerateNaNParity(t *testing.T) {
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

			_, nativeOK := nativeZTwoSampleP(t, tc.targetStream, tc.refStream)
			_, overlayOK := overlayZFromTriples(t, targetTriple, refTriple)

			if nativeOK {
				t.Errorf("row test unexpectedly succeeded in degenerate arm")
			}
			if overlayOK {
				t.Errorf("overlay unexpectedly succeeded in degenerate arm")
			}
		})
	}
}

// TestOverlayZCell_HelpersReused is the "no duplication" gate for the
// Z surface. The overlay handler MUST consume welchZTest (the
// canonical z-on-means implementation in processing/overlay_z_cell.go)
// and welchZTest MUST consume standardNormalCDF (the canonical normal
// CDF helper at processing/test_stat.go). We verify behavioural
// equivalence at boundary inputs: welchZTest's output for a fixed
// (mean, var, n) pair MUST equal what zTestRow.Finalize emits for the
// same inputs driven through the same welfordBucket. The bit-equal
// assertion in TestOverlayZCell_ParityWithTestZTwoSample implicitly
// enforces this; this test stresses the entry-point reuse with a
// targeted single-tuple probe so a refactor that duplicates the
// recurrence (instead of reusing the canonical helper) breaks here
// first.
func TestOverlayZCell_HelpersReused(t *testing.T) {
	stream := makeParityStream(10.0, 2.0, 50)
	refStream := makeParityStream(9.5, 2.0, 50)
	targetBucket := bucketFromStream(stream)
	refBucket := bucketFromStream(refStream)
	targetTriple := tripleFromBucket(targetBucket)
	refTriple := tripleFromBucket(refBucket)

	// Direct invocation of welchZTest (the helper applyZCell consumes).
	pDirect, ok := welchZTest(
		targetTriple.Mean, targetTriple.Variance, float64(targetTriple.N),
		refTriple.Mean, refTriple.Variance, float64(refTriple.N))
	if !ok {
		t.Fatal("welchZTest direct invocation failed for non-degenerate inputs")
	}
	// Row-test invocation (the production TEST_Z_TWO_SAMPLE plumbing).
	pNative, nativeOK := nativeZTwoSampleP(t, stream, refStream)
	if !nativeOK {
		t.Fatal("TEST_Z_TWO_SAMPLE row test failed for non-degenerate inputs")
	}
	// Overlay invocation (the production applyZCell plumbing).
	pOverlay, overlayOK := overlayZFromTriples(t, targetTriple, refTriple)
	if !overlayOK {
		t.Fatal("applyZCell failed for non-degenerate inputs")
	}

	bitsDirect := math.Float64bits(pDirect)
	bitsNative := math.Float64bits(pNative)
	bitsOverlay := math.Float64bits(pOverlay)
	if bitsDirect != bitsNative {
		t.Errorf("welchZTest direct vs TEST_Z_TWO_SAMPLE row test bit drift: direct=%v (bits=0x%x), native=%v (bits=0x%x)",
			pDirect, bitsDirect, pNative, bitsNative)
	}
	if bitsDirect != bitsOverlay {
		t.Errorf("welchZTest direct vs applyZCell bit drift: direct=%v (bits=0x%x), overlay=%v (bits=0x%x)",
			pDirect, bitsDirect, pOverlay, bitsOverlay)
	}
}
