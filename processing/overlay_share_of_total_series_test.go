package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_SHARE_OF_TOTAL SERIES-host handler
// (processing/overlay_share_of_total_series.go).
//
// E3-S3 scope:
//
//   - Sibling SERIES-host overlay kind to OVERLAY_INDEX_VS_TOTAL.
//     Dispatch registered in seriesOverlayHandlers (overlay_series.go).
//   - The acceptance criteria call for: basic series math (scale 1.0),
//     ULP-precision sum across a complete partition, zero-grand-total
//     emits PULSE_OVERLAY_REF_ZERO + NaN, absent-group passthrough
//     stays absent, streaming-vs-buffered byte-identity at the post-
//     finalize entry point, default layer-name synthesis.
//   - Tests reuse the SeriesHostView fixtures from
//     overlay_series_test.go (newStubSeriesHost) and the per-entry
//     assertion helpers established by the INDEX_VS_TOTAL tests
//     (assertSeriesEntryStatisticWithinTol).

// newShareOfTotalSeriesSpec returns the canonical happy-path SERIES
// OVERLAY_SHARE_OF_TOTAL spec the per-test fixtures consume — GROUP
// scope, empty Ref (implicit-grand-total), and a deterministic name so
// the layer's renderer-facing label is pinned in the assertions.
func newShareOfTotalSeriesSpec(name string) types.OverlaySpec {
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindShareOfTotal,
		Scope: types.OverlayScopeGroup,
		Ref:   types.OverlayRef{},
	}
}

// TestOverlay_ShareOfTotal_Series_BasicSeries pins the canonical happy
// path: three groups with equal values share the grand total, so each
// surfaces a share of 1/3. Tolerance is tight because the math is plain
// f64 division.
func TestOverlay_ShareOfTotal_Series_BasicSeries(t *testing.T) {
	keys := []types.AxisKey{{"US"}, {"CA"}, {"MX"}}
	values := []float64{100.0, 100.0, 100.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newShareOfTotalSeriesSpec("share_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}

	const want = 1.0 / 3.0
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want, 1e-12)
	}
	// Layer-level summary: baseline = 1.0 (distinct from INDEX_VS_TOTAL's
	// baseline = 100; the SHARE family centres on 1.0), Count = 3,
	// Min == Max == want.
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated")
	}
	if got := *layers[0].Summary.Baseline; got != 1.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 1.0", got)
	}
	if got, want := *layers[0].Summary.Count, 3; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
	if got := *layers[0].Summary.Min; math.Abs(got-want) > 1e-12 {
		t.Errorf("layer.Summary.Min = %v, want %v", got, want)
	}
	if got := *layers[0].Summary.Max; math.Abs(got-want) > 1e-12 {
		t.Errorf("layer.Summary.Max = %v, want %v", got, want)
	}

	// Payload shape: SERIES, with one entry per host group ordinal.
	if got, want := layers[0].Payload.Shape, types.OverlayShapeSeries; got != want {
		t.Errorf("layer.Payload.Shape = %q, want %q", got, want)
	}
	if layers[0].Payload.Series == nil {
		t.Fatalf("layer.Payload.Series nil; want populated")
	}
	if got, want := len(layers[0].Payload.Series.Entries), len(keys); got != want {
		t.Errorf("len(Entries) = %d, want %d (parallel-slice contract)", got, want)
	}
}

// TestOverlay_ShareOfTotal_Series_UnequalGroups exercises the non-
// uniform case: three groups with values 50 / 30 / 20 sum to 100 so the
// per-group shares are exactly 0.5 / 0.3 / 0.2. Verifies the per-entry
// math without divisor-induced rounding.
func TestOverlay_ShareOfTotal_Series_UnequalGroups(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{50.0, 30.0, 20.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newShareOfTotalSeriesSpec("share_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	want := []float64{0.5, 0.3, 0.2}
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want[i], 1e-12)
	}
}

