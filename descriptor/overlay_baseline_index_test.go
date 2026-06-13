package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict-time tests for the BaselineIndex resolver foundation
// (E4-S1). The descriptor mirror of `processing.ResolveBaselineIndex`
// surfaces PULSE_OVERLAY_REF_UNKNOWN for negative or out-of-range
// `Position` values, using schema-derivable upper bounds where
// computable and deferring to runtime otherwise.
//
// Contract enforced (matches `processing.ResolveBaselineIndex` runtime
// shape):
//
//   - Negative `Position` always rejected.
//   - `Position >= predictedLength` rejected when the schema bounds the
//     host (single-grouper GROUP_CATEGORY over a categorical_*
//     dictionary). The predict gate's series_length detail matches the
//     dict cardinality.
//   - Schema cannot bound the host (GROUP_DATE / GROUP_RANGE /
//     numeric-axis grouper / no schema) → predict defers to runtime;
//     no overlay error fires at predict time.
//   - Crosstab host → predict skips (the SERIES-host arm has no MATRIX
//     meaning); the per-kind validators already reject populated
//     BaselineIndex pointers on the MATRIX kinds.
//
// Test fixtures: a "synthetic baseline" overlay kind synthesised on the
// wire (an existing E1..E3 kind would short-circuit the predict gate
// via its own per-kind validator before BaselineIndex is consulted). We
// use the unknown-kind path: the predict gate runs
// validateOverlayBaselineIndexPredict ALONGSIDE the per-kind validator,
// so the per-kind unknown-kind error AND the baseline-index error both
// land on the envelope when the BaselineIndex slot is exercised. The
// test only asserts on the baseline-index code's presence/absence; the
// unknown-kind code is incidental.

// baselineIndexHostReq returns a single-grouper SERIES-host request
// over the `region` categorical field (2 dict entries) so the predict
// gate's schema-derived upper bound is 2.
func baselineIndexHostReq() *types.Request {
	return &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// baselineSpec returns an overlay spec carrying a Ref.BaselineIndex
// pointer with the requested Position. Kind is a synthetic
// `OVERLAY_BASELINE_INDEX_PROBE` placeholder — the per-kind validator
// surfaces PULSE_OVERLAY_KIND_UNKNOWN (incidental for these tests) but
// validateOverlayBaselineIndexPredict still runs.
func baselineSpec(position int) types.OverlaySpec {
	return types.OverlaySpec{
		Kind:  types.OverlayKind("OVERLAY_BASELINE_INDEX_PROBE"),
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			BaselineIndex: &types.OverlayBaselineIndexRef{Position: position},
		},
	}
}

// TestValidateOverlay_BaselineIndexNegative pins the negative-Position
// rejection. The predict gate surfaces PULSE_OVERLAY_REF_UNKNOWN
// regardless of whether the schema can bound the host length.
func TestValidateOverlay_BaselineIndexNegative(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, position := range []int{-1, -42} {
		req := baselineIndexHostReq()
		req.Overlays = []types.OverlaySpec{baselineSpec(position)}

		env := PredictFromBytes(data, req, nil)
		if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_UNKNOWN) {
			codes := make([]string, 0, len(env.Errors))
			for _, e := range env.Errors {
				codes = append(codes, e.Code)
			}
			t.Fatalf("position=%d: expected PULSE_OVERLAY_REF_UNKNOWN; got %v",
				position, codes)
		}
	}
}

// TestValidateOverlay_BaselineIndexExceedsKnownCardinality pins the
// schema-derivable out-of-range rejection. The host's `region` field
// is categorical_u8 with 2 dict entries, so Position >= 2 is out of
// range at predict time.
func TestValidateOverlay_BaselineIndexExceedsKnownCardinality(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, position := range []int{2, 3, 99} {
		req := baselineIndexHostReq()
		req.Overlays = []types.OverlaySpec{baselineSpec(position)}

		env := PredictFromBytes(data, req, nil)
		if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_UNKNOWN) {
			codes := make([]string, 0, len(env.Errors))
			for _, e := range env.Errors {
				codes = append(codes, e.Code)
			}
			t.Fatalf("position=%d: expected PULSE_OVERLAY_REF_UNKNOWN; got %v",
				position, codes)
		}
	}
}

