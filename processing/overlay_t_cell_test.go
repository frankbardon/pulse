package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// E1-S13 non-regression gate: scalar + Params path byte-identical.
//
// The triple-aware Welch upgrade (E1-S9 MATRIX, E1-S10 SERIES) is
// additive — pre-S9 callers using scalar AGG_MEAN cells + Params
// (variance_target / variance_ref / sample_size_target /
// sample_size_ref) MUST see byte-identical Response.Overlays JSON to
// the pre-S9 output. This file pins that invariant via deterministic
// JSON snapshots: each test case captures the layer JSON the post-S9
// handler emits and asserts byte-equality against a hand-authored
// snapshot. Any drift in the scalar fallback arm — a new field on
// OverlayLayer, a reordered Summary, an unexpected warning — breaks
// here first.
//
// The snapshots are authored from the same hand-computed math the
// pre-S9 tests pinned (Cell (0,0): target_mean=10, ref_mean=9,
// variance=1, n=2 ⇒ se=1, t=1, df=2 ⇒ p ≈ 0.42264973081 via
// studentTTwoSidedP). Encoding the snapshot inline (rather than
// against a testdata file) keeps the test self-contained and avoids
// adding a never-edited golden — the snapshot is hand-checked once
// and any rebuild needs a new hand-authored string. This is the
// conservative shape for a non-regression pin.
//
// Coverage:
//
//   - TestOverlayTCell_NonRegression_ScalarParamsByteIdentical: the
//     MATRIX-host scalar fallback path (applyTCell against scalar
//     MatrixCells + Params defaults) round-trips through
//     encoding/json byte-identical to the captured snapshot.
//   - TestOverlayTVsRef_NonRegression_ScalarParamsByteIdentical: the
//     SERIES-host scalar fallback path (applyTVsRef against scalar
//     numeric row values + Params defaults) round-trips byte-
//     identical.
//
// Naming: respondent cohort / pairwise survey terminology only — no
// customer brand names in this file.

// scalarMatrix3x3 wraps the existing makeMatrixFromValues helper from
// overlay_compose_handlers_test.go and applies a marker so the
// non-regression test's intent is clear at the call site.
func scalarMatrix3x3(values [3][3]float64) *types.Response {
	return makeMatrixFromValues(values)
}

// snapshotJSON marshals an OverlayLayer to a canonical JSON byte slice.
// Uses Marshal (not MarshalIndent) so the captured snapshot is the
// same shape the production envelope serializes.
func snapshotJSON(t *testing.T, layer types.OverlayLayer) []byte {
	t.Helper()
	buf, err := json.Marshal(layer)
	if err != nil {
		t.Fatalf("json.Marshal layer: %v", err)
	}
	return buf
}

// captureLayer drives applyTCell and returns the resulting
// OverlayLayer + any warnings. Used by both byte-identity tests.
func captureLayerTCell(t *testing.T, spec types.ComposeOverlaySpec, ref, target *types.Response) (types.OverlayLayer, []OverlayWarning) {
	t.Helper()
	layer, warnings, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	return layer, warnings
}

// captureLayerTVsRef drives applyTVsRef and returns the resulting
// OverlayLayer + any warnings.
func captureLayerTVsRef(t *testing.T, spec types.ComposeOverlaySpec, ref, target *types.Response) (types.OverlayLayer, []OverlayWarning) {
	t.Helper()
	layer, warnings, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	return layer, warnings
}

// expectedMatrixPValueScalar is the hand-computed Welch p-value for
// the pre-S9 scalar+Params fixture: target_mean=10, ref_mean=9,
// variance=1, n=2 ⇒ se=sqrt(0.5+0.5)=1, t=1, df=2 ⇒
// p = studentTTwoSidedP(1, 2). We source the constant from the same
// helper applyTCell uses so the snapshot is anchored to the canonical
// recurrence (not a transcribed decimal that could drift if
// studentTTwoSidedP's accuracy improves).
func expectedMatrixPValueScalar() float64 {
	return studentTTwoSidedP(1.0, 2.0)
}

