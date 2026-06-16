package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func nestedCrosstabHostSpec() *types.CrosstabSpec {
	return &types.CrosstabSpec{
		Rows: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
			{Type: types.GROUP_CATEGORY, Field: "segment"},
		},
		Columns: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "segment"},
		},
		Cell:  &types.Aggregation{Type: types.AGG_COUNT, Field: "value"},
		Shape: types.CrosstabShapeMatrix,
	}
}

// TestValidateOverlay_LevelOutOfRange asserts that a SHARE_OF_ROW
// overlay with Level >= row-axis-depth surfaces
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on the envelope. Mirrors the
// crosstab PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE gate.
func TestValidateOverlay_LevelOutOfRange(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	// Row axis has 2 groupers; Level=2 is out of range.
	req := &types.Request{
		Crosstab: nestedCrosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindShareOfRow,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
				Level: 2,
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	if !hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
		t.Fatalf("expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on Level=2 over 2-deep row axis; got envelope errors: %+v", env.Errors)
	}
}

// TestValidateOverlay_WithinOutOfRange asserts that a SHARE_OF_ROW
// overlay with Within >= column-axis-depth surfaces
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on the envelope. The crosstab
// host's column axis is 1-deep, so Within=1 is out of range.
func TestValidateOverlay_WithinOutOfRange(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: nestedCrosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindShareOfRow,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
				Within: 1,
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	if !hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
		t.Fatalf("expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on Within=1 over 1-deep column axis; got envelope errors: %+v", env.Errors)
	}
}

// TestValidateOverlay_LevelOnChisqRejected asserts that the χ² /
// Fisher inferential family rejects any non-zero Level / Within slot
// with PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. Those handlers compute their
// own contingency from the host margins inline (implicit-margin
// contract); Level / Within would alter that contract and the
// statistical semantics would diverge from TEST_CHISQ / TEST_FISHER.
func TestValidateOverlay_LevelOnChisqRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: nestedCrosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqMatrix,
				Scope: types.OverlayScopeMatrix,
				Level: 1,
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	if !hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
		t.Fatalf("expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on CHISQ_MATRIX with Level=1; got envelope errors: %+v", env.Errors)
	}
}

// TestValidateOverlay_LevelInRange_HappyPath asserts that a SHARE_OF_ROW
// overlay with Level in [0, row-axis-depth) and Within in
// [0, column-axis-depth) passes the predict gate without surfacing
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE.
func TestValidateOverlay_LevelInRange_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: nestedCrosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindShareOfRow,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
				Level: 1, // valid for 2-deep row axis
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	if hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
		t.Fatalf("unexpected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on in-range Level; envelope errors: %+v", env.Errors)
	}
}
