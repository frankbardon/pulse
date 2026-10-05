package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// applyRegressionLowNEff attaches one PULSE_WEIGHT_LOW_NEFF warning per
// fitted regression weighted under kind probability whose Kish n_eff
// is at or below its raw-row floor (regression.LowNEffFloor: p + 1; a
// REG_GLM only below it, having no residual df) —
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
		glm := spec.Type == types.REG_GLM
		// A residual-df fit (OLS, penalised OLS) warns at n_eff ≤ p + 1:
		// at equality its df N* − p − 1 is already 0 (U12 review WS-05).
		// The GLM's Wald z has no residual df, so it warns only below.
		if res.NEff > float64(min) || (glm && res.NEff == float64(min)) {
			continue
		}
		label := spec.Name
		if label == "" {
			label = fmt.Sprintf("regressions[%d]", i)
		}
		details := map[string]any{"regression": label, "type": string(spec.Type), "n_eff": res.NEff, "min_required": min}
		consequence := "its residual df N* − p − 1 is not positive, so the standard errors, residual standard error, adjusted R² and p-values are undefined (null)"
		bound := "is not above"
		if glm {
			bound = "is below"
			// The GLM's Wald z has no residual df; its covariance still
			// rests on fewer effective observations than parameters.
			consequence = "the Wald standard errors and p-values rest on fewer effective observations than parameters and are unreliable"
		}
		msg := fmt.Sprintf("%s: effective sample size n_eff = %.4g %s the %d the fit needs (predictors + 1); %s", label, res.NEff, bound, min, consequence)
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