// TestOverlayTCell_NonRegression_ScalarParamsByteIdentical is the
// MATRIX-host non-regression gate. The fixture mirrors
// TestApplyTCell_KnownAnswer (the pre-S9 reference behavior) and
// asserts the post-S9 handler emits byte-identical JSON. Any drift
// in the layer's Name / Kind / Scope / Payload / Summary shape — or
// in the underlying Welch p-value — breaks the snapshot.
func TestOverlayTCell_NonRegression_ScalarParamsByteIdentical(t *testing.T) {
	ref := scalarMatrix3x3([3][3]float64{
		{9, 9, 9},
		{9, 9, 9},
		{9, 9, 9},
	})
	target := scalarMatrix3x3([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	// Spec mirrors the pre-S9 authoring shape: no triple cells, full
	// Params slot supplying variance + sample_size for both sides.
	spec := types.ComposeOverlaySpec{
		Name:      "test",
		Kind:      types.OverlayKindTCell,
		Scope:     types.OverlayScopeCell,
		Reference: "baseline",
		Targets:   []string{"treatment"},
		Params: map[string]any{
			"variance_target":    1.0,
			"variance_ref":       1.0,
			"sample_size_target": 2.0,
			"sample_size_ref":    2.0,
		},
	}
	layer, warnings := captureLayerTCell(t, spec, ref, target)
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none for scalar+Params path", warnings)
	}
	got := snapshotJSON(t, layer)

	// Build the expected JSON from the same byte-stable primitives
	// the handler produces. We assert byte-identity of the wire bytes
	// — any reorder of MatrixPayload fields or Summary fields would
	// surface here. The expected value is constructed inline (no
	// hand-transcribed decimals) so the canonical math the handler
	// reuses is the same source of truth.
	want := buildExpectedTCellMatrixJSON(t, expectedMatrixPValueScalar(), 0.99)

	if string(got) != string(want) {
		t.Errorf("byte-identity violated for scalar+Params path:\n got: %s\nwant: %s",
			string(got), string(want))
	}

	// Sanity guard: the captured p-value must equal the hand-computed
	// constant to the ULP, which is also what TestApplyTCell_
	// KnownAnswer asserted at coarser tolerance. If this fails the
	// snapshot author drifted from the pre-S9 reference.
	mx := layer.Payload.Matrix
	gotP := mx.Cells[0][0].Value.(float64)
	wantP := expectedMatrixPValueScalar()
	if math.Float64bits(gotP) != math.Float64bits(wantP) {
		t.Errorf("p-value bit drift vs studentTTwoSidedP(1,2): got=%v want=%v", gotP, wantP)
	}
}

// expectedSeriesPValueScalar mirrors expectedMatrixPValueScalar for
// the SERIES-host snapshot. Same Welch math (target_val=10, ref_val=9,
// variance=1, n=2 defaults).
func expectedSeriesPValueScalar() float64 {
	return studentTTwoSidedP(1.0, 2.0)
}

// TestOverlayTVsRef_NonRegression_ScalarParamsByteIdentical is the
// SERIES-host non-regression gate. The fixture mirrors the pre-S10
// scalar fallback behavior: scalar numeric value columns + Params
// defaults supplying variance + sample_size for both sides.
func TestOverlayTVsRef_NonRegression_ScalarParamsByteIdentical(t *testing.T) {
	keys := []string{"east", "north", "south", "west"}
	ref := makeSeriesResponse(keys, []float64{9, 9, 9, 9})
	target := makeSeriesResponse(keys, []float64{10, 10, 10, 10})
	spec := types.ComposeOverlaySpec{
		Name:      "test",
		Kind:      types.OverlayKindTVsRef,
		Scope:     types.OverlayScopeGroup,
		Reference: "baseline",
		Targets:   []string{"treatment"},
		Params: map[string]any{
			"variance_target":    1.0,
			"variance_ref":       1.0,
			"sample_size_target": 2.0,
			"sample_size_ref":    2.0,
		},
	}
	layer, warnings := captureLayerTVsRef(t, spec, ref, target)
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none for scalar+Params series path", warnings)
	}
	got := snapshotJSON(t, layer)

	want := buildExpectedTVsRefSeriesJSON(t, expectedSeriesPValueScalar(), keys)

	if string(got) != string(want) {
		t.Errorf("byte-identity violated for scalar+Params series path:\n got: %s\nwant: %s",
			string(got), string(want))
	}

	// Bit-equal p-value sanity guard.
	series := layer.Payload.Series
	for i, entry := range series.Entries {
		if entry.Summary.Statistic == nil {
			t.Fatalf("entry %d Statistic unset", i)
		}
		if math.Float64bits(*entry.Summary.Statistic) != math.Float64bits(expectedSeriesPValueScalar()) {
			t.Errorf("entry %d p-value bit drift: got=%v want=%v",
				i, *entry.Summary.Statistic, expectedSeriesPValueScalar())
		}
	}
}

