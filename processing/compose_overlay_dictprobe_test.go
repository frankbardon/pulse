package processing

import (
	stderrors "errors"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func dictMatrixSlot(rowField string, rowLabels []string, colField string, colLabels []string) *types.Response {
	rowKeys := make([]types.AxisKey, len(rowLabels))
	for i, lbl := range rowLabels {
		rowKeys[i] = types.AxisKey{lbl}
	}
	colKeys := make([]types.AxisKey, len(colLabels))
	for j, lbl := range colLabels {
		colKeys[j] = types.AxisKey{lbl}
	}
	cells := make([][]types.MatrixCell, len(rowLabels))
	for i := range cells {
		row := make([]types.MatrixCell, len(colLabels))
		for j := range row {
			row[j] = types.MatrixCell{Present: true, Value: 0.0}
		}
		cells[i] = row
	}
	var rowHeader, colHeader types.AxisHeader
	rowHeader.Types = []string{"GROUP_CATEGORY"}
	colHeader.Types = []string{"GROUP_CATEGORY"}
	if rowField != "" {
		rowHeader.Fields = []string{rowField}
	}
	if colField != "" {
		colHeader.Fields = []string{colField}
	}
	return &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    rowHeader,
				ColumnHeader: colHeader,
				RowKeys:      rowKeys,
				ColumnKeys:   colKeys,
				Cells:        cells,
			},
		},
	}
}

// requireDictPrefixDrift asserts the supplied error carries the
// canonical PULSE_OVERLAY_DICT_PREFIX_DRIFT code plus the expected
// reference / target labels, field name, and canonical prefix
// strings.
func requireDictPrefixDrift(t *testing.T, err error, wantReference, wantTargetLabel, wantField, wantRefPrefix, wantTargetPrefix string) {
	t.Helper()
	if err == nil {
		t.Fatal("checkDictPrefixEquality: want error, got nil")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got, _ := coded.Details["code"].(string); got != string(pulseerrors.PULSE_OVERLAY_DICT_PREFIX_DRIFT) {
		t.Fatalf("Details[code] = %q, want %q", got, pulseerrors.PULSE_OVERLAY_DICT_PREFIX_DRIFT)
	}
	if got, _ := coded.Details["reference"].(string); got != wantReference {
		t.Errorf("Details[reference] = %q, want %q", got, wantReference)
	}
	if got, _ := coded.Details["target_label"].(string); got != wantTargetLabel {
		t.Errorf("Details[target_label] = %q, want %q", got, wantTargetLabel)
	}
	if got, _ := coded.Details["field"].(string); got != wantField {
		t.Errorf("Details[field] = %q, want %q", got, wantField)
	}
	if got, _ := coded.Details["reference_dict_prefix"].(string); got != wantRefPrefix {
		t.Errorf("Details[reference_dict_prefix] = %q, want %q", got, wantRefPrefix)
	}
	if got, _ := coded.Details["target_dict_prefix"].(string); got != wantTargetPrefix {
		t.Errorf("Details[target_dict_prefix] = %q, want %q", got, wantTargetPrefix)
	}
}

// TestOverlayOptions_DictPrefixFast_Default_False asserts the safe
// default behaviour of the new OverlayOptions.DictPrefixFast knob:
// a zero-value OverlayOptions has DictPrefixFast=false, and a nil
// pointer behaves the same as the zero value (default to safe
// path). Belt-and-braces against accidental flipping of the safe
// default.
func TestOverlayOptions_DictPrefixFast_Default_False(t *testing.T) {
	var zero types.OverlayOptions
	if zero.DictPrefixFast {
		t.Errorf("zero-value OverlayOptions.DictPrefixFast = true, want false (safe default)")
	}
	var spec types.ComposeOverlaySpec
	if spec.Options != nil {
		t.Errorf("zero-value ComposeOverlaySpec.Options = %+v, want nil (omitempty)", spec.Options)
	}
}

func TestOverlay_DictDrift_ByLabelDefault(t *testing.T) {
	// Reference: rows in [a, b] order.
	ref := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"x", "y"})
	// Target: same labels, byte-swapped row order. Key-set agrees
	// (set of labels is {"a","b"} on both); the axis-key SEQUENCE
	// differs but the by-label key encoding is order-independent
	// (extractKeySet returns map[string]struct{} from the rendered
	// label strings).
	target := dictMatrixSlot("brand", []string{"b", "a"}, "region", []string{"x", "y"})

	responses := []*types.Response{ref, target}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{
		{
			Name:      "by-label-default",
			Kind:      types.OverlayKindIndexVsStage,
			Scope:     types.OverlayScopeCell,
			Reference: "baseline",
			Targets:   []string{"treatment"},
			// Options is nil — by-label join is the safe default.
		},
	}
	layers, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err != nil {
		t.Fatalf("ApplyComposeOverlays(by-label default): %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
	if layers[0].Kind != types.OverlayKindIndexVsStage {
		t.Errorf("layers[0].Kind = %q, want %q", layers[0].Kind, types.OverlayKindIndexVsStage)
	}
}

// TestOverlay_DictDrift_PrefixFastOpt_MatchingPrefix exercises the
// happy path of the opt-in fast path: both slots carry byte-equal
// dictionaries on every axis. The probe accepts, the per-kind
// stub handler runs, and one layer is emitted.
func TestOverlay_DictDrift_PrefixFastOpt_MatchingPrefix(t *testing.T) {
	ref := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"x", "y"})
	target := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"x", "y"})

	responses := []*types.Response{ref, target}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{
		{
			Name:      "fast-path-match",
			Kind:      types.OverlayKindIndexVsStage,
			Scope:     types.OverlayScopeCell,
			Reference: "baseline",
			Targets:   []string{"treatment"},
			Options:   &types.OverlayOptions{DictPrefixFast: true},
		},
	}
	layers, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err != nil {
		t.Fatalf("ApplyComposeOverlays(matching prefix): %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
}

// TestOverlay_DictDrift_PrefixFastOpt_MismatchedPrefix exercises
// the failure arm of the opt-in fast path: ref ["a", "b"], target
// ["b", "a"] (same labels, swapped order) → probe fails with
// PULSE_OVERLAY_DICT_PREFIX_DRIFT at position 0 (the row axis).
// The Details payload carries the canonical prefix strings up to
// and including the divergence.
func TestOverlay_DictDrift_PrefixFastOpt_MismatchedPrefix(t *testing.T) {
	ref := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"x", "y"})
	target := dictMatrixSlot("brand", []string{"b", "a"}, "region", []string{"x", "y"})

	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
		Options:   &types.OverlayOptions{DictPrefixFast: true},
	}
	err := checkDictPrefixEquality(ref, []*types.Response{target}, spec, 0)
	requireDictPrefixDrift(t, err,
		"baseline", "treatment", "brand",
		"a", "b", // ref renders first divergence; target renders first divergence
	)
}

