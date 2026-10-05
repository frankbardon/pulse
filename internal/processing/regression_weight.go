package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// applyRegressionLowNEff attaches one PULSE_WEIGHT_LOW_NEFF warning per
// fitted regression weighted under kind probability whose Kish n_eff
// fell below its raw-row floor (regression.LowNEffFloor: p + 1) —
// details {regression, type, n_eff, min_required} — or, under strict,
// returns the first as an error instead. results are in specs order
// (one per spec, as both fit paths return them); a result without
// n_eff (unweighted or frequency-weighted) is skipped.
func applyRegressionLowNEff(resp *types.Response, specs []*types.RegressionSpec, results []*types.RegressionResult, strict bool) error {
	if resp == nil {
		return nil
	}
	for i, res := range results {
		if res == nil || res.NEff == 0 || i >= len(specs) || specs[i] == nil {
			continue
		}
		spec := specs[i]
		min := regression.LowNEffFloor(spec)
		if res.NEff >= float64(min) {
			continue
		}
		label := spec.Name
		if label == "" {
			label = fmt.Sprintf("regressions[%d]", i)
		}
		details := map[string]any{"regression": label, "type": string(spec.Type), "n_eff": res.NEff, "min_required": min}
		msg := fmt.Sprintf("%s: effective sample size n_eff = %.4g is below the %d the fit needs (predictors + 1); its residual df N* − p − 1 is negative, so the standard errors and p-values are undefined", label, res.NEff, min)
		if strict {
			return errors.NewCodedErrorWithDetails(errors.PULSE_WEIGHT_LOW_NEFF, msg, details)
		}
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
			Code:    string(errors.PULSE_WEIGHT_LOW_NEFF),
			Message: msg,
			Details: details,
		})
	}
	return nil
}
