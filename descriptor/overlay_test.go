package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// overlayPredictSchema returns the canonical schema the overlay-
// validator tests run against — a region / segment dimension pair
// plus a numeric value field. Mirrors crosstabPredictSchema so the
// overlay tests can re-use the same MATRIX-shaped host setup that
// the crosstab predict tests run.
func overlayPredictSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8,
				Description: "Region categorical identifier dimension",
				Dictionary:  makeDictionary(t, "north", "south")},
			{Name: "segment", Type: encoding.FieldTypeCategoricalU8,
				Description: "Customer segment identifier dimension",
				Dictionary:  makeDictionary(t, "retail", "wholesale")},
			{Name: "value", Type: encoding.FieldTypeF64,
				Description: "Numeric revenue value field for analytics"},
		},
	}
}

// crosstabHostSpec returns the minimal valid CrosstabSpec that the
// overlay validator's MATRIX-host requirement is satisfied by. Used
// by tests that need a green crosstab and care only about overlay
// behaviour.
func crosstabHostSpec() *types.CrosstabSpec {
	return &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value"},
		Shape:   types.CrosstabShapeMatrix,
	}
}

// TestValidateOverlay_KindUnknown asserts that an OverlaySpec whose
// Kind is not in AllOverlayKinds() surfaces PULSE_OVERLAY_KIND_UNKNOWN
// on the envelope. Acceptance criterion 1 of E1-S3.
func TestValidateOverlay_KindUnknown(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKind("OVERLAY_DOES_NOT_EXIST"),
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	if !hasErrorCode(env, errors.PULSE_OVERLAY_KIND_UNKNOWN) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_KIND_UNKNOWN; got %v", codes)
	}

	result, ok := env.Data.(*PredictResult)
	if !ok {
		t.Fatalf("envelope Data is not *PredictResult: %T", env.Data)
	}
	if result.Valid {
		t.Errorf("PredictResult.Valid = true; expected false (unknown overlay kind)")
	}
}

// TestValidateOverlay_RefIncompatibleShape asserts the three ways an
// OVERLAY_INDEX_VS_MARGIN overlay can fail the ref/shape compatibility
// gate:
//
//  1. Ref.Margin is nil (no axis pointer at all).
//  2. Ref.Margin.Axis is not a known MarginAxis value.
//  3. Host is non-MATRIX (Request.Crosstab is nil).
//
// Each case must surface PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on
// the envelope. Acceptance criterion 2 of E1-S3.
func TestValidateOverlay_RefIncompatibleShape(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		req  *types.Request
	}{
		{
			name: "Ref.Margin nil",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindIndexVsMargin,
						Scope: types.OverlayScopeCell,
						Ref:   types.OverlayRef{},
					},
				},
			},
		},
		{
			name: "Margin.Axis invalid",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindIndexVsMargin,
						Scope: types.OverlayScopeCell,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: types.MarginAxis("diagonal")},
						},
					},
				},
			},
		},
		{
			name: "host non-MATRIX (no Crosstab)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "value"},
				},
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindIndexVsMargin,
						Scope: types.OverlayScopeCell,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := PredictFromBytes(data, tc.req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE; got %v", codes)
			}
		})
	}
}

// TestValidateOverlay_ScopeUnsupported asserts that a known overlay
// kind (OVERLAY_INDEX_VS_MARGIN) paired with a non-CELL scope surfaces
// PULSE_OVERLAY_SCOPE_UNSUPPORTED. E1 ships CELL only; ROW / COLUMN /
// TOTAL widen the gate in later epics.
func TestValidateOverlay_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindIndexVsMargin,
						Scope: scope,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
						},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_HappyPath asserts that a well-formed
// OVERLAY_INDEX_VS_MARGIN overlay riding on a MATRIX-shaped crosstab
// passes the predict gate without surfacing any overlay-specific
// errors. Streamability still goes to false because crosstab Shape=
// matrix forces buffered — but no overlay code lands on the envelope.
func TestValidateOverlay_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Name:  "row_index",
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on happy-path request", code)
		}
	}
}

