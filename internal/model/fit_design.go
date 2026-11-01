package model

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// fit_design.go is the bound-fit half of the influence figures (U65
// E1-S2): ATTR_REG_LEVERAGE, ATTR_REG_INFLUENCE and ATTR_REG_RESIDUAL
// kind standardized / studentized naming params.fit read the fit's
// DESIGN — its coefficient covariance Vcov = φ·(XᵀWX)⁻¹, its dispersion
// φ and its residual df — instead of refitting OLS. FitDesign is the
// static face of what a fit carries for that; CheckFitDesign is the one
// rule over it, run inside CheckFitReference by the field-reference
// pass (predict and runtime) and the attribute's bind alike, so a
// refusal is the same code, message and details on either side. The
// runtime takes the face off the bound result (Bound.Signature), predict
// off the fit request as written (CatalogFromFits, finished by
// WithDefaultWeight) or the loaded model file (CatalogWithModels).

// FitDesign is a regression fit's influence face: what reading its
// leverage and influence figures requires, without the fit's numbers.
type FitDesign struct {
	// Family is the GLM error family ("" on REG_OLS and every other
	// type: Gaussian, identity).
	Family string
	// Vcov reports that the fit carries its coefficient covariance
	// (RegressionSpec.Vcov).
	Vcov bool
	// Dispersion reports that the fit has the dispersion its figures
	// read: φ fixed at 1 (binomial, poisson), or its estimate carried
	// (RegressionResult.Dispersion, REG_OLS / gamma / a fixed-θ
	// negative binomial under dispersion "estimated" beside a classical
	// vcov; the negative binomial's φ is otherwise 1). A model file written before the field existed carries a
	// vcov without it.
	Dispersion bool
	// Robust: the covariance is a robust sandwich, not φ·(XᵀWX)⁻¹.
	Robust bool
	// Penalized, Selection and Resample echo those modifiers.
	Penalized bool
	Selection bool
	Resample  bool
	// Constrained: a REG_OLS `bounds` fit (its covariance is undefined
	// once a bound binds). NoIntercept: a through-origin REG_OLS fit.
	// Both are known from the spec only — a model file does not keep
	// them (its vcov carries the nulls they leave).
	Constrained bool
	NoIntercept bool
	// Weighted: the fit read a row weight. The static view of a fit
	// whose regression names no weight and whose request none knows it
	// only with the instance default weight (WithDefaultWeight).
	Weighted bool
}

// designFromResult is the face of a finished fit's result: the
// runtime's bound fit and a loaded model file (File.Result) alike.
func designFromResult(res *types.RegressionResult) FitDesign {
	if res == nil {
		return FitDesign{}
	}
	return FitDesign{
		Family:     res.Family,
		Vcov:       res.Vcov != nil,
		Dispersion: res.Dispersion != 0 || !estimatedDispersion(res.Type, res.Family),
		Robust:     res.Covariance != nil,
		Penalized:  res.Penalty != "",
		Selection:  res.Selection != "",
		Resample:   res.Resample != "",
		Weighted:   res.SumWeights != 0,
	}
}

// designFromSpec is the static face of a fit request's regression as
// written: what the fit will carry when it runs — the engine attaches
// the dispersion beside a classical (not robust, penalized or
// resampled) vcov wherever φ is estimated. Weighted reads the explicit
// weights only (weightInherited is the default-weight question).
func designFromSpec(req *types.Request, reg *types.RegressionSpec) FitDesign {
	robust, penalized, resample := reg.Covariance.Robust(), reg.Penalty != "", reg.Resample != ""
	return FitDesign{
		Family:      reg.Family,
		Vcov:        reg.Vcov,
		Dispersion:  !estimatedDispersion(reg.Type, reg.Family) || reg.Vcov && !robust && !penalized && !resample,
		Robust:      robust,
		Penalized:   penalized,
		Selection:   reg.Selection != "",
		Resample:    resample,
		Constrained: reg.Bounds.Active(),
		NoIntercept: reg.Intercept != nil && !*reg.Intercept,
		Weighted:    !reg.Weight.IsNull() && (!reg.Weight.IsZero() || req != nil && req.Weight != nil),
	}
}

// weightInherited reports whether reg (in fit request req) names no
// weight of its own and req none: the fit is weighted exactly when the
// instance has a default weight.
func weightInherited(req *types.Request, reg *types.RegressionSpec) bool {
	return reg.Weight.IsZero() && (req == nil || req.Weight == nil)
}

