package service

import (
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// targetLeakageRefusal is the strict channel of
// PULSE_FEAT_TARGET_LEAKAGE_RISK (#309): under strict the first
// FEAT_TARGET_ENCODE refuses the run before any record is read —
// predict reports every one as an error. Otherwise nil, and
// appendTargetLeakageWarnings repeats predict's warnings on the
// response. The rule is descx.TargetLeakageWarnings, predict's own.
func (s *Service) targetLeakageRefusal(req *types.Request) error {
	if !s.strict {
		return nil
	}
	if issues := descx.TargetLeakageWarnings(req, s.instance); len(issues) > 0 {
		return issues[0]
	}
	return nil
}

// appendTargetLeakageWarnings adds one PULSE_FEAT_TARGET_LEAKAGE_RISK
// per FEAT_TARGET_ENCODE in req, in request order — the warnings
// predict reports, with its message and details.
func (s *Service) appendTargetLeakageWarnings(req *types.Request, resp *types.Response) {
	if resp == nil {
		return
	}
	for _, w := range descx.TargetLeakageWarnings(req, s.instance) {
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
			Code:    string(w.Code),
			Message: w.Message,
			Details: w.Details,
		})
	}
}