// TestValidateOverlay_ShareOfRow_HappyPath asserts a well-formed
// OVERLAY_SHARE_OF_ROW overlay riding on a MATRIX-shaped crosstab
// passes the predict gate without surfacing any overlay-specific
// errors. Matches the INDEX_VS_MARGIN happy-path contract — the
// kind-unknown gate auto-passes once the streamability row lands
// (E2-S1), and the per-kind scope-cell + ref-margin checks accept.
func TestValidateOverlay_ShareOfRow_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Name:  "row_share",
				Kind:  types.OverlayKindShareOfRow,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on SHARE_OF_ROW happy-path request", code)
		}
	}
}

// TestValidateOverlay_ShareOfRow_ScopeUnsupported asserts the per-kind
// scope gate rejects non-CELL scopes on SHARE_OF_ROW. Mirrors the
// INDEX_VS_MARGIN scope check — SHARE_OF_ROW is CELL-only by
// construction.
func TestValidateOverlay_ShareOfRow_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindShareOfRow,
						Scope: scope,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
						},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_ShareOfCol_HappyPath asserts a well-formed
// OVERLAY_SHARE_OF_COL overlay riding on a MATRIX-shaped crosstab
// passes the predict gate without surfacing any overlay-specific
// errors. Matches the SHARE_OF_ROW happy-path contract — the kind-
// unknown gate auto-passes once the streamability row lands
// (E2-S2), and the per-kind scope-cell + ref-margin checks accept.
func TestValidateOverlay_ShareOfCol_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Name:  "col_share",
				Kind:  types.OverlayKindShareOfCol,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on SHARE_OF_COL happy-path request", code)
		}
	}
}

// TestValidateOverlay_ShareOfCol_ScopeUnsupported asserts the per-kind
// scope gate rejects non-CELL scopes on SHARE_OF_COL. Mirrors the
// SHARE_OF_ROW scope check — SHARE_OF_COL is CELL-only by
// construction.
func TestValidateOverlay_ShareOfCol_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindShareOfCol,
						Scope: scope,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn},
						},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_ShareOfTotal_HappyPath asserts a well-formed
// OVERLAY_SHARE_OF_TOTAL overlay riding on a MATRIX-shaped crosstab
// passes the predict gate without surfacing any overlay-specific
// errors. Mirrors the SHARE_OF_ROW / SHARE_OF_COL happy-path
// contract — the kind-unknown gate auto-passes once the
// streamability row lands (E2-S3), and the per-kind scope-cell +
// ref-margin checks accept.
func TestValidateOverlay_ShareOfTotal_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Name:  "total_share",
				Kind:  types.OverlayKindShareOfTotal,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on SHARE_OF_TOTAL happy-path request", code)
		}
	}
}

// TestValidateOverlay_ShareOfTotal_ScopeUnsupported asserts the
// per-kind scope gate rejects non-CELL scopes on SHARE_OF_TOTAL.
// Mirrors the SHARE_OF_ROW / SHARE_OF_COL scope checks —
// SHARE_OF_TOTAL is CELL-only by construction.
func TestValidateOverlay_ShareOfTotal_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindShareOfTotal,
						Scope: scope,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand},
						},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_ZScoreVsMargin_HappyPath asserts a well-formed