// TestOverlay_ShareOfTotal_Series_SumsToOneULP is the inline ULP-
// precision smoke test the story explicitly calls out: "4 groups sum to
// 1.0 within `math.Nextafter`." A complete partition's shares MUST sum
// to exactly 1.0 (within one ULP of 1.0); any divergence indicates the
// scale factor regressed (e.g. an accidental ×100 — the INDEX_VS_TOTAL
// scaling — slipped in).
//
// Deeper coverage of the SUM=1.0 invariant across nested groupings
// lands in E3-S8.
func TestOverlay_ShareOfTotal_Series_SumsToOneULP(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	values := []float64{125.0, 375.0, 250.0, 250.0} // sum = 1000
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newShareOfTotalSeriesSpec("share_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d", len(entries), len(keys))
	}

	var sum float64
	for i, entry := range entries {
		if entry.Summary.Statistic == nil {
			t.Fatalf("entry[%d].Summary.Statistic nil; want present", i)
		}
		sum += *entry.Summary.Statistic
	}

	// ULP-precision tolerance: the sum must equal 1.0 within one ULP of
	// 1.0 (math.Nextafter(1.0, 2.0) - 1.0 = ~2.22e-16 on IEEE 754
	// double). Anything looser would let an off-by-100 scaling slip in
	// (a SHARE handler that accidentally produced 100x the value would
	// sum to 100, not 1.0).
	const oneULP = 2.220446049250313e-16 // math.Nextafter(1.0, 2.0) - 1.0
	if diff := math.Abs(sum - 1.0); diff > oneULP {
		t.Errorf("Σ shares = %v, want 1.0 within %v ULP; diff = %v",
			sum, oneULP, diff)
	}
}

// TestOverlay_ShareOfTotal_Series_NoIndexScalingMatches pins the
// kind-name distinctness contract — the SERIES SHARE_OF_TOTAL handler
// MUST NOT silently rewrite as `INDEX_VS_TOTAL / 100`. The two kinds
// share the grand-total accumulator (computeSeriesGrandTotal) but the
// scaling is distinct. This test runs both kinds on the same host and
// asserts the per-entry SHARE statistic is exactly 1/100 of the
// INDEX_VS_TOTAL statistic — anything else would indicate the SHARE
// scaling regressed.
func TestOverlay_ShareOfTotal_Series_NoIndexScalingMatches(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{50.0, 30.0, 20.0}
	host := newStubSeriesHost(keys, values)

	indexSpecs := []types.OverlaySpec{newIndexVsTotalSpec("idx_total")}
	shareSpecs := []types.OverlaySpec{newShareOfTotalSeriesSpec("share_total")}

	indexLayers, _, err := ApplyOverlaysSeries(indexSpecs, host)
	if err != nil {
		t.Fatalf("INDEX_VS_TOTAL ApplyOverlaysSeries: %v", err)
	}
	shareLayers, _, err := ApplyOverlaysSeries(shareSpecs, host)
	if err != nil {
		t.Fatalf("SHARE_OF_TOTAL ApplyOverlaysSeries: %v", err)
	}

	indexEntries := indexLayers[0].Payload.Series.Entries
	shareEntries := shareLayers[0].Payload.Series.Entries
	if len(indexEntries) != len(shareEntries) {
		t.Fatalf("entry count mismatch: index=%d share=%d",
			len(indexEntries), len(shareEntries))
	}
	for i := range indexEntries {
		idx := *indexEntries[i].Summary.Statistic
		share := *shareEntries[i].Summary.Statistic
		// share == index / 100 within tight tolerance — that's the
		// kind-distinctness contract.
		if math.Abs(idx-share*100.0) > 1e-9 {
			t.Errorf("entry[%d]: index=%v, share=%v; share*100=%v should equal index",
				i, idx, share, share*100.0)
		}
	}
}

