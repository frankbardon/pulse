package processing

import (
	stderrors "errors"
	"reflect"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// composeMatrixSlot returns a *types.Response carrying a Crosstab
// matrix whose RowKeys × ColumnKeys axis grid is exactly the
// passed-in (rowKeys, colKeys) pair. Cells are populated dense with
// Present=true / Value=0 — the key-set check is structural over the
// declared axis (row × column tuples regardless of cell presence) so
// the chassis-side gate does not depend on per-cell values.
func composeMatrixSlot(rowKeys, colKeys []types.AxisKey) *types.Response {
	cells := make([][]types.MatrixCell, len(rowKeys))
	for i := range rowKeys {
		row := make([]types.MatrixCell, len(colKeys))
		for j := range colKeys {
			row[j] = types.MatrixCell{Present: true, Value: 0.0}
		}
		cells[i] = row
	}
	return &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowKeys:    rowKeys,
				ColumnKeys: colKeys,
				Cells:      cells,
			},
		},
	}
}

// composeSeriesSlot returns a *types.Response carrying grouped
// Process Data rows whose canonical row-key string (via
// encodeSeriesRow) is each entry in `keys` paired with a constant
// numeric value column. Used by the series-arm key-set tests to
// exercise the encodeSeriesRow drop-the-value-column rule — the
// value never participates in the key-set, only the group field
// does.
func composeSeriesSlot(group string, keys []string) *types.Response {
	data := make([]map[string]any, len(keys))
	for i, k := range keys {
		data[i] = map[string]any{
			group: k,
			"sum": 1.0,
		}
	}
	return &types.Response{Data: data}
}

