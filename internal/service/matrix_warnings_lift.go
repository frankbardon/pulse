package service

import (
	"maps"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// A matrix result's own Warnings stay on the result (MatrixResult.Warnings)
// — a fit note such as PULSE_MATRIX_NOT_CONVERGED or _NEAREST_PD rides
// beside the figures it qualifies. The closed set below names the matrix
// warnings that mark a figure as MISSING rather than qualified: a null :=
// row (PULSE_MATRIX_DEFINED_NONFINITE) or a declared group level that is
// no group of the fit (PULSE_MATRIX_GROUP_LEVEL_EMPTY). Left on the
// result alone they were silent to every consumer reading the envelope's
// top-level warnings (issue #393), so, like a regression result's
// warnings (regression_warnings.go), each reaches the caller two ways,
// applied once in Service.process after every arm returns:
//
//   - escalateMatrixWarnings, under Strict, turns each matrix result
//     carrying one into a FAILED slot with that warning's code, message
//     and details — slotfail.Apply then fails the run with the slot's
//     own code plus {slot_kind, index, label};
//   - liftMatrixWarnings, otherwise, appends a copy of each to
//     Response.Warnings, details.index (the request slot index) added
//     (and details.group_key on a grouped bucket). The result keeps its
//     own copy.
//
// A response without such a warning is untouched, byte-identical.

// liftedMatrixWarnings are the matrix warning codes lifted to
// Response.Warnings and escalated under Strict.
var liftedMatrixWarnings = map[string]bool{
	string(errors.PULSE_MATRIX_DEFINED_NONFINITE): true,
	string(errors.PULSE_MATRIX_GROUP_LEVEL_EMPTY): true,
}

// matrixSlotIndex is the request slot index of the matrix result named
// name (k, its response position, when no spec matches).
func matrixSlotIndex(req *types.Request, name string, k int) int {
	if req != nil {
		for i := range req.Matrices {
			if req.Matrices[i].Name == name {
				return i
			}
		}
	}
	return k
}

// escalateMatrixWarnings fails, under strict, every matrix result of
// resp carrying a lifted warning, with its first such warning's code.
func escalateMatrixWarnings(resp *types.Response, strict bool) {
	if !strict || resp == nil {
		return
	}
	for k := range resp.Matrices {
		r := &resp.Matrices[k]
		if r.Error != nil {
			continue
		}
		for _, w := range r.Warnings {
			if w == nil || !liftedMatrixWarnings[w.Code] {
				continue
			}
			resp.Matrices[k] = types.MatrixResult{
				Name:        r.Name,
				Type:        r.Type,
				GroupKey:    r.GroupKey,
				GroupHeader: r.GroupHeader,
				Error:       &types.SlotError{Code: w.Code, Message: w.Message, Details: maps.Clone(w.Details)},
			}
			break
		}
	}
}

// liftMatrixWarnings appends each matrix result's lifted warnings to
// resp.Warnings, in result then warning order, each located by its
// slot.
func liftMatrixWarnings(req *types.Request, resp *types.Response) {
	if resp == nil {
		return
	}
	for k := range resp.Matrices {
		r := &resp.Matrices[k]
		if r.Error != nil {
			continue
		}
		for _, w := range r.Warnings {
			if w == nil || !liftedMatrixWarnings[w.Code] {
				continue
			}
			details := make(map[string]any, len(w.Details)+2)
			maps.Copy(details, w.Details)
			if _, set := details["index"]; !set {
				details["index"] = matrixSlotIndex(req, r.Name, k)
			}
			if _, set := details["group_key"]; !set && len(r.GroupKey) > 0 {
				details["group_key"] = r.GroupKey
			}
			resp.Warnings = append(resp.Warnings, &types.ResponseWarning{Code: w.Code, Message: w.Message, Details: details})
		}
	}
}
