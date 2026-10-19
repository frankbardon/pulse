package overlayhost

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The MATRIX_RESULT host: Request.Overlays kinds that read a request's
// finished Response.Matrices results (types.MatrixValues — never the
// crosstab MatrixPayload). OverlayRef.Matrix names one Request.Matrices
// spec by its effective name; the kind reads every group bucket of its
// result. It runs on a process request and on a Compose slot's own
// Request.Overlays; the Compose, chain and facet overlay slots refuse
// it through Refusal, and a ProcessChain stage's Request.Overlays
// through StageRefusal.

// The OVERLAY_MATRIX_GROUP_SUMMARY parts beside its primary per-cell
// mean (every one a MATRIX on the union of the buckets' keys).
const (
	GroupSummaryPartMin     = "min"
	GroupSummaryPartMax     = "max"
	GroupSummaryPartRange   = "range"
	GroupSummaryPartNGroups = "n_groups"
)

// GroupSummaryParts lists the OVERLAY_MATRIX_GROUP_SUMMARY part names
// in emission order.
func GroupSummaryParts() []string {
	return []string{GroupSummaryPartMin, GroupSummaryPartMax, GroupSummaryPartRange, GroupSummaryPartNGroups}
}

// MatrixResultRefusal judges the Request.Overlays spec at index whose
// kind routes to route, when route runs on the MATRIX_RESULT host (any
// other route: nil). Predict (descriptor.ValidateOverlays) and the
// runtime dispatcher (processing.ApplyMatrixResultOverlays) both call
// it after the slot and shape refusals, so the refusal — code, message
// and Details — is identical on both sides. First hit wins:
//
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE: Ref.Matrix unset or
//     empty, or another Ref arm populated.
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED: Scope other than matrix.
//   - PULSE_OVERLAY_LEVEL_OUT_OF_RANGE: a non-zero Level / Within (no
//     crosstab axis to address).
//   - PULSE_OVERLAY_PARAM_MISSING: any params (the kind takes none).
//   - PULSE_OVERLAY_REFERENCE_UNKNOWN (which "matrix"): Ref.Matrix.Name
//     names no Request.Matrices spec's effective name.
//   - PULSE_OVERLAY_GROUPS_REQUIRED: the request carries no
//     Request.Groups (one result: nothing to summarize across).
func MatrixResultRefusal(req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int) *errors.CodedError {
	if req == nil || spec == nil || !RunsOnMatrixResult(route) {
		return nil
	}
	kind := string(spec.Kind)
	ref := spec.Ref
	matrix := ref.Matrix
	ref.Matrix = nil
	if matrix == nil || matrix.Name == "" || ref != (types.OverlayRef{}) {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+kind+" needs Ref.Matrix {name} naming a Request.Matrices spec, and no other Ref arm",
			map[string]any{"index": index, "kind": kind})
	}
	if spec.Scope != types.OverlayScopeMatrix {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			"overlay "+kind+" does not support scope "+strconv.Quote(string(spec.Scope))+" (supports: matrix)",
			map[string]any{"index": index, "kind": kind, "scope": string(spec.Scope)})
	}
	if spec.Level != 0 || spec.Within != 0 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
			"overlay "+kind+" does not support Level / Within (a matrix result has no crosstab axis to address)",
			map[string]any{"index": index, "kind": kind, "level": spec.Level, "within": spec.Within})
	}
	if !emptyParams(spec.Params) {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+kind+" takes no params",
			map[string]any{"index": index, "kind": kind})
	}
	names := make([]string, 0, len(req.Matrices))
	found := false
	for i := range req.Matrices {
		name := req.Matrices[i].EffectiveName()
		names = append(names, name)
		found = found || name == matrix.Name
	}
	if !found {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN,
			"overlay "+kind+" Ref.Matrix.Name "+strconv.Quote(matrix.Name)+" names no Request.Matrices spec",
			map[string]any{"index": index, "kind": kind, "which": "matrix", "name": matrix.Name, "matrices": names})
	}
	if len(req.Groups) == 0 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_GROUPS_REQUIRED,
			"overlay "+kind+" summarizes matrix "+strconv.Quote(matrix.Name)+" across group buckets, but the request has no Request.Groups",
			map[string]any{"index": index, "kind": kind, "name": matrix.Name})
	}
	return nil
}

// emptyParams reports whether raw is absent, null or an empty object.
func emptyParams(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return true
	}
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && m != nil && len(m) == 0
}

// StageRefusal refuses a MATRIX_RESULT kind (route) on a ProcessChain
// stage's own Request.Overlays: the MATRIX_RESULT host reaches a
// process request and a Compose slot only. Code
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, Details {index, kind,
// host: "chain", stage}. nil for any other route. Predict
// (ValidateChainWithOptions) and the chain runtime share it.
func StageRefusal(kind, route types.OverlayKind, stage, index int) *errors.CodedError {
	if !RunsOnMatrixResult(route) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+string(kind)+" does not run on a ProcessChain stage's Request.Overlays; it reads matrix results on a process request or a Compose slot only",
		map[string]any{"index": index, "kind": string(kind), "host": string(Chain), "stage": stage})
}
