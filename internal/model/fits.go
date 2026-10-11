package model

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The PULSE_FIT_INVALID reasons.
const (
	ReasonNameMissing     = "name_missing"
	ReasonRequestMissing  = "request_missing"
	ReasonRegressionCount = "regression_count"
	ReasonGrouped         = "grouped"
)

// CheckFits is the structural rule set over a ComposedRequest `fits`
// list, run by the runtime (before any fit or slot runs) and by
// PredictCompose alike, so both refuse the same batch with the same
// error. Entries are judged in order, each fully before the next; the
// first fault wins:
//
//   - an empty name                            — PULSE_FIT_INVALID name_missing
//   - no request                               — PULSE_FIT_INVALID request_missing
//   - zero or more than one regression         — PULSE_FIT_INVALID regression_count
//   - groups or a crosstab                     — PULSE_FIT_INVALID grouped
//   - an invalid `save` path, the entry's own or its regression's
//     (CheckSavePath)                          — PULSE_MODEL_SAVE_INVALID
//   - a name an earlier entry already binds    — PULSE_MODEL_NAME_COLLISION
//
// Every refusal carries details.fit (the 0-based entry index) and
// details.name. Nil for an empty list. Every save path is judged here,
// before any fit runs, so no fit writes a model file in a batch a later
// entry's path would refuse.
func CheckFits(fits []types.FitSpec) error {
	seen := make(map[string]bool, len(fits))
	for i, f := range fits {
		if f.Name == "" {
			return fitInvalid(i, f.Name, ReasonNameMissing, "has no name", nil)
		}
		if f.Request == nil {
			return fitInvalid(i, f.Name, ReasonRequestMissing, "has no request", nil)
		}
		if n := len(f.Request.Regressions); n != 1 {
			return fitInvalid(i, f.Name, ReasonRegressionCount,
				fmt.Sprintf("holds %d regressions; a fit request holds exactly one", n),
				map[string]any{"count": n})
		}
		if f.Request.Regressions[0] == nil {
			return fitInvalid(i, f.Name, ReasonRegressionCount,
				"holds a null regression; a fit request holds exactly one",
				map[string]any{"count": 0})
		}
		if len(f.Request.Groups) > 0 || f.Request.Crosstab != nil {
			return fitInvalid(i, f.Name, ReasonGrouped,
				"is grouped; a fit is one ungrouped model (drop groups / crosstab)", nil)
		}
		cerr := (*errors.CodedError)(nil)
		if f.Save != "" {
			cerr = CheckSavePath(f.Save)
		}
		if cerr == nil {
			cerr = CheckRegressionSaves(f.Request.Regressions)
		}
		if cerr != nil {
			cerr.Details["fit"] = i
			cerr.Details["name"] = f.Name
			return cerr
		}
		if seen[f.Name] {
			return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_NAME_COLLISION,
				fmt.Sprintf("fits[%d]: model name %q is bound twice", i, f.Name),
				map[string]any{"fit": i, "name": f.Name})
		}
		seen[f.Name] = true
	}
	return nil
}

func fitInvalid(i int, name, reason, what string, extra map[string]any) error {
	details := map[string]any{"fit": i, "name": name, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_FIT_INVALID,
		fmt.Sprintf("fits[%d]: fit request %s", i, what), details)
}

// FitRequest returns the request a fit runs: a shallow clone of
// spec.Request with its `return` cleared. The fit's figures surface
// whole as ComposedResponse.Fits[name] (outside every `return`), and a
// `return` that skipped the regressions slot would leave nothing to
// bind. The caller's request is never mutated.
func FitRequest(spec types.FitSpec) *types.Request {
	if spec.Request == nil {
		return nil
	}
	r := *spec.Request
	r.Return = nil
	return &r
}
