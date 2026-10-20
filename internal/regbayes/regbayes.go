// Package regbayes is the one rule over a REG_BAYES_LINEAR spec's knobs
// (types.RegressionSpec Prior / PriorMu / PriorPrecision / PriorShape /
// PriorRate / CredibleLevel, the refused Resample / Selection modifiers
// and the other engines' knobs), shared by the no-execute predict pass
// (internal/descriptor) and the regression engine
// (internal/processing/regression) so the two refuse the same requests
// with the same code, message and details. It reads the spec only —
// never a schema or a record — and imports neither side.
//
// A refusal that names another regression type as the fix carries a
// remedy clause (Remedies): a pure function of the instance's offered
// predicate whose nil rendering is the historical literal. The runtime
// renders nil and processing.ExtensionRegistry.ScopeRefusal swaps in the
// instance's rendering; predict renders the instance's directly, so both
// sides print the same message.
package regbayes

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Check returns the PROCESSING_CONFIG refusal of spec, or nil. Every
// type but REG_BAYES_LINEAR passes. offered is the instance's offered
// predicate for the remedy clauses (nil = everything offered). Rules,
// first fault wins:
//
//   - Resample and Selection are refused: the posterior already carries
//     the uncertainty, and conjugate-NIG selection is not offered.
//   - Prior is "" (nig) or "nig".
//   - a scalar PriorPrecision, PriorShape, PriorRate is ≥ 0 (zero selects
//     the engine default 1e-3).
//   - a vector PriorPrecision has length len(Predictors)+1 (intercept
//     first) and every entry finite and ≥ 0 (0 = a flat prior on that
//     coefficient; whether the posterior stays proper is the data's
//     question — the engine's RANK_DEFICIENT refusal).
//   - CredibleLevel, when set, lies in (0, 1).
//   - PriorMu, when set, has length len(Predictors)+1.
//   - Penalty, Alpha, L1Ratio, Unpenalized, Family, Link are refused
//     (other engines' knobs).
func Check(spec *types.RegressionSpec, offered func(string) bool) *errors.CodedError {
	if spec == nil || spec.Type != types.REG_BAYES_LINEAR {
		return nil
	}
	if spec.Resample != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR does not accept Resample: the posterior already conveys uncertainty (credible intervals via the Student-t marginal)"+RemedyResample(offered),
			map[string]any{"resample": spec.Resample, "type": string(spec.Type)},
		)
	}
	if spec.Selection != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR does not accept Selection: Bayesian feature selection is a posterior-based question (e.g., spike-and-slab); the conjugate-NIG engine doesn't support it"+RemedySelection(offered),
			map[string]any{"selection": spec.Selection, "type": string(spec.Type)},
		)
	}
	switch spec.Prior {
	case "", "nig":
		// ok — empty defaults to "nig"
	default:
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR Prior must be one of \"\", \"nig\"; got "+spec.Prior,
			map[string]any{"prior": spec.Prior, "allowed": []string{"", "nig"}},
		)
	}

	if cerr := checkPrecision(spec); cerr != nil {
		return cerr
	}
	if spec.PriorShape < 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR PriorShape must be > 0 (zero selects the engine default of 1e-3)",
			map[string]any{"prior_shape": spec.PriorShape},
		)
	}
	if spec.PriorRate < 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR PriorRate must be > 0 (zero selects the engine default of 1e-3)",
			map[string]any{"prior_rate": spec.PriorRate},
		)
	}
	if spec.CredibleLevel != 0 && (spec.CredibleLevel <= 0 || spec.CredibleLevel >= 1) {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR CredibleLevel must satisfy 0 < CredibleLevel < 1 (zero selects the default of 0.95)",
			map[string]any{"credible_level": spec.CredibleLevel},
		)
	}
	if len(spec.PriorMu) != 0 && len(spec.PriorMu) != len(spec.Predictors)+1 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR PriorMu length must equal len(Predictors)+1 (one intercept + one per predictor)",
			map[string]any{
				"prior_mu_len":    len(spec.PriorMu),
				"predictors_len":  len(spec.Predictors),
				"expected_length": len(spec.Predictors) + 1,
			},
		)
	}

	// Reject knobs that belong to other engines. Silently ignoring them
	// would hide caller confusion about which operator they're talking to.
	if spec.Penalty != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR does not accept Penalty; remove it"+RemedyPenalty(offered),
			map[string]any{"penalty": spec.Penalty, "type": string(spec.Type)},
		)
	}
	if spec.Alpha != 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR does not accept Alpha; the Inverse-Gamma prior on σ² is parameterized by PriorShape / PriorRate",
			map[string]any{"alpha": spec.Alpha, "type": string(spec.Type)},
		)
	}
	if spec.L1Ratio != 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR does not accept L1Ratio",
			map[string]any{"l1_ratio": spec.L1Ratio, "type": string(spec.Type)},
		)
	}
	if len(spec.Unpenalized) != 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR does not accept Unpenalized; a per-coefficient prior_precision (0 = flat) leaves a coefficient unshrunk",
			map[string]any{"unpenalized": spec.Unpenalized, "type": string(spec.Type)},
		)
	}
	if spec.Family != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR does not accept Family"+RemedyFamily(offered),
			map[string]any{"family": spec.Family, "type": string(spec.Type)},
		)
	}
	if spec.Link != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR does not accept Link"+RemedyLink(offered),
			map[string]any{"link": spec.Link, "type": string(spec.Type)},
		)
	}
	return nil
}

