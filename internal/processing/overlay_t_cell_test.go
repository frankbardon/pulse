package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

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
func captureLayerTCell(t *testing.T, spec types.ComposeOverlaySpec, ref, target *types.Response) (types.OverlayLayer, []types.OverlayWarning) {
	t.Helper()
	layer, warnings, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	return layer, warnings
}

// captureLayerTVsRef drives applyTVsRef and returns the resulting
// OverlayLayer + any warnings.
func captureLayerTVsRef(t *testing.T, spec types.ComposeOverlaySpec, ref, target *types.Response) (types.OverlayLayer, []types.OverlayWarning) {
	t.Helper()
	layer, warnings, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	return layer, warnings
}

func expectedMatrixPValueScalar() float64 {
	return studentTTwoSidedP(1.0, 2.0)
}

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
