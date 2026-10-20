package service

import (
	"strconv"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/regbounds"
	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/types"
)

// attachRegressionAdvisories writes the advisories a regression result
// carries beside its undefined figures (RegressionResult.Advisories):
// PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED on every fitted l1 /
// elasticnet slot without Resample, whose standard errors, p-values and
// intervals the engines report NaN. The predicate, message and details
// are internal/regpenalty's — the ones predict's advisory reads — so a
// result and its predict agree (the slot path is the result's own
// "regressions[i]"; predict prefixes a Compose slot's). A failed slot
// (Error) carries none, and a code in Options.SuppressAdvisories
// (InstanceSnapshot.AdvisorySuppressed) is never attached. Never a
// warning: Strict never sees it. resp.Regressions is index-aligned with
// req.Regressions (the slotfail / multiplicity contract).
//
// PULSE_ADVISORY_CONSTRAINT_ACTIVE rides a REG_OLS bounds fit without
// Resample whose bound bound (RegressionResult.ActiveBounds, set by the
// engine): result-only, since whether a bound binds depends on the data
// (internal/regbounds holds its predicate and wording).
func attachRegressionAdvisories(req *types.Request, resp *types.Response, inst *descx.InstanceSnapshot) {
	if req == nil || resp == nil {
		return
	}
	penalized := !inst.AdvisorySuppressed(string(regpenalty.InferenceAdvisoryCode))
	bounded := !inst.AdvisorySuppressed(string(regbounds.AdvisoryCode))
	for i, res := range resp.Regressions {
		if res == nil || res.Error != nil || i >= len(req.Regressions) {
			continue
		}
		spec := req.Regressions[i]
		if spec == nil {
			continue
		}
		slot := "regressions[" + strconv.Itoa(i) + "]"
		if penalized && regpenalty.InferenceUndefined(spec) {
			msg, details := regpenalty.InferenceAdvisory(spec, slot)
			res.Advisories = append(res.Advisories, types.Advisory{
				Code:    string(regpenalty.InferenceAdvisoryCode),
				Message: msg,
				Details: details,
			})
		}
		if bounded && regbounds.InferenceUndefined(spec, res.ActiveBounds) {
			msg, details := regbounds.Advisory(spec, slot, res.ActiveBounds)
			res.Advisories = append(res.Advisories, types.Advisory{
				Code:    string(regbounds.AdvisoryCode),
				Message: msg,
				Details: details,
			})
		}
	}
}