// TestOverlay_ShareOfTotal_Series_ZeroGrandTotalEmitsWarn pins the
// zero-grand-total contract: when every group value sums to zero the
// handler emits ONE PULSE_OVERLAY_REF_ZERO warning and every present
// group entry carries a NaN Statistic. Mirrors INDEX_VS_TOTAL's
// zero-denominator emission shape (one warning per layer, not per
// cell).
func TestOverlay_ShareOfTotal_Series_ZeroGrandTotalEmitsWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{0.0, 0.0, 0.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newShareOfTotalSeriesSpec("share_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d (parallel-slice contract)",
			len(entries), len(keys))
	}
	for i, entry := range entries {
		if entry.Summary.Statistic == nil {
			t.Fatalf("entry[%d].Summary.Statistic nil; want NaN (zero grand_total path emits NaN per present entry)",
				i)
		}
		if !math.IsNaN(*entry.Summary.Statistic) {
			t.Fatalf("entry[%d].Summary.Statistic = %v, want NaN", i,
				*entry.Summary.Statistic)
		}
	}

	// Layer-level summary: zero count (no non-NaN entries seen).
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated with Count=0")
	}
	if got, want := *layers[0].Summary.Count, 0; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_ShareOfTotal_Series_AbsentGroupsStayAbsent exercises the
// absent-group contract: groups for which the resolver reports
// (0, false) surface a SeriesEntry whose Summary leaves Statistic unset.
// Absent groups do NOT contribute to the grand total.
//
// Fixture: groups [a=100, b=absent, c=200, d=absent]. The grand total
// is 100 + 200 = 300 (absent groups excluded). Present entries:
// a → 100/300 ≈ 0.333; c → 200/300 ≈ 0.667. Absent entries: nil
// Statistic.
func TestOverlay_ShareOfTotal_Series_AbsentGroupsStayAbsent(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	values := []float64{100.0, math.NaN(), 200.0, math.NaN()}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newShareOfTotalSeriesSpec("share_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d (parallel-slice contract)",
			len(entries), len(keys))
	}

	const wantA = 100.0 / 300.0
	const wantC = 200.0 / 300.0
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 0, wantA, 1e-12)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, wantC, 1e-12)

	if entries[1].Summary.Statistic != nil {
		t.Errorf("entries[1].Summary.Statistic = %v, want nil (absent group)",
			*entries[1].Summary.Statistic)
	}
	if entries[3].Summary.Statistic != nil {
		t.Errorf("entries[3].Summary.Statistic = %v, want nil (absent group)",
			*entries[3].Summary.Statistic)
	}

	// Key alignment: every entry's Key still matches the host ordinal
	// element-for-element (parallel-slice contract; absent groups DO
	// carry the key, only the metric is missing).
	for i, want := range keys {
		got := entries[i].Key
		if len(got) != len(want) {
			t.Fatalf("entries[%d].Key length = %d, want %d", i, len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("entries[%d].Key[%d] = %v, want %v", i, j, got[j], want[j])
			}
		}
	}

	// Absent groups also do NOT contribute to the grand total — verify
	// by asserting that the present shares sum to 1.0 (within ULP), not
	// less.
	sum := *entries[0].Summary.Statistic + *entries[2].Summary.Statistic
	const oneULP = 2.220446049250313e-16
	if diff := math.Abs(sum - 1.0); diff > oneULP {
		t.Errorf("Σ present shares = %v, want 1.0 within %v ULP; absent groups must not contribute to denominator",
			sum, oneULP)
	}
}