// WithDefaultWeight settles the static faces of fits (the list c was
// built from, CatalogFromFits) under the instance default weight
// (pulse.Options.DefaultWeight, on = set): every regression fit that
// inherits it (weightInherited) is weighted. Predict's catalog of a
// `fits` list and the runtime's failed-fit signature both call it, so
// the two judge an inheriting fit alike. Returns c (modified in place).
func (c FitCatalog) WithDefaultWeight(fits []types.FitSpec, on bool) FitCatalog {
	if !on {
		return c
	}
	for _, f := range fits {
		sig, ok := c[f.Name]
		if !ok || f.Request == nil || sig.EffectiveKind() != types.FitKindRegression || len(f.Request.Regressions) == 0 {
			continue
		}
		if weightInherited(f.Request, f.Request.Regressions[0]) {
			sig.Influence.Weighted = true
			c[f.Name] = sig
		}
	}
	return c
}

// estimatedDispersion reports whether a fit of type t and family
// estimates its dispersion (REG_OLS σ̂², gamma's Pearson χ²/df); a
// binomial or poisson fit fixes φ = 1.
func estimatedDispersion(t types.RegressionType, family string) bool {
	return t == types.REG_OLS || family == "gamma"
}

// FitDesignKind reports whether a (routed as t) with params p reads the
// bound fit's design: ATTR_REG_LEVERAGE, ATTR_REG_INFLUENCE, or an
// ATTR_REG_RESIDUAL of kind standardized / studentized.
func FitDesignKind(t types.AttributeType, p FitParams) bool {
	switch t {
	case types.ATTR_REG_LEVERAGE, types.ATTR_REG_INFLUENCE:
		return true
	case types.ATTR_REG_RESIDUAL:
		return ScaledResidualKind(p.Kind)
	}
	return false
}

// CheckFitDesign is the rule over a design-reading params.fit slot
// (FitDesignKind) bound to sig: the fit is a built-in REG_OLS or REG_GLM
// (a Bayesian-linear, extension or model regression is
// type_unsupported), its covariance is the classical φ·(XᵀWX)⁻¹ it
// carries (vcov, not robust, no penalty / selection / resample / bounds,
// with an intercept), its dispersion is carried where it is estimated,
// and it is unweighted. Every other fault is PULSE_MODEL_SCORE_INVALID
// reason params_invalid with details.requires naming the missing face.
// The first fault wins.
func CheckFitDesign(t types.AttributeType, p FitParams, sig Signature) *errors.CodedError {
	// A residual names its kind: "ATTR_REG_RESIDUAL kind studentized
	// over a …".
	prefix := ""
	if t == types.ATTR_REG_RESIDUAL {
		prefix = "kind " + p.Kind + " "
	}
	if sig.Type != types.REG_OLS && sig.Type != types.REG_GLM {
		return scoreInvalidFor(t, p.Fit, ReasonTypeUnsupported,
			prefix+"reads a bound fit's leverage, which only a REG_OLS or REG_GLM fit defines; got a "+string(sig.Type)+" fit — bind a REG_OLS or REG_GLM fit, or drop params.fit to refit OLS on the slot's own target and predictors",
			map[string]any{"type": string(sig.Type)})
	}
	d := sig.Influence
	refuse := func(requires, msg string) *errors.CodedError {
		return scoreInvalidFor(t, p.Fit, ReasonParamsInvalid, prefix+msg,
			map[string]any{"requires": requires, "type": string(sig.Type)})
	}
	switch {
	case d.Penalized:
		return refuse("unpenalized", "over a penalized bound fit is not supported (its leverage needs the regularized resolvent); bind an unpenalized fit")
	case d.Selection:
		return refuse("no_selection", "over a selection bound fit is not supported (the selected design's influence ignores the search); bind a fit without selection")
	case d.Resample:
		return refuse("no_resample", "over a resampled bound fit is not supported (its vcov is the replicate covariance, not φ·(XᵀWX)⁻¹); bind a fit without resample")
	case d.Robust:
		return refuse("classical_covariance", "over a robust-covariance bound fit is not supported (its vcov is a sandwich, not φ·(XᵀWX)⁻¹); bind a fit with the classical covariance")
	case d.Constrained:
		return refuse("unconstrained", "over a bounds-constrained bound fit is not supported; bind a fit without bounds")
	case d.NoIntercept:
		return refuse("intercept", "over a through-origin bound fit (intercept: false) is not supported; bind a fit with an intercept")
	case d.Weighted:
		return refuse("unweighted", "influence over a weighted bound fit is not supported; refit unweighted (weight: null on the fit's regression), or drop params.fit to refit OLS on the slot's own fields")
	case !d.Vcov:
		return refuse("vcov", "reads the bound fit's coefficient covariance, which it does not carry; set vcov: true on the fit's regression")
	case !d.Dispersion && estimatedDispersion(sig.Type, d.Family):
		return refuse("dispersion", "reads the bound fit's estimated dispersion, which it does not carry (a model file saved by an earlier build); refit, or re-save the model with this build")
	}
	return nil
}
