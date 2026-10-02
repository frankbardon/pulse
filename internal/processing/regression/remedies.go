package regression

import "github.com/frankbardon/pulse/types"

// Remedy clauses: the pieces of regression refusal prose that point the
// caller at ANOTHER regression type. Each is a pure function of the
// instance's offered predicate (nil = everything offered); the nil
// rendering is the historical literal, so a refusal built from
// remedy(nil) is byte-identical on an instance without a feature
// profile. processing.ExtensionRegistry.ScopeRefusal swaps each default
// rendering for the instance's, so a refusal never names a regression
// type the instance hides.

func regOffered(offered func(string) bool, t types.RegressionType) bool {
	return offered == nil || offered(string(t))
}

// remedyGLMAlpha completes the REG_GLM Alpha refusal.
func remedyGLMAlpha(offered func(string) bool) string {
	if regOffered(offered, types.REG_OLS) {
		return "; use REG_OLS for regularized fits or wait for the GLM regularization phase"
	}
	return "; wait for the GLM regularization phase"
}

// remedyBayesResample completes the REG_BAYES_LINEAR Resample refusal.
func remedyBayesResample(offered func(string) bool) string {
	if regOffered(offered, types.REG_OLS) {
		return "; use REG_OLS if you want resample-based uncertainty"
	}
	return ""
}

// remedyBayesSelection completes the REG_BAYES_LINEAR Selection refusal.
func remedyBayesSelection(offered func(string) bool) string {
	if regOffered(offered, types.REG_OLS) {
		return ". Use REG_OLS for greedy AIC/BIC selection"
	}
	return ""
}

// remedyBayesPenalty completes the REG_BAYES_LINEAR Penalty refusal.
func remedyBayesPenalty(offered func(string) bool) string {
	if regOffered(offered, types.REG_OLS) {
		return " or switch to REG_OLS"
	}
	return ""
}

// remedyBayesFamily completes the REG_BAYES_LINEAR Family refusal.
func remedyBayesFamily(offered func(string) bool) string {
	if regOffered(offered, types.REG_GLM) {
		return "; Family is a REG_GLM knob"
	}
	return ""
}

// remedyBayesLink completes the REG_BAYES_LINEAR Link refusal.
func remedyBayesLink(offered func(string) bool) string {
	if regOffered(offered, types.REG_GLM) {
		return "; Link is a REG_GLM knob"
	}
	return ""
}

// Remedies lists every regression remedy clause, for
// processing.ExtensionRegistry.ScopeRefusal.
func Remedies() []func(offered func(string) bool) string {
	return []func(func(string) bool) string{
		remedyGLMAlpha,
		remedyBayesResample,
		remedyBayesSelection,
		remedyBayesPenalty,
		remedyBayesFamily,
		remedyBayesLink,
	}
}