// TestOverlay_ShareOfTotal_Series_StreamingBufferedByteIdentical pins
// the streaming-vs-buffered byte-identity contract. The post-host-
// finalize entry point produces byte-identical SeriesPayload output
// whether the host was built via a streaming Process pass or a buffered
// one, because in both cases the handler consumes the same finalised
// SeriesHostView. Mirrors the INDEX_VS_TOTAL byte-identity assertion.
func TestOverlay_ShareOfTotal_Series_StreamingBufferedByteIdentical(t *testing.T) {
	keys := []types.AxisKey{{"x"}, {"y"}, {"z"}}
	values := []float64{12.0, 34.0, 56.0}

	bufHost := newStubSeriesHost(keys, values)

	finalizedMap := map[int]float64{0: 12.0, 1: 34.0, 2: 56.0}
	streamHost := NewSeriesHostView(keys, func(i int) (float64, bool) {
		v, ok := finalizedMap[i]
		return v, ok
	})

	specs := []types.OverlaySpec{newShareOfTotalSeriesSpec("share_total")}

	bufLayers, bufWarnings, err := ApplyOverlaysSeries(specs, bufHost)
	if err != nil {
		t.Fatalf("buffered ApplyOverlaysSeries: %v", err)
	}
	streamLayers, streamWarnings, err := ApplyOverlaysSeries(specs, streamHost)
	if err != nil {
		t.Fatalf("streaming ApplyOverlaysSeries: %v", err)
	}

	if len(bufWarnings) != len(streamWarnings) {
		t.Fatalf("warning slice lengths differ: buffered=%d streaming=%d",
			len(bufWarnings), len(streamWarnings))
	}

	if len(bufLayers) != len(streamLayers) {
		t.Fatalf("layer slice lengths differ: buffered=%d streaming=%d",
			len(bufLayers), len(streamLayers))
	}
	for li := range bufLayers {
		bufEntries := bufLayers[li].Payload.Series.Entries
		streamEntries := streamLayers[li].Payload.Series.Entries
		if len(bufEntries) != len(streamEntries) {
			t.Fatalf("layer[%d] entry counts differ: buffered=%d streaming=%d",
				li, len(bufEntries), len(streamEntries))
		}
		for ei := range bufEntries {
			bufStat := bufEntries[ei].Summary.Statistic
			streamStat := streamEntries[ei].Summary.Statistic
			switch {
			case bufStat == nil && streamStat == nil:
				// both absent — fine
			case bufStat == nil || streamStat == nil:
				t.Fatalf("layer[%d].entry[%d] Statistic presence differs: buffered=%v streaming=%v",
					li, ei, bufStat, streamStat)
			default:
				if *bufStat != *streamStat {
					t.Fatalf("layer[%d].entry[%d] Statistic differs: buffered=%v streaming=%v",
						li, ei, *bufStat, *streamStat)
				}
			}
		}
		if (bufLayers[li].Summary == nil) != (streamLayers[li].Summary == nil) {
			t.Fatalf("layer[%d].Summary presence differs: buffered=%v streaming=%v",
				li, bufLayers[li].Summary, streamLayers[li].Summary)
		}
		if bufLayers[li].Summary != nil {
			if *bufLayers[li].Summary.Baseline != *streamLayers[li].Summary.Baseline {
				t.Fatalf("layer[%d].Summary.Baseline differs: buffered=%v streaming=%v",
					li, *bufLayers[li].Summary.Baseline, *streamLayers[li].Summary.Baseline)
			}
			if *bufLayers[li].Summary.Count != *streamLayers[li].Summary.Count {
				t.Fatalf("layer[%d].Summary.Count differs: buffered=%v streaming=%v",
					li, *bufLayers[li].Summary.Count, *streamLayers[li].Summary.Count)
			}
		}
	}
}

// TestOverlay_ShareOfTotal_Series_ZeroGroupsClean exercises the
// degenerate zero-group host. The handler must not panic; it surfaces
// an empty Entries slice + a warning because grand_total == 0 in the
// every-group-absent case.
func TestOverlay_ShareOfTotal_Series_ZeroGroupsClean(t *testing.T) {
	host := newStubSeriesHost(nil, nil)
	specs := []types.OverlaySpec{newShareOfTotalSeriesSpec("share_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	if got := len(layers[0].Payload.Series.Entries); got != 0 {
		t.Errorf("len(Entries) = %d, want 0 (zero-group host)", got)
	}
}

// TestOverlay_ShareOfTotal_Series_DefaultLayerName pins the synthesised
// renderer-facing label when the spec omits Name. The SERIES SHARE_OF_TOTAL
// dispatch (Ref empty) surfaces the lower-case bare-kind string
// "share_of_total" mirroring the INDEX_VS_TOTAL / CHISQ_* convention.
// Distinct from the MATRIX SHARE_OF_TOTAL default
// ("OVERLAY_SHARE_OF_TOTAL_grand"), which keys off Ref.Margin being
// populated — keeping the two synthesised defaults distinct surfaces
// the dispatch routing decision at the renderer-label level.
func TestOverlay_ShareOfTotal_Series_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}}
	host := newStubSeriesHost(keys, []float64{1.0})
	specs := []types.OverlaySpec{{
		Kind:  types.OverlayKindShareOfTotal,
		Scope: types.OverlayScopeGroup,
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "share_of_total"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}