// TestValidateOverlay_BaselineIndexInRangeAccepted pins the
// schema-derivable in-range acceptance. Position values strictly less
// than the dict cardinality pass the baseline-index gate (other gates
// — unknown kind, etc. — may still fire, but PULSE_OVERLAY_REF_UNKNOWN
// must NOT).
func TestValidateOverlay_BaselineIndexInRangeAccepted(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, position := range []int{0, 1} {
		req := baselineIndexHostReq()
		req.Overlays = []types.OverlaySpec{baselineSpec(position)}

		env := PredictFromBytes(data, req, nil)
		if hasErrorCode(env, errors.PULSE_OVERLAY_REF_UNKNOWN) {
			t.Fatalf("position=%d: did NOT expect PULSE_OVERLAY_REF_UNKNOWN (in-range)", position)
		}
	}
}

// TestValidateOverlay_BaselineIndexDefersOnNonCategoricalGrouper pins
// the defer-to-runtime branch: a SERIES host whose grouper is NOT
// GROUP_CATEGORY (here GROUP_DATE) cannot have its length bounded from
// the schema. The predict gate accepts Position values that runtime
// might still reject; the runtime resolver owns the range check.
func TestValidateOverlay_BaselineIndexDefersOnNonCategoricalGrouper(t *testing.T) {
	// Build a schema with a date field so the request can use
	// GROUP_DATE without referencing a categorical column.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "day", Type: encoding.FieldTypeDate,
				Description: "Date axis used for the GROUP_DATE defer-to-runtime test"},
			{Name: "value", Type: encoding.FieldTypeF64,
				Description: "Numeric revenue value field for analytics"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_DATE, Field: "day", Params: []byte(`{"interval":"day"}`)},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
		Overlays: []types.OverlaySpec{baselineSpec(9999)},
	}
	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_REF_UNKNOWN) {
		t.Fatalf("GROUP_DATE grouper: did NOT expect PULSE_OVERLAY_REF_UNKNOWN " +
			"(predict cannot bound host length; defer to runtime)")
	}
}

// TestValidateOverlay_BaselineIndexDefersWhenNoSeriesHost pins the
// no-SERIES-host branch: when Request.Groups is empty the predict gate
// has no axis to bound and defers to runtime. The per-kind validator
// catches the missing-host case for shipping kinds; the BaselineIndex
// gate does not double-report.
func TestValidateOverlay_BaselineIndexDefersWhenNoSeriesHost(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "value"}},
		Overlays:     []types.OverlaySpec{baselineSpec(0)},
	}
	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_REF_UNKNOWN) {
		t.Fatalf("no SERIES host: did NOT expect PULSE_OVERLAY_REF_UNKNOWN")
	}
}

// TestValidateOverlay_BaselineIndexSkipsCrosstabHost pins the
// crosstab-host short-circuit: when Request.Crosstab is set the
// BaselineIndex gate skips. The per-kind validators (E1..E3 kinds)
// already reject populated BaselineIndex pointers on MATRIX hosts via
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_BaselineIndexSkipsCrosstabHost(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{baselineSpec(-1)},
	}
	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_REF_UNKNOWN) {
		t.Fatalf("crosstab host: did NOT expect PULSE_OVERLAY_REF_UNKNOWN " +
			"(BaselineIndex gate skips on MATRIX host)")
	}
}

// TestValidateOverlay_BaselineIndexAbsentRefSkips pins the "no
// BaselineIndex pointer" no-op: a spec without Ref.BaselineIndex never
// surfaces PULSE_OVERLAY_REF_UNKNOWN from the baseline-index gate.
func TestValidateOverlay_BaselineIndexAbsentRefSkips(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := baselineIndexHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsTotal,
			Scope: types.OverlayScopeGroup,
		},
	}
	env := PredictFromBytes(data, req, nil)
	if hasErrorCode(env, errors.PULSE_OVERLAY_REF_UNKNOWN) {
		t.Fatalf("no BaselineIndex: did NOT expect PULSE_OVERLAY_REF_UNKNOWN")
	}
}