// TestApplyComposeOverlays_DictDrift_ByLabelHappyPath exercises the
// end-to-end chassis pass for the SAFE default path. The
// reference and target carry the same label set in differing
// orders (rows ["a","b"] vs ["b","a"]); the key-set + schema +
// shape gates accept (set equality is label-based), the dict
// probe does NOT run (Options.DictPrefixFast=false), and the
// chassis emits one stub layer per spec.
func TestApplyComposeOverlays_DictDrift_ByLabelHappyPath(t *testing.T) {
	ref := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"x", "y"})
	target := dictMatrixSlot("brand", []string{"b", "a"}, "region", []string{"x", "y"})

	responses := []*types.Response{ref, target}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{
		{
			Kind:      types.OverlayKindIndexVsStage,
			Reference: "baseline",
			Targets:   []string{"treatment"},
			// No Options → DictPrefixFast=false → by-label safe path.
		},
	}
	layers, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err != nil {
		t.Fatalf("ApplyComposeOverlays(by-label happy path): %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
}

// TestApplyComposeOverlays_DictDrift_FastPathFailsLoud exercises
// the end-to-end chassis failure for the opt-in fast path. With
// DictPrefixFast=true the chassis runs the probe AFTER the key-set
// + schema + shape gates accept; the probe surfaces
// PULSE_OVERLAY_DICT_PREFIX_DRIFT and the chassis returns the
// coded error without dispatching the per-kind handler.
func TestApplyComposeOverlays_DictDrift_FastPathFailsLoud(t *testing.T) {
	ref := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"x", "y"})
	target := dictMatrixSlot("brand", []string{"b", "a"}, "region", []string{"x", "y"})

	responses := []*types.Response{ref, target}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{
		{
			Kind:      types.OverlayKindIndexVsStage,
			Reference: "baseline",
			Targets:   []string{"treatment"},
			Options:   &types.OverlayOptions{DictPrefixFast: true},
		},
	}
	layers, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err == nil {
		t.Fatalf("ApplyComposeOverlays(fast path mismatched prefix): want error, got nil; layers=%+v", layers)
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got, _ := coded.Details["code"].(string); got != string(pulseerrors.PULSE_OVERLAY_DICT_PREFIX_DRIFT) {
		t.Fatalf("Details[code] = %q, want %q", got, pulseerrors.PULSE_OVERLAY_DICT_PREFIX_DRIFT)
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil on probe failure", layers)
	}
}