// requireKeySetDivergent asserts the supplied error carries the
// canonical PULSE_OVERLAY_KEY_SET_DIVERGENT code AND a `missing`
// list + `extra` list matching the expectations. Sorted-equal
// comparison so test authors do not need to thread the sort order
// through their inputs.
func requireKeySetDivergent(t *testing.T, err error, wantMissing, wantExtra []string, wantReference, wantTarget string) {
	t.Helper()
	if err == nil {
		t.Fatal("checkKeySetAlignment: want error, got nil")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got, _ := coded.Details["code"].(string); got != string(pulseerrors.PULSE_OVERLAY_KEY_SET_DIVERGENT) {
		t.Fatalf("Details[code] = %q, want %q", got, pulseerrors.PULSE_OVERLAY_KEY_SET_DIVERGENT)
	}
	if got, _ := coded.Details["reference"].(string); got != wantReference {
		t.Errorf("Details[reference] = %q, want %q", got, wantReference)
	}
	if got, _ := coded.Details["target"].(string); got != wantTarget {
		t.Errorf("Details[target] = %q, want %q", got, wantTarget)
	}
	gotMissing, _ := coded.Details["missing"].([]string)
	gotExtra, _ := coded.Details["extra"].([]string)
	if wantMissing == nil {
		wantMissing = []string{}
	}
	if wantExtra == nil {
		wantExtra = []string{}
	}
	if gotMissing == nil {
		gotMissing = []string{}
	}
	if gotExtra == nil {
		gotExtra = []string{}
	}
	if !reflect.DeepEqual(gotMissing, wantMissing) {
		t.Errorf("Details[missing] = %v, want %v", gotMissing, wantMissing)
	}
	if !reflect.DeepEqual(gotExtra, wantExtra) {
		t.Errorf("Details[extra] = %v, want %v", gotExtra, wantExtra)
	}
}

// TestValidateOverlay_KeySetDivergent_Matrix exercises the matrix
// arm: reference carries (rowA, colX) + (rowA, colY); target carries
// (rowA, colX) + (rowB, colY). The check must reject with
// PULSE_OVERLAY_KEY_SET_DIVERGENT, populate missing = [(rowA, colY)]
// (present on ref but not target) and extra = [(rowB, colY)]
// (present on target but not ref).
func TestValidateOverlay_KeySetDivergent_Matrix(t *testing.T) {
	ref := composeMatrixSlot(
		[]types.AxisKey{{"rowA"}},
		[]types.AxisKey{{"colX"}, {"colY"}},
	)
	target := composeMatrixSlot(
		[]types.AxisKey{{"rowA"}, {"rowB"}},
		[]types.AxisKey{{"colX"}, {"colY"}},
	)
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	err := checkKeySetAlignment(ref, []*types.Response{target}, spec, 0)
	requireKeySetDivergent(t, err,
		nil,
		[]string{"rowB||colX", "rowB||colY"},
		"baseline", "treatment",
	)
}

// TestValidateOverlay_KeySetDivergent_Matrix_RefSuperset exercises
// the mirror arm: reference carries the strict-superset axis grid;
// target carries a subset. The `missing` list must list the
// reference-only coordinates, the `extra` list must be empty.
func TestValidateOverlay_KeySetDivergent_Matrix_RefSuperset(t *testing.T) {
	ref := composeMatrixSlot(
		[]types.AxisKey{{"rowA"}, {"rowB"}},
		[]types.AxisKey{{"colX"}},
	)
	target := composeMatrixSlot(
		[]types.AxisKey{{"rowA"}},
		[]types.AxisKey{{"colX"}},
	)
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	err := checkKeySetAlignment(ref, []*types.Response{target}, spec, 0)
	requireKeySetDivergent(t, err,
		[]string{"rowB||colX"},
		nil,
		"baseline", "treatment",
	)
}

// TestValidateOverlay_KeySetDivergent_Series exercises the series
// arm: reference carries (region=us, sum=1) + (region=eu, sum=2);
// target carries (region=us, sum=3) + (region=apac, sum=4). The
// `missing` list lists the reference-only group key (region=eu);
// the `extra` list lists the target-only group key (region=apac).
//
// The value column ("sum") is dropped from the encoded key by
// encodeSeriesRow's first-numeric-column rule so the divergence
// surface is purely on the group field — two slots carrying the
// same group keys but different aggregator values must NOT diverge.
func TestValidateOverlay_KeySetDivergent_Series(t *testing.T) {
	ref := composeSeriesSlot("region", []string{"us", "eu"})
	target := composeSeriesSlot("region", []string{"us", "apac"})
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	err := checkKeySetAlignment(ref, []*types.Response{target}, spec, 0)
	requireKeySetDivergent(t, err,
		[]string{"region=eu"},
		[]string{"region=apac"},
		"baseline", "treatment",
	)
}

func TestValidateOverlay_KeySetAlignment_HappyPath_Matrix(t *testing.T) {
	ref := composeMatrixSlot(
		[]types.AxisKey{{"rowA"}, {"rowB"}},
		[]types.AxisKey{{"colX"}, {"colY"}},
	)
	target := composeMatrixSlot(
		[]types.AxisKey{{"rowA"}, {"rowB"}},
		[]types.AxisKey{{"colX"}, {"colY"}},
	)
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	if err := checkKeySetAlignment(ref, []*types.Response{target}, spec, 0); err != nil {
		t.Fatalf("checkKeySetAlignment(identical matrix): want nil, got %v", err)
	}
}

// TestValidateOverlay_KeySetAlignment_HappyPath_Series exercises the
// series arm under identical group keys, even when the aggregator
// values differ — value columns are not part of the key encoding.
func TestValidateOverlay_KeySetAlignment_HappyPath_Series(t *testing.T) {
	ref := &types.Response{Data: []map[string]any{
		{"region": "us", "sum": 1.0},
		{"region": "eu", "sum": 2.0},
	}}
	target := &types.Response{Data: []map[string]any{
		{"region": "us", "sum": 7.0}, // different value, same group key
		{"region": "eu", "sum": 8.0},
	}}
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	if err := checkKeySetAlignment(ref, []*types.Response{target}, spec, 0); err != nil {
		t.Fatalf("checkKeySetAlignment(identical series keys, different values): want nil, got %v", err)
	}
}

// TestValidateOverlay_KeySetAlignment_MultiTargetMixedDivergence
// asserts that with two targets — one matching the reference, one
// diverging — the check reports the divergence against the diverging
// target and the matching target does not mask the failure. The
// "first divergence wins" rule means the helper returns at the first
// failing target; the Details payload identifies that target by its
// label.
func TestValidateOverlay_KeySetAlignment_MultiTargetMixedDivergence(t *testing.T) {
	ref := composeSeriesSlot("region", []string{"us", "eu"})
	matchingTarget := composeSeriesSlot("region", []string{"us", "eu"})
	divergingTarget := composeSeriesSlot("region", []string{"us", "apac"})

	// Matching target FIRST → must walk past without erroring; the
	// diverging target second → expected to surface the error with
	// its own label.
	specMatchingFirst := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"matching", "diverging"},
	}
	err := checkKeySetAlignment(ref,
		[]*types.Response{matchingTarget, divergingTarget},
		specMatchingFirst, 0)
	requireKeySetDivergent(t, err,
		[]string{"region=eu"},
		[]string{"region=apac"},
		"baseline", "diverging",
	)

	// Diverging target FIRST → must surface the error immediately
	// without consulting the matching target afterwards.
	specDivergingFirst := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"diverging", "matching"},
	}
	err = checkKeySetAlignment(ref,
		[]*types.Response{divergingTarget, matchingTarget},
		specDivergingFirst, 0)
	requireKeySetDivergent(t, err,
		[]string{"region=eu"},
		[]string{"region=apac"},
		"baseline", "diverging",
	)
}

