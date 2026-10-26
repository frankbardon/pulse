package service

import (
	"fmt"
	"maps"

	"github.com/frankbardon/pulse/types"
)

// A regression result's own Warnings (an extension model's
// extend.RegressionOutput.Warnings, copied on by the adapter) reach the
// caller two ways, both applied once in Service.process after every arm
// returns, so streaming, buffered, parallel and shard arms agree:
//
//   - escalateRegressionWarnings, under Strict, turns each warned slot
//     into a FAILED slot carrying its first warning's code, message and
//     details — the per-slot failure slotfail.Apply then fails the run
//     with (the slot's own code plus {slot_kind, index, label});
//   - liftRegressionWarnings, otherwise, appends a copy of each to
//     Response.Warnings — details.regression (the slot label) and
//     details.index (the request slot index) added unless the model set
//     them — which the envelope promotes like any run-time warning. The
//     result keeps its own copy.
//
// A result without warnings is untouched, so its response is
// byte-identical.

// escalateRegressionWarnings fails, under strict, every regression slot
// of resp whose result carries a warning, with that warning's code.
func escalateRegressionWarnings(resp *types.Response, strict bool) {
	if !strict || resp == nil {
		return
	}
	for i, r := range resp.Regressions {
		if r == nil || r.Error != nil || len(r.Warnings) == 0 {
			continue
		}
		w := r.Warnings[0]
		resp.Regressions[i] = &types.RegressionResult{
			Name: r.Name,
			Type: r.Type,
			Error: &types.SlotError{
				Code:    w.Code,
				Message: w.Message,
				Details: maps.Clone(w.Details),
			},
		}
	}
}

// liftRegressionWarnings appends each regression result's warnings to
// resp.Warnings, in slot then warning order, each located by its slot.
func liftRegressionWarnings(resp *types.Response) {
	if resp == nil {
		return
	}
	for i, r := range resp.Regressions {
		if r == nil || r.Error != nil {
			continue
		}
		label := r.Name
		if label == "" {
			label = fmt.Sprintf("regressions[%d]", i)
		}
		for _, w := range r.Warnings {
			if w == nil {
				continue
			}
			details := make(map[string]any, len(w.Details)+2)
			maps.Copy(details, w.Details)
			if _, set := details["regression"]; !set {
				details["regression"] = label
			}
			if _, set := details["index"]; !set {
				details["index"] = i
			}
			resp.Warnings = append(resp.Warnings, &types.ResponseWarning{Code: w.Code, Message: w.Message, Details: details})
		}
	}
}
