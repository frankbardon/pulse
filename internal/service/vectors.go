package service

import (
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// appendVectorWarnings adds one PULSE_VECTOR_UNREFERENCED warning per
// Request.Vectors entry no operator slot references, in request order —
// the warning predict reports — then each PULSE_VECTOR_METRIC_UNSUITED
// issue (vectors.MetricUnsuitedIssues, predict's list; under strict
// metricUnsuitedRefusal refused the run before dispatch). Vector resolution itself runs in the
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
	for _, w := range vectors.MetricUnsuitedIssues(req) {
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
			Code:    string(w.Code),
			Message: w.Message,
			Details: w.Details,
		})
	}
}

// metricUnsuitedRefusal is the strict channel of
// PULSE_VECTOR_METRIC_UNSUITED: under strict the first issue refuses the
// run before any record is read (predict reports every issue as an
// error); otherwise nil and appendVectorWarnings carries them.
func metricUnsuitedRefusal(strict bool, req *types.Request) error {
	if !strict {
		return nil
	}
	if issues := vectors.MetricUnsuitedIssues(req); len(issues) > 0 {
		return issues[0]
	}
	return nil
}