// TestOverlay_DictDrift_PrefixFastOpt_TargetExtendsReference exercises
// the prefix-extension arm: target dictionary EXTENDS the reference
// dictionary (ref ["a","b"], target ["a","b","c"]). The probe must
// accept because every position in the common-length comparison
// agrees byte-equal — extension on top of a matching prefix is
// allowed (mirrors encoding.isPrefix / encoding.ValidateDictPrefixRule
// semantics for sharded cohorts).
//
// Note: at this gate the upstream key-set check would normally reject
// the slot pair because the target has an extra row key ("c") not
// present on the reference. We bypass that here by calling
// checkDictPrefixEquality directly — the unit-level test stays focused
// on the probe's prefix-extension contract while the end-to-end
// chassis-level paths are exercised by the other tests in this file.
func TestOverlay_DictDrift_PrefixFastOpt_TargetExtendsReference(t *testing.T) {
	ref := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"x", "y"})
	target := dictMatrixSlot("brand", []string{"a", "b", "c"}, "region", []string{"x", "y"})

	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
		Options:   &types.OverlayOptions{DictPrefixFast: true},
	}
	if err := checkDictPrefixEquality(ref, []*types.Response{target}, spec, 0); err != nil {
		t.Fatalf("checkDictPrefixEquality(target extends reference): want nil, got %v", err)
	}
}

// TestOverlay_DictDrift_PrefixFastOpt_ColumnAxisDivergence exercises
// the per-axis arm: row axis matches byte-equal, column axis
// diverges at position 0. The probe must surface
// PULSE_OVERLAY_DICT_PREFIX_DRIFT with field="region" (the column
// axis's field name).
func TestOverlay_DictDrift_PrefixFastOpt_ColumnAxisDivergence(t *testing.T) {
	ref := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"x", "y"})
	target := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"y", "x"})

	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
		Options:   &types.OverlayOptions{DictPrefixFast: true},
	}
	err := checkDictPrefixEquality(ref, []*types.Response{target}, spec, 0)
	requireDictPrefixDrift(t, err,
		"baseline", "treatment", "region",
		"x", "y", // ref first divergence at position 0 (x); target first divergence (y)
	)
}

// TestOverlay_DictDrift_PrefixFastOpt_NilOptionsSkipsProbe exercises
// the dispatch-side gating: when Options is nil OR DictPrefixFast
// is false, the probe MUST NOT run. We construct a divergent slot
// pair (would fail the probe if it ran) and confirm
// ApplyComposeOverlays accepts because the safe default path is
// engaged.
func TestOverlay_DictDrift_PrefixFastOpt_NilOptionsSkipsProbe(t *testing.T) {
	ref := dictMatrixSlot("brand", []string{"a", "b"}, "region", []string{"x", "y"})
	target := dictMatrixSlot("brand", []string{"b", "a"}, "region", []string{"x", "y"})

	responses := []*types.Response{ref, target}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{
		{
			Kind:      types.OverlayKindIndexVsStage,
			Reference: "baseline",
			Targets:   []string{"treatment"},
			Options:   &types.OverlayOptions{DictPrefixFast: false}, // explicit false
		},
	}
	if _, _, err := ApplyComposeOverlays(specs, responses, labels); err != nil {
		t.Fatalf("ApplyComposeOverlays(DictPrefixFast=false, divergent prefix): want nil, got %v", err)
	}
}

// TestOverlay_DictDrift_PrefixFastOpt_NonMatrixHostSkipsProbe
// exercises the host-shape arm: when both slots are SERIES (no
// MatrixPayload), the probe degenerates to a no-op even when
// DictPrefixFast=true. The probe only consults categorical
// dictionaries at the MATRIX-host axis layer.
func TestOverlay_DictDrift_PrefixFastOpt_NonMatrixHostSkipsProbe(t *testing.T) {
	ref := composeSlotOf(types.OverlayShapeSeries)
	target := composeSlotOf(types.OverlayShapeSeries)

	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
		Options:   &types.OverlayOptions{DictPrefixFast: true},
	}
	if err := checkDictPrefixEquality(ref, []*types.Response{target}, spec, 0); err != nil {
		t.Fatalf("checkDictPrefixEquality(SERIES host, fast path): want nil (no-op), got %v", err)
	}
}