// OVERLAY_ZSCORE_VS_MARGIN overlay riding on a MATRIX-shaped crosstab
// passes the predict gate without surfacing any overlay-specific
// errors. Unlike the SHARE_OF_* triad (each structurally axis-locked
// to one MarginAxis at runtime) ZSCORE_VS_MARGIN supports all three
// axes — the validator therefore runs the happy-path check for each
// axis to guard against an accidental axis-lock regression.
func TestValidateOverlay_ZScoreVsMargin_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, axis := range []types.MarginAxis{
		types.MarginAxisRow,
		types.MarginAxisColumn,
		types.MarginAxisGrand,
	} {
		t.Run(string(axis), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Name:  "zscore_" + string(axis),
						Kind:  types.OverlayKindZScoreVsMargin,
						Scope: types.OverlayScopeCell,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: axis},
						},
					},
				},
			}

			env := PredictFromBytes(data, req, nil)

			for _, code := range []errors.Code{
				errors.PULSE_OVERLAY_KIND_UNKNOWN,
				errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			} {
				if hasErrorCode(env, code) {
					t.Errorf("axis=%s: unexpected overlay error %s on ZSCORE_VS_MARGIN happy-path request",
						axis, code)
				}
			}
		})
	}
}

// TestValidateOverlay_ZScoreVsMargin_ScopeUnsupported asserts the per-
// kind scope gate rejects non-CELL scopes on ZSCORE_VS_MARGIN. Matches
// the INDEX_VS_MARGIN / SHARE_OF_* scope check — ZSCORE_VS_MARGIN is
// CELL-only by construction.
func TestValidateOverlay_ZScoreVsMargin_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindZScoreVsMargin,
						Scope: scope,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
						},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_DeltaVsMargin_HappyPath asserts a well-formed
// OVERLAY_DELTA_VS_MARGIN overlay riding on a MATRIX-shaped crosstab
// passes the predict gate without surfacing any overlay-specific
// errors. Like ZSCORE_VS_MARGIN the kind supports all three axes —
// the validator runs the happy-path check for each axis to guard
// against an accidental axis-lock regression.
func TestValidateOverlay_DeltaVsMargin_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, axis := range []types.MarginAxis{
		types.MarginAxisRow,
		types.MarginAxisColumn,
		types.MarginAxisGrand,
	} {
		t.Run(string(axis), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Name:  "delta_" + string(axis),
						Kind:  types.OverlayKindDeltaVsMargin,
						Scope: types.OverlayScopeCell,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: axis},
						},
					},
				},
			}

			env := PredictFromBytes(data, req, nil)

			for _, code := range []errors.Code{
				errors.PULSE_OVERLAY_KIND_UNKNOWN,
				errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			} {
				if hasErrorCode(env, code) {
					t.Errorf("axis=%s: unexpected overlay error %s on DELTA_VS_MARGIN happy-path request",
						axis, code)
				}
			}
		})
	}
}

// TestValidateOverlay_DeltaVsMargin_ScopeUnsupported asserts the
// per-kind scope gate rejects non-CELL scopes on DELTA_VS_MARGIN.
// Matches the INDEX_VS_MARGIN / SHARE_OF_* / ZSCORE_VS_MARGIN scope
// check — DELTA_VS_MARGIN is CELL-only by construction.
func TestValidateOverlay_DeltaVsMargin_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindDeltaVsMargin,
						Scope: scope,
						Ref: types.OverlayRef{
							Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
						},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_ChiSqMatrix_HappyPath asserts a well-formed
// OVERLAY_CHISQ_MATRIX overlay riding on a MATRIX-shaped crosstab
// passes the predict gate without surfacing any overlay-specific
// errors. CHISQ_MATRIX is the first inferential / SCALAR-shape Crosstab
// overlay: Scope=MATRIX, Ref=empty (implicit-margin).
func TestValidateOverlay_ChiSqMatrix_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Name:  "chisq",
				Kind:  types.OverlayKindChiSqMatrix,
				Scope: types.OverlayScopeMatrix,
				Ref:   types.OverlayRef{},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on CHISQ_MATRIX happy-path request", code)
		}
	}
}

// TestValidateOverlay_ChiSqMatrix_ScopeUnsupported asserts the per-kind
// scope gate rejects every non-MATRIX scope on CHISQ_MATRIX.
// CHISQ_MATRIX is a whole-table inferential overlay — CELL / ROW /
// COLUMN / TOTAL / GROUP scopes are all rejected with
// PULSE_OVERLAY_SCOPE_UNSUPPORTED.
func TestValidateOverlay_ChiSqMatrix_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeCell,
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindChiSqMatrix,
						Scope: scope,
						Ref:   types.OverlayRef{},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_ChiSqMatrix_RefRejected asserts the validator
