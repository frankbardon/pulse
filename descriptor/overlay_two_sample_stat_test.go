package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// triplebearingCrosstabHostReq returns a MATRIX-host Request whose
// crosstab cell aggregator is AGG_WELFORD (the map-valued triple
// carrier). The two-sample stat-test overlays consume the Welford
// triple's (mean, variance, n) directly so Params become optional
// when the cell aggregator carries the triple.
func triplebearingCrosstabHostReq() *types.Request {
	return &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_WELFORD, Field: "value"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
}

// scalarCrosstabHostReq returns a MATRIX-host Request whose crosstab
// cell aggregator is AGG_AVERAGE (the canonical scalar-mean carrier).
// The two-sample stat-test overlays cannot infer variance + sample
// size from a scalar mean so predict requires the four per-side Params
// keys against this host.
func scalarCrosstabHostReq() *types.Request {
	return &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "value"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
}

// triplebearingSeriesHostReq returns a SERIES-host Request whose
// aggregations include AGG_WELFORD. The two-sample stat-test VS_REF
// overlays consume the Welford triple directly so Params become
// optional when any aggregator is map-valued.
func triplebearingSeriesHostReq() *types.Request {
	return &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_WELFORD, Field: "value"},
		},
	}
}

// scalarSeriesHostReq returns a SERIES-host Request whose aggregations
// are all scalar (AGG_SUM today). The two-sample stat-test VS_REF
// overlays cannot infer variance + sample size from a scalar mean so
// predict requires the four per-side Params keys against this host.
func scalarSeriesHostReq() *types.Request {
	return &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// twoSampleStatFullParams returns the Params payload carrying all four
// per-side keys the scalar-cell predict gate requires.
func twoSampleStatFullParams() json.RawMessage {
	return json.RawMessage(`{"variance_target":1.0,"variance_ref":1.0,"sample_size_target":10,"sample_size_ref":10}`)
}

func TestValidateOverlay_TCell_TripleBearingNoParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := triplebearingCrosstabHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "t_cell",
			Kind:  types.OverlayKindTCell,
			Scope: types.OverlayScopeCell,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("unexpected PULSE_OVERLAY_PARAM_MISSING on triple-bearing TCell spec (Params optional)")
	}
	if hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Errorf("unexpected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on triple-bearing TCell spec (MATRIX host present)")
	}
}

// TestValidateOverlay_ZCell_TripleBearingNoParams asserts predict
// accepts a Params-less OVERLAY_Z_CELL spec against a MATRIX host
// whose cell aggregator is AGG_WELFORD. Same contract as TCell — the
// runtime handler reads the triple directly.
func TestValidateOverlay_ZCell_TripleBearingNoParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := triplebearingCrosstabHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "z_cell",
			Kind:  types.OverlayKindZCell,
			Scope: types.OverlayScopeCell,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("unexpected PULSE_OVERLAY_PARAM_MISSING on triple-bearing ZCell spec (Params optional)")
	}
	if hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Errorf("unexpected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on triple-bearing ZCell spec (MATRIX host present)")
	}
}

// TestValidateOverlay_TCell_ScalarMissingParams asserts predict rejects
// a Params-less OVERLAY_T_CELL spec against a MATRIX host whose cell
// aggregator is scalar (AGG_AVERAGE). The runtime handler falls back
// to var=1.0, n=2 defaults silently — predict surfaces the four
// missing keys explicitly so callers cannot silently accept the
// runtime defaults.
func TestValidateOverlay_TCell_ScalarMissingParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := scalarCrosstabHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "t_cell",
			Kind:  types.OverlayKindTCell,
			Scope: types.OverlayScopeCell,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("expected PULSE_OVERLAY_PARAM_MISSING on Params-less TCell spec against scalar mean aggregator")
	}
}

// TestValidateOverlay_ZCell_ScalarMissingParams asserts predict rejects
// a Params-less OVERLAY_Z_CELL spec against a scalar-mean MATRIX host.
// Mirror of TestValidateOverlay_TCell_ScalarMissingParams.
func TestValidateOverlay_ZCell_ScalarMissingParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := scalarCrosstabHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "z_cell",
			Kind:  types.OverlayKindZCell,
			Scope: types.OverlayScopeCell,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("expected PULSE_OVERLAY_PARAM_MISSING on Params-less ZCell spec against scalar mean aggregator")
	}
}

// TestValidateOverlay_TCell_ScalarFullParams asserts predict accepts an
// OVERLAY_T_CELL spec against a scalar-mean MATRIX host when all four
// per-side Params keys are populated.
func TestValidateOverlay_TCell_ScalarFullParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := scalarCrosstabHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:   "t_cell",
			Kind:   types.OverlayKindTCell,
			Scope:  types.OverlayScopeCell,
			Params: twoSampleStatFullParams(),
		},
	}

	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("unexpected PULSE_OVERLAY_PARAM_MISSING on TCell spec with all per-side Params present")
	}
}

