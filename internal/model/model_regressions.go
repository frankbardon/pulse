package model

import (
	"slices"

	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// A model regression is a built-in REG_* type authored on the
// extension contract (extend.Regression): it is registered in every
// instance and listed in types.AllRegressionTypes, but its fit is shaped
// like an extension regression's — it names its own coefficients beside
// the intercept and the predictors, a saved fit is a model file version
// 3, and its scorer scores the whole model (no params.terms, no
// lower / upper bound). Unlike an extension's, its coefficient names are
// declared: ModelRegressionTerms derives them from the spec alone, so
// predict validates a coef() term statically.

// The curve-coefficient prefixes of the media-mix models: channel c's
// Hill half-saturation is the coefficient MMMHalfSaturationPrefix + c,
// its shape MMMShapePrefix + c, and under REG_MMM_BAYES its adstock
// decay MMMDecayPrefix + c; REG_MMM_BAYES's noise SD is MMMSigmaName.
const (
	MMMHalfSaturationPrefix = "k:"
	MMMShapePrefix          = "s:"
	MMMDecayPrefix          = "decay:"
	MMMSigmaName            = "sigma"
)

// modelRegressions are the model regressions, in AllRegressionTypes
// order.
var modelRegressions = []types.RegressionType{types.REG_MMM_BAYES, types.REG_MMM_HILL}

// ModelRegression reports whether t is a model regression: a built-in
// authored on the extension contract (see the file comment).
func ModelRegression(t types.RegressionType) bool {
	return slices.Contains(modelRegressions, t)
}

// ModelRegressions returns the model regressions, in a fresh slice.
func ModelRegressions() []types.RegressionType {
	return slices.Clone(modelRegressions)
}

// ModelRegressionTerms is a model regression spec's term catalog: the
// coefficient names its fit reports beyond the intercept and the
// predictors, derived from the spec alone, and whether they are known.
// REG_MMM_HILL names "k:<channel>" for every params.channels entry, then
// "s:<channel>" for each; REG_MMM_BAYES names "decay:<channel>", then
// "k:<channel>", then "s:<channel>" for each, then "sigma". A spec whose
// params do not decode (its factory refuses it), or a type that is no
// model regression, reports false — the caller then treats the names as
// unknown.
func ModelRegressionTerms(spec *types.RegressionSpec) ([]string, bool) {
	if spec == nil || (spec.Type != types.REG_MMM_HILL && spec.Type != types.REG_MMM_BAYES) {
		return nil, false
	}
	var p struct {
		Channels []string `json:"channels"`
	}
	// A lenient look: the model's factory decodes (and records) params.
	if len(spec.Params) == 0 || paramread.Probe(spec.Params, &p) != nil || len(p.Channels) == 0 {
		return nil, false
	}
	prefixes := []string{MMMHalfSaturationPrefix, MMMShapePrefix}
	if spec.Type == types.REG_MMM_BAYES {
		prefixes = []string{MMMDecayPrefix, MMMHalfSaturationPrefix, MMMShapePrefix}
	}
	out := make([]string, 0, len(prefixes)*len(p.Channels)+1)
	for _, prefix := range prefixes {
		for _, c := range p.Channels {
			out = append(out, prefix+c)
		}
	}
	if spec.Type == types.REG_MMM_BAYES {
		out = append(out, MMMSigmaName)
	}
	return out, true
}

// ModelRegressionFieldInputs is the fields a model regression spec reads
// per record beyond its target and predictors, derived from the spec
// alone — a built-in's twin of an extension's FieldInputs hook. The
// runtime projection decodes them and predict's field-reference walk
// refuses an unknown one. REG_MMM_BAYES reads params.time; every other
// type, and a spec whose params do not decode, reads none (nil).
func ModelRegressionFieldInputs(spec *types.RegressionSpec) []string {
	if spec == nil || spec.Type != types.REG_MMM_BAYES || len(spec.Params) == 0 {
		return nil
	}
	var p struct {
		Time string `json:"time"`
	}
	if paramread.Probe(spec.Params, &p) != nil || p.Time == "" {
		return nil
	}
	return []string{p.Time}
}