// rejects a CHISQ_MATRIX spec that populates any Ref-family pointer.
// CHISQ_MATRIX is implicit-margin (uses the host's row / column /
// grand margins inline) — any Ref pointer is a shape mismatch and
// must fire PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_ChiSqMatrix_RefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqMatrix,
				Scope: types.OverlayScopeMatrix,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when CHISQ_MATRIX populates Ref.Margin; got %v",
			codes)
	}
}

// TestValidateOverlay_ChiSqMatrix_NoCrosstabHostRejected asserts the
// validator rejects a CHISQ_MATRIX overlay when the request has no
// crosstab — the test consumes a row × col contingency table sourced
// from the host crosstab, and without one there is no MATRIX host to
// test against.
func TestValidateOverlay_ChiSqMatrix_NoCrosstabHostRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		// No Crosstab — base agg only.
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "value"},
		},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqMatrix,
				Scope: types.OverlayScopeMatrix,
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when CHISQ_MATRIX is missing crosstab host; got %v",
			codes)
	}
}

// TestValidateOverlay_ChiSqRow_HappyPath asserts a well-formed
// OVERLAY_CHISQ_ROW overlay riding on a MATRIX-shaped crosstab passes
// the predict gate without surfacing any overlay-specific errors.
// CHISQ_ROW is the first SERIES-shape Crosstab overlay: Scope=ROW,
// Ref=empty (implicit-margin).
func TestValidateOverlay_ChiSqRow_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Name:  "chisq_row",
				Kind:  types.OverlayKindChiSqRow,
				Scope: types.OverlayScopeRow,
				Ref:   types.OverlayRef{},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on CHISQ_ROW happy-path request", code)
		}
	}
}

// TestValidateOverlay_ChiSqRow_ScopeUnsupported asserts the per-kind
// scope gate rejects every non-ROW scope on CHISQ_ROW. CHISQ_ROW emits
// one statistic per row tuple — CELL / COLUMN / MATRIX / TOTAL / GROUP
// scopes are all rejected with PULSE_OVERLAY_SCOPE_UNSUPPORTED.
func TestValidateOverlay_ChiSqRow_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeCell,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindChiSqRow,
						Scope: scope,
						Ref:   types.OverlayRef{},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_ChiSqRow_RefRejected asserts the validator
// rejects a CHISQ_ROW spec that populates any Ref-family pointer.
// CHISQ_ROW is implicit-margin (mirrors CHISQ_MATRIX) — any Ref
// pointer is a shape mismatch and must fire
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_ChiSqRow_RefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqRow,
				Scope: types.OverlayScopeRow,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when CHISQ_ROW populates Ref.Margin; got %v",
			codes)
	}
}

// TestValidateOverlay_ChiSqRow_NoCrosstabHostRejected asserts the
// validator rejects a CHISQ_ROW overlay when the request has no
// crosstab — the per-row test consumes a row × col contingency table
// sourced from the host crosstab, and without one there is no MATRIX
// host to test against.
func TestValidateOverlay_ChiSqRow_NoCrosstabHostRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		// No Crosstab — base agg only.
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "value"},
		},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqRow,
				Scope: types.OverlayScopeRow,
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when CHISQ_ROW is missing crosstab host; got %v",
			codes)
	}
}

// TestValidateOverlay_ChiSqCol_HappyPath asserts a well-formed
// OVERLAY_CHISQ_COL overlay riding on a MATRIX-shaped crosstab passes
// the predict gate without surfacing any overlay-specific errors.
// CHISQ_COL is the mechanical column-axis twin of CHISQ_ROW:
// Scope=COLUMN, Ref=empty (implicit-margin).
func TestValidateOverlay_ChiSqCol_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Name:  "chisq_col",
				Kind:  types.OverlayKindChiSqCol,
				Scope: types.OverlayScopeColumn,
				Ref:   types.OverlayRef{},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on CHISQ_COL happy-path request", code)
		}
	}
}