// TestValidateOverlay_KeySetAlignment_ScalarNoOp asserts that a
// scalar reference (no Crosstab, empty Data) short-circuits the
// alignment check regardless of target shape — scalar slots have
// no coordinate grid, so the strict-alignment contract degenerates
// to a structural no-op and the per-kind handler's scalar-arm
// owns the rest.
func TestValidateOverlay_KeySetAlignment_ScalarNoOp(t *testing.T) {
	ref := &types.Response{}
	target := &types.Response{}
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	if err := checkKeySetAlignment(ref, []*types.Response{target}, spec, 0); err != nil {
		t.Fatalf("checkKeySetAlignment(scalar / scalar): want nil, got %v", err)
	}

	seriesTarget := composeSeriesSlot("region", []string{"us"})
	if err := checkKeySetAlignment(ref, []*types.Response{seriesTarget}, spec, 0); err != nil {
		t.Fatalf("checkKeySetAlignment(scalar ref / series target): want nil, got %v", err)
	}
}

func TestValidateOverlay_KeySetAlignment_TargetScalarSkipped(t *testing.T) {
	ref := composeSeriesSlot("region", []string{"us", "eu"})
	scalarTarget := &types.Response{}
	divergingTarget := composeSeriesSlot("region", []string{"us", "apac"})

	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"scalar_skipped", "diverging"},
	}
	err := checkKeySetAlignment(ref,
		[]*types.Response{scalarTarget, divergingTarget},
		spec, 0)
	// The scalar target slips past the gate; the diverging series
	// target is what we report on.
	requireKeySetDivergent(t, err,
		[]string{"region=eu"},
		[]string{"region=apac"},
		"baseline", "diverging",
	)
}

// TestApplyComposeOverlays_KeySetDivergenceFailsLoud is the
// service-level integration: the chassis rejects a spec whose
// reference + target produce divergent series key sets with the
// canonical coded error BEFORE the per-kind dispatch. Asserts the
// `code` Detail surfaces verbatim so the orchestrator can branch on
// it the same way it branches on LookupReference / LookupTarget
// failures.
func TestApplyComposeOverlays_KeySetDivergenceFailsLoud(t *testing.T) {
	responses := []*types.Response{
		composeSeriesSlot("region", []string{"us", "eu"}),
		composeSeriesSlot("region", []string{"us", "apac"}),
	}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{{
		Name:      "diverging_keys",
		Kind:      types.OverlayKindIndexVsStage,
		Scope:     types.OverlayScopeTotal,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}}
	layers, warnings, err := ApplyComposeOverlays(specs, responses, labels)
	if err == nil {
		t.Fatal("ApplyComposeOverlays(key-set divergence): want error, got nil")
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil under coded error", layers)
	}
	if warnings != nil {
		t.Errorf("warnings = %+v, want nil under coded error", warnings)
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_KEY_SET_DIVERGENT)
}
