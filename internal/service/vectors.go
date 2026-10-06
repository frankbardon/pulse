package service

import (
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// appendVectorWarnings adds one PULSE_VECTOR_UNREFERENCED warning per
// Request.Vectors entry no operator slot references, in request order —
// the warning predict reports. Vector resolution itself runs in the
// field-reference pass (checkFieldRefs), before any record is read, so
// every vector reaching here resolved.
func appendVectorWarnings(req *types.Request, resp *types.Response) {
	if resp == nil {
		return
	}
	for _, name := range vectors.Unreferenced(req) {
		w := vectors.UnreferencedWarning(name)
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
			Code:    string(w.Code),
			Message: w.Message,
			Details: w.Details,
		})
	}
}
