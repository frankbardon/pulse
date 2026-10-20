package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/types"
)

// applyRegressionLowNEff attaches one PULSE_WEIGHT_LOW_NEFF warning per
// fitted regression weighted under kind probability whose Kish n_eff
// is at or below its raw-row floor (regression.LowNEffFloor: p + 1; a
// REG_GLM at dispersion 1 only below it, having no residual df; a gamma
// fit, whose estimated dispersion reads the residual df, like OLS) —
// details {regression, type, n_eff, min_required} — or, under strict,
// returns the first as an error instead. results are in specs order
// (one per spec, as both fit paths return them); a result without
// n_eff (unweighted or frequency-weighted) is skipped.
func applyRegressionLowNEff(resp *types.Response, specs []*types.RegressionSpec, results []*types.RegressionResult, strict bool) error {
	if resp == nil {
		return nil
	}
	for i, res := range results {
		if res == nil || res.Error != nil || res.NEff == 0 || i >= len(specs) || specs[i] == nil {
			continue
		}
		spec := specs[i]
		min := regression.LowNEffFloor(spec)
		glm := spec.Type == types.REG_GLM
		// A residual-df fit (OLS, penalised OLS, and a gamma GLM, whose
		// estimated dispersion and t reference read N* − p − 1 — the fit
		// reports it as DF, NaN when not positive) warns at n_eff ≤ p + 1:
		// at equality its df is already 0 (U12 review WS-05). A
		// dispersion-1 GLM's Wald z has no residual df, so it warns only
		// below.
		residualDF := !glm || res.DF != 0
		if res.NEff > float64(min) || (!residualDF && res.NEff == float64(min)) {
			continue
		}
		label := spec.Name
		if label == "" {
			label = fmt.Sprintf("regressions[%d]", i)
		}
		details := map[string]any{"regression": label, "type": string(spec.Type), "n_eff": res.NEff, "min_required": min}
		consequence := "its residual df N* − p − 1 is not positive, so the standard errors, residual standard error, adjusted R² and p-values are undefined (null)"
		bound := "is not above"
		if glm && residualDF {
			consequence = "its residual df N* − p − 1 is not positive, so the estimated dispersion, the standard errors, p-values and intervals are undefined (null)"
		} else if glm {
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

// regressionSlotResults pairs each regression spec's fit with its fit
// error (index-aligned with specs): a data-dependent refusal
// (slotfail.RegressionCodes) becomes that slot's result carrying the
// error — its Name and Type, no figure — so the other slots survive;
// any other error is returned and fails the run. Shared by the buffered
// and streaming paths so the two can never disagree on a failed slot.
func regressionSlotResults(specs []*types.RegressionSpec, fits []*types.RegressionResult, fitErrs []error) ([]*types.RegressionResult, error) {
	out := make([]*types.RegressionResult, len(fits))
	for i, res := range fits {
		if i < len(fitErrs) && fitErrs[i] != nil {
			se, ok := slotfail.From(slotfail.KindRegressions, fitErrs[i])
			if !ok {
				return nil, fitErrs[i]
			}
			res = &types.RegressionResult{Error: se}
			if i < len(specs) && specs[i] != nil {
				res.Name, res.Type = specs[i].Name, specs[i].Type
			}
		}
		out[i] = res
	}
	return out, nil
}
