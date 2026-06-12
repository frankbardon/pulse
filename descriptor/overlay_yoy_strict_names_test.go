package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E4-S9 strict-name wrappers for the YoY predict-side rejection
// battery. The brief named four tests verbatim:
//
//   - TestValidateOverlay_YoYFrequencyRequired (S7 — already
//     verbatim, no wrapper needed)
//   - TestValidateOverlay_YoYIncompatibleFrequency (S7 shipped as
//     TestValidateOverlay_YoYIncompatibleFrequencyRejected — wrap
//     under the brief name so a grep for the verbatim test name
//     surfaces a hit)
//   - TestValidateOverlay_YoYNonDateGrouper (S7 shipped as
//     TestValidateOverlay_YoYNonDateGrouperRejected — same
//     wrapping rationale)
//
// Each wrapper drives a single canonical-fixture predict invocation
// against the same gate the *Rejected suffix sibling already covers.
// Wrapping rather than renaming keeps the existing test history
// intact (the S7 commit hash 3284b17 lands the original tests) while
// satisfying the brief's "test names must appear verbatim" rule.
// The wrappers themselves are tiny — each calls PredictFromBytes once
// against a fixture that pins the corresponding rejection code.

// TestValidateOverlay_YoYIncompatibleFrequency asserts a frequency
// value outside the supported set fires
// PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY. Wraps the existing
// TestValidateOverlay_YoYIncompatibleFrequencyRejected (S7) under
// the brief's strict name.
func TestValidateOverlay_YoYIncompatibleFrequency(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			// "yearly" is not in the supported set
			// (annual|quarterly|monthly|weekly|daily|hourly).
			Params: json.RawMessage(`{"frequency": "yearly"}`),
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY) {
		t.Fatalf("expected PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY for unsupported frequency")
	}
}

// TestValidateOverlay_YoYNonDateGrouper asserts that a host whose
// first grouper is not GROUP_DATE fires
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Wraps the existing
// TestValidateOverlay_YoYNonDateGrouperRejected (S7) under the
// brief's strict name.
func TestValidateOverlay_YoYNonDateGrouper(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	// siblingHostReq uses GROUP_CATEGORY over "region" — not a DATE host.
	req := siblingHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for non-GROUP_DATE host")
	}
}