// TestValidateOverlay_ChiSqCol_ScopeUnsupported asserts the per-kind
// scope gate rejects every non-COLUMN scope on CHISQ_COL. CHISQ_COL
// emits one statistic per column tuple — CELL / ROW / MATRIX / TOTAL /
// GROUP scopes are all rejected with PULSE_OVERLAY_SCOPE_UNSUPPORTED.
func TestValidateOverlay_ChiSqCol_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeCell,
		types.OverlayScopeRow,
		types.OverlayScopeMatrix,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindChiSqCol,
						Scope: scope,
						Ref:   types.OverlayRef{},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_ChiSqCol_RefRejected asserts the validator
// rejects a CHISQ_COL spec that populates any Ref-family pointer.
// CHISQ_COL is implicit-margin (mirrors CHISQ_MATRIX / CHISQ_ROW) —
// any Ref pointer is a shape mismatch and must fire
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_ChiSqCol_RefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqCol,
				Scope: types.OverlayScopeColumn,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when CHISQ_COL populates Ref.Margin; got %v",
			codes)
	}
}

// TestValidateOverlay_FisherExactCell_HappyPath asserts a well-formed
// OVERLAY_FISHER_EXACT_CELL overlay riding on a MATRIX-shaped crosstab
// passes the predict gate without surfacing any overlay-specific
// errors. FISHER_EXACT_CELL is implicit-margin (mirrors the CHISQ_*
// family): Scope=CELL, Ref=empty. PRD § 4.C FR-C2 closes the E2
// inferential overlay family.
func TestValidateOverlay_FisherExactCell_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Name:  "fisher",
				Kind:  types.OverlayKindFisherExactCell,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on FISHER_EXACT_CELL happy-path request", code)
		}
	}
}

// TestValidateOverlay_FisherExactCell_ScopeUnsupported asserts the
// per-kind scope gate rejects every non-CELL scope on
// FISHER_EXACT_CELL. The kind emits one p-value per cell — ROW /
// COLUMN / MATRIX / TOTAL / GROUP scopes are all rejected with
// PULSE_OVERLAY_SCOPE_UNSUPPORTED.
func TestValidateOverlay_FisherExactCell_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeGroup,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindFisherExactCell,
						Scope: scope,
						Ref:   types.OverlayRef{},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_FisherExactCell_RefRejected asserts the validator
// rejects a FISHER_EXACT_CELL spec that populates any Ref-family
// pointer. FISHER_EXACT_CELL is implicit-margin (mirrors CHISQ_MATRIX
// / CHISQ_ROW / CHISQ_COL) — any Ref pointer is a shape mismatch and
// must fire PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_FisherExactCell_RefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindFisherExactCell,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when FISHER_EXACT_CELL populates Ref.Margin; got %v",
			codes)
	}
}

// TestValidateOverlay_FisherExactCell_NoCrosstabHostRejected asserts
// the validator rejects a FISHER_EXACT_CELL overlay when the request
// has no crosstab — the per-cell test consumes a row × col contingency
// table sourced from the host crosstab, and without one there is no
// MATRIX host to test against.
func TestValidateOverlay_FisherExactCell_NoCrosstabHostRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		// No Crosstab — base agg only.
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "value"},
		},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindFisherExactCell,
				Scope: types.OverlayScopeCell,
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when FISHER_EXACT_CELL is missing crosstab host; got %v",
			codes)
	}
}

