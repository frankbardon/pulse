package service

import (
	"strconv"

	descx "github.com/frankbardon/pulse/internal/descriptor"
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
func attachRegressionAdvisories(req *types.Request, resp *types.Response, inst *descx.InstanceSnapshot) {
	if req == nil || resp == nil || inst.AdvisorySuppressed(string(regpenalty.InferenceAdvisoryCode)) {
		return
	}
	for i, res := range resp.Regressions {
		if res == nil || res.Error != nil || i >= len(req.Regressions) {
			continue
		}
		spec := req.Regressions[i]
		if spec == nil || !regpenalty.InferenceUndefined(spec) {
			continue
		}
		msg, details := regpenalty.InferenceAdvisory(spec, "regressions["+strconv.Itoa(i)+"]")
		res.Advisories = append(res.Advisories, types.Advisory{
			Code:    string(regpenalty.InferenceAdvisoryCode),
			Message: msg,
			Details: details,
		})
	}
}
