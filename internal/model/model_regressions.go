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

// The curve-coefficient prefixes of REG_MMM_HILL: channel c's Hill
// half-saturation is the coefficient MMMHalfSaturationPrefix + c, its
// shape MMMShapePrefix + c.
const (
	MMMHalfSaturationPrefix = "k:"
	MMMShapePrefix          = "s:"
)

// modelRegressions are the model regressions, in AllRegressionTypes
// order.
var modelRegressions = []types.RegressionType{types.REG_MMM_HILL}

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
// "s:<channel>" for each. A spec whose params do not decode (its factory
// refuses it), or a type that is no model regression, reports false —
// the caller then treats the names as unknown.
func ModelRegressionTerms(spec *types.RegressionSpec) ([]string, bool) {
	if spec == nil || spec.Type != types.REG_MMM_HILL {
		return nil, false
	}
	var p struct {
		Channels []string `json:"channels"`
	}
	// A lenient look: the model's factory decodes (and records) params.
	if len(spec.Params) == 0 || paramread.Probe(spec.Params, &p) != nil || len(p.Channels) == 0 {
		return nil, false
	}
	out := make([]string, 0, 2*len(p.Channels))
	for _, c := range p.Channels {
		out = append(out, MMMHalfSaturationPrefix+c)
	}
	for _, c := range p.Channels {
		out = append(out, MMMShapePrefix+c)
	}
	return out, true
}