// TestValidateOverlay_ExpectedLowWarn pins the predict-vs-runtime
// boundary for the canonical χ² low-expected-count warning code
// PULSE_OVERLAY_EXPECTED_LOW. Predict is no-execute: it sees the
// request, the .pulse file header, and the schema — but NOT the
// records the crosstab will eventually fold. Expected-count
// computation (row_margin × col_margin / grand_total < 5) requires
// observing the per-cell counts the host crosstab produces from those
// records, which is data only the runtime path holds.
//
// Contract pinned by this test:
//
//   - PredictFromBytes does NOT emit PULSE_OVERLAY_EXPECTED_LOW on a
//     well-formed CHISQ_MATRIX overlay — the per-kind predict gate
//     accepts the spec without inspecting records, and the warning
//     surfaces only after ApplyOverlays consumes the materialised host
//     matrix at runtime.
//   - The same applies to CHISQ_ROW, CHISQ_COL, and FISHER_EXACT_CELL
//     (every inferential overlay that emits PULSE_OVERLAY_EXPECTED_LOW
//     at runtime is silent at predict time).
//
// Runtime emission lives at
// processing/overlay_chisq_matrix_test.go::TestOverlay_ChiSqMatrix_ExpectedLowEmitsWarn
// (and its CHISQ_ROW / CHISQ_COL / FISHER_EXACT_CELL siblings). Those
// tests cover the warning's positive surface; this test pins the
// negative surface at predict time so a future refactor that pushes
// the warning into ValidateOverlays (and accidentally drops it from
// runtime) fails closed.
//
// This is the "warn, not error" test the E2-S12 story names — predict
// does not promote PULSE_OVERLAY_EXPECTED_LOW to an envelope error,
// AND predict does not surface it as a warning either; the warning is
// deferred to the runtime overlay handler, which has the records to
// compute expected counts.
func TestValidateOverlay_ExpectedLowWarn(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	// Every E2 inferential overlay that emits PULSE_OVERLAY_EXPECTED_LOW
	// at runtime must be silent at predict time.
	cases := []struct {
		name string
		req  *types.Request
	}{
		{
			name: "CHISQ_MATRIX",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindChiSqMatrix,
						Scope: types.OverlayScopeMatrix,
					},
				},
			},
		},
		{
			name: "CHISQ_ROW",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindChiSqRow,
						Scope: types.OverlayScopeRow,
					},
				},
			},
		},
		{
			name: "CHISQ_COL",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindChiSqCol,
						Scope: types.OverlayScopeColumn,
					},
				},
			},
		},
		{
			name: "FISHER_EXACT_CELL",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindFisherExactCell,
						Scope: types.OverlayScopeCell,
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := PredictFromBytes(data, tc.req, nil)

			// Predict must NOT surface PULSE_OVERLAY_EXPECTED_LOW on the
			// envelope's Errors slice — the warning is runtime-only.
			if hasErrorCode(env, errors.PULSE_OVERLAY_EXPECTED_LOW) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("predict emitted PULSE_OVERLAY_EXPECTED_LOW; expected runtime-only emission. Envelope errors: %v",
					codes)
			}

			// Predict must NOT surface PULSE_OVERLAY_EXPECTED_LOW on the
			// envelope's Warnings slice either — the warning is keyed to
			// per-cell expected counts that only the runtime path computes
			// (predict cannot see records, only schema). Asserting against
			// Warnings closes the loop: predict is silent on this code in
			// both directions.
			for _, w := range env.Warnings {
				if w.Code == string(errors.PULSE_OVERLAY_EXPECTED_LOW) {
					t.Fatalf("predict surfaced PULSE_OVERLAY_EXPECTED_LOW as a warning; expected runtime-only emission. Envelope warnings: %+v",
						env.Warnings)
				}
			}

			// Sanity check: the per-kind predict gates still accept the
			// spec (none of the structural overlay errors land). If a
			// structural error landed the negative assertion above would
			// pass vacuously — exercise the positive happy-path surface
			// the per-kind happy-path tests already pin so this test
			// fails closed if the structural gates regress.
			for _, code := range []errors.Code{
				errors.PULSE_OVERLAY_KIND_UNKNOWN,
				errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			} {
				if hasErrorCode(env, code) {
					t.Errorf("unexpected structural overlay error %s on well-formed %s spec; envelope errors: %+v",
						code, tc.name, env.Errors)
				}
			}
		})
	}
}