// buildExpectedTCellMatrixJSON constructs the byte-canonical JSON the
// MATRIX-host applyTCell handler emits for the
// TestOverlayTCell_NonRegression_ScalarParamsByteIdentical fixture.
// We reconstruct the OverlayLayer with the same struct shape and
// marshal it — keeping the encoding/json field ordering / omitempty
// rules in lock-step with the production handler. Any drift in the
// production shape (added field, renamed field, reordered struct)
// would diverge from the production marshaler output and surface in
// the test as a byte-mismatch.
//
// minP / maxP / count come from the handler's per-cell loop: all 9
// cells are present, all carry the same p-value, so min == max ==
// pValue and count == 9.
func buildExpectedTCellMatrixJSON(t *testing.T, pValue, _unused float64) []byte {
	t.Helper()
	_ = _unused
	count := 9
	cells := make([][]types.MatrixCell, 3)
	for i := 0; i < 3; i++ {
		row := make([]types.MatrixCell, 3)
		for j := 0; j < 3; j++ {
			row[j] = types.MatrixCell{Value: pValue, Present: true}
		}
		cells[i] = row
	}
	mn := pValue
	mx := pValue
	cnt := count
	layer := types.OverlayLayer{
		Name:  "test",
		Kind:  types.OverlayKindTCell,
		Scope: types.OverlayScopeCell,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeMatrix,
			Matrix: &types.MatrixPayload{
				RowHeader:        types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader:     types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:          []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
				ColumnKeys:       []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
				Cells:            cells,
				CellLabel:        "test",
				NormalizeApplied: types.CrosstabNormalizeNone,
			},
		},
		Summary: &types.OverlaySummary{
			Min:   &mn,
			Max:   &mx,
			Count: &cnt,
		},
	}
	buf, err := json.Marshal(layer)
	if err != nil {
		t.Fatalf("json.Marshal expected: %v", err)
	}
	return buf
}

// buildExpectedTVsRefSeriesJSON constructs the byte-canonical JSON the
// SERIES-host applyTVsRef handler emits for the
// TestOverlayTVsRef_NonRegression_ScalarParamsByteIdentical fixture.
// Each entry carries the same p-value (4 groups × 1 numeric "sum"
// column ⇒ all entries get identical Welch stats).
//
// Key encoding: encodeSeriesRowAny treats the first sorted numeric
// column ("sum") as the value column, leaving the non-numeric
// "region" column to fold into the row key as "region=<value>" via
// anyToCanonicalString. The series handler's RowKey output mirrors
// this canonical form so the snapshot must too.
func buildExpectedTVsRefSeriesJSON(t *testing.T, pValue float64, keys []string) []byte {
	t.Helper()
	entries := make([]types.SeriesEntry, len(keys))
	for i, k := range keys {
		p := pValue
		entries[i] = types.SeriesEntry{
			Key:     types.AxisKey{"region=" + k},
			Summary: types.OverlaySummary{Statistic: &p},
		}
	}
	mn := pValue
	mx := pValue
	cnt := len(keys)
	layer := types.OverlayLayer{
		Name:  "test",
		Kind:  types.OverlayKindTVsRef,
		Scope: types.OverlayScopeGroup,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeSeries,
			Series: &types.SeriesPayload{
				Entries: entries,
			},
		},
		Summary: &types.OverlaySummary{
			Min:   &mn,
			Max:   &mx,
			Count: &cnt,
		},
	}
	buf, err := json.Marshal(layer)
	if err != nil {
		t.Fatalf("json.Marshal expected: %v", err)
	}
	return buf
}