// checkPrecision is the PriorPrecision rule: a scalar ≥ 0; a vector of
// length len(Predictors)+1 with every entry finite and ≥ 0.
func checkPrecision(spec *types.RegressionSpec) *errors.CodedError {
	pp := spec.PriorPrecision
	if !pp.IsVector() {
		if pp.Scalar() < 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_BAYES_LINEAR PriorPrecision must be > 0 (zero selects the engine default of 1e-3)",
				map[string]any{"prior_precision": pp.Scalar()},
			)
		}
		return nil
	}
	v := pp.Vector()
	if want := len(spec.Predictors) + 1; len(v) != want {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_BAYES_LINEAR PriorPrecision vector length must equal len(Predictors)+1 (one intercept + one per predictor, PriorMu's order)",
			map[string]any{
				"prior_precision_len": len(v),
				"predictors_len":      len(spec.Predictors),
				"expected_length":     want,
			},
		)
	}
	for i, e := range v {
		if !(e >= 0) || math.IsInf(e, 1) {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_BAYES_LINEAR PriorPrecision entries must be finite and ≥ 0 (0 is a flat prior on that coefficient)",
				map[string]any{"index": i, "coefficient": coefficientName(spec, i), "value": e},
			)
		}
	}
	return nil
}

// coefficientName is the coefficient a precision index names:
// "(intercept)" then the predictors.
func coefficientName(spec *types.RegressionSpec, i int) string {
	if i == 0 {
		return "(intercept)"
	}
	return spec.Predictors[i-1]
}

func regOffered(offered func(string) bool, t types.RegressionType) bool {
	return offered == nil || offered(string(t))
}

// RemedyResample completes the REG_BAYES_LINEAR Resample refusal.
func RemedyResample(offered func(string) bool) string {
	if regOffered(offered, types.REG_OLS) {
		return "; use REG_OLS if you want resample-based uncertainty"
	}
	return ""
}

// RemedySelection completes the REG_BAYES_LINEAR Selection refusal.
func RemedySelection(offered func(string) bool) string {
	if regOffered(offered, types.REG_OLS) {
		return ". Use REG_OLS for greedy AIC/BIC selection"
	}
	return ""
}

// RemedyPenalty completes the REG_BAYES_LINEAR Penalty refusal.
func RemedyPenalty(offered func(string) bool) string {
	if regOffered(offered, types.REG_OLS) {
		return " or switch to REG_OLS"
	}
	return ""
}

// RemedyFamily completes the REG_BAYES_LINEAR Family refusal.
func RemedyFamily(offered func(string) bool) string {
	if regOffered(offered, types.REG_GLM) {
		return "; Family is a REG_GLM knob"
	}
	return ""
}

// RemedyLink completes the REG_BAYES_LINEAR Link refusal.
func RemedyLink(offered func(string) bool) string {
	if regOffered(offered, types.REG_GLM) {
		return "; Link is a REG_GLM knob"
	}
	return ""
}

// Remedies lists every remedy clause, for
// processing.ExtensionRegistry.ScopeRefusal.
func Remedies() []func(offered func(string) bool) string {
	return []func(func(string) bool) string{
		RemedyResample,
		RemedySelection,
		RemedyPenalty,
		RemedyFamily,
		RemedyLink,
	}
}