// indexVsTotalSeriesHostReq returns a minimal grouped Process request
// (the SERIES-host predicate INDEX_VS_TOTAL targets — Groups non-empty,
// Crosstab nil) that the per-test fixtures consume.
func indexVsTotalSeriesHostReq() *types.Request {
	return &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// TestValidateOverlay_IndexVsTotal_HappyPath asserts a well-formed
// OVERLAY_INDEX_VS_TOTAL overlay riding on a grouped Process request
// (SERIES host) passes the predict gate without surfacing any overlay-
// specific errors. INDEX_VS_TOTAL is implicit-grand-total: Scope=GROUP,
// Ref=empty.
func TestValidateOverlay_IndexVsTotal_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := indexVsTotalSeriesHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "idx_total",
			Kind:  types.OverlayKindIndexVsTotal,
			Scope: types.OverlayScopeGroup,
			Ref:   types.OverlayRef{},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
		errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on INDEX_VS_TOTAL happy-path request", code)
		}
	}
}

// TestValidateOverlay_IndexVsTotal_ScopeUnsupported asserts the per-
// kind scope gate rejects every non-GROUP scope on INDEX_VS_TOTAL. The
// kind emits one statistic per group tuple — CELL / ROW / COLUMN /
// MATRIX / TOTAL scopes are all rejected with
// PULSE_OVERLAY_SCOPE_UNSUPPORTED.
func TestValidateOverlay_IndexVsTotal_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeCell,
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := indexVsTotalSeriesHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindIndexVsTotal,
					Scope: scope,
					Ref:   types.OverlayRef{},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_IndexVsTotal_RefRejected asserts the validator
// rejects an INDEX_VS_TOTAL spec that populates any Ref-family
// pointer. INDEX_VS_TOTAL is implicit-grand-total (the host series'
// own grand total is the denominator) — any Ref pointer is a shape
// mismatch and must fire PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// (mirrors the CHISQ_* / FISHER_EXACT_CELL implicit-margin contract).
func TestValidateOverlay_IndexVsTotal_RefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := indexVsTotalSeriesHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsTotal,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when INDEX_VS_TOTAL populates Ref.Margin; got %v",
			codes)
	}
}

// TestValidateOverlay_IndexVsTotal_NoSeriesHostRejected asserts the
// validator rejects an INDEX_VS_TOTAL overlay when the request has no
// SERIES host — INDEX_VS_TOTAL needs a grouped Process result
// (Request.Groups non-empty AND Request.Crosstab nil). Both halves of
// that predicate are exercised: no groupers, and the crosstab-active
// case.
func TestValidateOverlay_IndexVsTotal_NoSeriesHostRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		req  *types.Request
	}{
		{
			name: "no groupers (ungrouped)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "value"},
				},
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindIndexVsTotal,
						Scope: types.OverlayScopeGroup,
					},
				},
			},
		},
		{
			name: "crosstab host (MATRIX, not SERIES)",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindIndexVsTotal,
						Scope: types.OverlayScopeGroup,
					},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := PredictFromBytes(data, tc.req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when INDEX_VS_TOTAL is missing SERIES host; got %v",
					codes)
			}
		})
	}
}

// TestValidateOverlay_IndexVsTotal_LevelWithinRejected asserts the
// validator rejects an INDEX_VS_TOTAL spec that sets Level or Within
// to non-zero. INDEX_VS_TOTAL is implicit-grand-total: the host
// series' grand total is a single scalar that does not partition by
// any axis prefix, so Level / Within would alter the implicit-grand-
// total contract (mirrors the χ² / Fisher inferential family's
// non-zero rejection).
func TestValidateOverlay_IndexVsTotal_LevelWithinRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name        string
		level       int
		within      int
	}{
		{name: "level=1", level: 1},
		{name: "within=1", within: 1},
		{name: "both", level: 1, within: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := indexVsTotalSeriesHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:   types.OverlayKindIndexVsTotal,
					Scope:  types.OverlayScopeGroup,
					Level:  tc.level,
					Within: tc.within,
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on level=%d within=%d; got %v",
					tc.level, tc.within, codes)
			}
		})
	}
}