// TestValidateOverlay_ZCell_ScalarFullParams asserts predict accepts an
// OVERLAY_Z_CELL spec against a scalar-mean MATRIX host when all four
// per-side Params keys are populated. Mirror of TCell_ScalarFullParams.
func TestValidateOverlay_ZCell_ScalarFullParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := scalarCrosstabHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:   "z_cell",
			Kind:   types.OverlayKindZCell,
			Scope:  types.OverlayScopeCell,
			Params: twoSampleStatFullParams(),
		},
	}

	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("unexpected PULSE_OVERLAY_PARAM_MISSING on ZCell spec with all per-side Params present")
	}
}

// TestValidateOverlay_TVsRef_TripleBearingNoParams asserts predict
// accepts a Params-less OVERLAY_T_VS_REF spec against a SERIES host
// whose aggregations include AGG_WELFORD. The runtime handler reads
// the triple's (mean, variance, n) directly so Params are optional.
func TestValidateOverlay_TVsRef_TripleBearingNoParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := triplebearingSeriesHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "t_vs_ref",
			Kind:  types.OverlayKindTVsRef,
			Scope: types.OverlayScopeGroup,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("unexpected PULSE_OVERLAY_PARAM_MISSING on triple-bearing TVsRef spec (Params optional)")
	}
	if hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Errorf("unexpected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on triple-bearing TVsRef spec (SERIES host present)")
	}
}

// TestValidateOverlay_ZVsRef_TripleBearingNoParams mirrors
// TestValidateOverlay_TVsRef_TripleBearingNoParams for OVERLAY_Z_VS_REF.
func TestValidateOverlay_ZVsRef_TripleBearingNoParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := triplebearingSeriesHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "z_vs_ref",
			Kind:  types.OverlayKindZVsRef,
			Scope: types.OverlayScopeGroup,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("unexpected PULSE_OVERLAY_PARAM_MISSING on triple-bearing ZVsRef spec (Params optional)")
	}
	if hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Errorf("unexpected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on triple-bearing ZVsRef spec (SERIES host present)")
	}
}

// TestValidateOverlay_TVsRef_ScalarMissingParams asserts predict
// rejects a Params-less OVERLAY_T_VS_REF spec against a SERIES host
// whose aggregations are all scalar (AGG_SUM). Mirror of
// TestValidateOverlay_TCell_ScalarMissingParams for the SERIES-host
// arm.
func TestValidateOverlay_TVsRef_ScalarMissingParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := scalarSeriesHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "t_vs_ref",
			Kind:  types.OverlayKindTVsRef,
			Scope: types.OverlayScopeGroup,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("expected PULSE_OVERLAY_PARAM_MISSING on Params-less TVsRef spec against scalar SERIES host")
	}
}

// TestValidateOverlay_ZVsRef_ScalarMissingParams mirrors
// TestValidateOverlay_TVsRef_ScalarMissingParams for OVERLAY_Z_VS_REF.
func TestValidateOverlay_ZVsRef_ScalarMissingParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := scalarSeriesHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "z_vs_ref",
			Kind:  types.OverlayKindZVsRef,
			Scope: types.OverlayScopeGroup,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Errorf("expected PULSE_OVERLAY_PARAM_MISSING on Params-less ZVsRef spec against scalar SERIES host")
	}
}

// TestValidateOverlay_TCell_RejectsSeriesHost asserts predict rejects an
// OVERLAY_T_CELL spec against a SERIES host (the kind requires a MATRIX
// host so the per-cell test has a cell grid to walk).
func TestValidateOverlay_TCell_RejectsSeriesHost(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := triplebearingSeriesHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "t_cell",
			Kind:  types.OverlayKindTCell,
			Scope: types.OverlayScopeCell,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Errorf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on TCell spec against SERIES host")
	}
}

// TestValidateOverlay_TVsRef_RejectsMatrixHost asserts predict rejects an
// OVERLAY_T_VS_REF spec against a MATRIX host (the kind requires a
// SERIES host so the per-group test has a series to walk).
func TestValidateOverlay_TVsRef_RejectsMatrixHost(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := triplebearingCrosstabHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "t_vs_ref",
			Kind:  types.OverlayKindTVsRef,
			Scope: types.OverlayScopeGroup,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Errorf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on TVsRef spec against MATRIX host")
	}
}

// TestValidateOverlay_TCell_RejectsNonCellScope asserts predict rejects
// an OVERLAY_T_CELL spec whose scope is not CELL (e.g. ROW). The kind
// is CELL-scoped by construction.
func TestValidateOverlay_TCell_RejectsNonCellScope(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := triplebearingCrosstabHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "t_cell_row",
			Kind:  types.OverlayKindTCell,
			Scope: types.OverlayScopeRow,
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
		t.Errorf("expected PULSE_OVERLAY_SCOPE_UNSUPPORTED on TCell spec with non-CELL scope")
	}
}