// TestValidateOverlay_ShareOfTotal_Series_HappyPath asserts a well-
// formed OVERLAY_SHARE_OF_TOTAL overlay riding on a grouped Process
// request (SERIES host — E3-S3 sibling dispatch to INDEX_VS_TOTAL)
// passes the predict gate without surfacing any overlay-specific
// errors. SERIES SHARE_OF_TOTAL is implicit-grand-total: Scope=GROUP,
// Ref=empty. Mirrors TestValidateOverlay_IndexVsTotal_HappyPath.
func TestValidateOverlay_ShareOfTotal_Series_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := indexVsTotalSeriesHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "share_total",
			Kind:  types.OverlayKindShareOfTotal,
			Scope: types.OverlayScopeGroup,
			Ref:   types.OverlayRef{},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
		errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on SERIES SHARE_OF_TOTAL happy-path request", code)
		}
	}
}

// TestValidateOverlay_ShareOfTotal_Series_ScopeUnsupported asserts the
// per-kind scope gate rejects every non-GROUP scope on a SERIES
// SHARE_OF_TOTAL spec. The SERIES dispatch emits one statistic per
// group tuple — CELL / ROW / COLUMN / MATRIX / TOTAL scopes are all
// rejected with PULSE_OVERLAY_SCOPE_UNSUPPORTED. Mirrors
// TestValidateOverlay_IndexVsTotal_ScopeUnsupported.
func TestValidateOverlay_ShareOfTotal_Series_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeCell,
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := indexVsTotalSeriesHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindShareOfTotal,
					Scope: scope,
					Ref:   types.OverlayRef{},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED; got %v",
					scope, codes)
			}
		})
	}
}

// TestValidateOverlay_ShareOfTotal_Series_RefRejected asserts the
// validator rejects a SERIES SHARE_OF_TOTAL spec that populates any
// Ref-family pointer. SERIES SHARE_OF_TOTAL is implicit-grand-total
// (mirrors INDEX_VS_TOTAL) — any Ref pointer is a shape mismatch and
// must fire PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_ShareOfTotal_Series_RefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := indexVsTotalSeriesHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfTotal,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when SERIES SHARE_OF_TOTAL populates Ref.Margin; got %v",
			codes)
	}
}

// TestValidateOverlay_ShareOfTotal_Series_LevelWithinRejected asserts
// the validator rejects a SERIES SHARE_OF_TOTAL spec that sets Level or
// Within to non-zero. SERIES SHARE_OF_TOTAL is implicit-grand-total:
// the host series' grand total is a single scalar that does not
// partition by any axis prefix, so Level / Within would alter the
// implicit-grand-total contract. Mirrors
// TestValidateOverlay_IndexVsTotal_LevelWithinRejected.
func TestValidateOverlay_ShareOfTotal_Series_LevelWithinRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name   string
		level  int
		within int
	}{
		{name: "level=1", level: 1},
		{name: "within=1", within: 1},
		{name: "both", level: 1, within: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := indexVsTotalSeriesHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:   types.OverlayKindShareOfTotal,
					Scope:  types.OverlayScopeGroup,
					Level:  tc.level,
					Within: tc.within,
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on level=%d within=%d; got %v",
					tc.level, tc.within, codes)
			}
		})
	}
}

// TestValidateOverlay_EmptySliceNoop asserts the validator is a no-op
// for requests without overlays — predict still runs every other gate
// untouched.
func TestValidateOverlay_EmptySliceNoop(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "value"},
		},
	}

	env := PredictFromBytes(data, req, nil)

	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s when Request.Overlays is empty", code)
		}
	}
}
